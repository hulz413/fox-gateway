package jobs

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"fox-gateway/internal/domain"
	"fox-gateway/internal/store"
	"fox-gateway/internal/worker/claudecode"
)

type WorkerRequest struct {
	JobID             string
	ChatID            string
	Prompt            string
	WorkspaceRoot     string
	ClaudePath        string
	Mutating          bool
	ResumeSessionID   string
	SessionGeneration int64
}

type Callback func(context.Context, domain.Job) error
type ConfirmationHandler func(context.Context, domain.Job, claudecode.Result) (domain.Job, error)

type Runner struct {
	store          *store.Store
	worker         *claudecode.Runner
	workspaceLock  *WorkspaceLock
	chatLock       *KeyedLock
	readonlyTokens chan struct{}
	onUpdate       Callback
	onConfirmation ConfirmationHandler
	wg             sync.WaitGroup
}

func NewRunner(st *store.Store, worker *claudecode.Runner, maxReadOnly int, onUpdate Callback, onConfirmation ConfirmationHandler) *Runner {
	return &Runner{
		store:          st,
		worker:         worker,
		workspaceLock:  NewWorkspaceLock(),
		chatLock:       NewKeyedLock(),
		readonlyTokens: make(chan struct{}, maxReadOnly),
		onUpdate:       onUpdate,
		onConfirmation: onConfirmation,
	}
}

func (r *Runner) Enqueue(req WorkerRequest) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		ctx := context.Background()
		r.chatLock.Acquire(req.ChatID)
		defer r.chatLock.Release(req.ChatID)
		if req.Mutating {
			r.workspaceLock.Acquire(req.JobID)
			defer r.workspaceLock.Release(req.JobID)
		} else {
			r.readonlyTokens <- struct{}{}
			defer func() { <-r.readonlyTokens }()
		}
		_ = r.execute(ctx, req)
	}()
}

func (r *Runner) Wait() {
	r.wg.Wait()
}

func (r *Runner) execute(ctx context.Context, req WorkerRequest) error {
	job, err := r.store.GetJob(ctx, req.JobID)
	if err != nil {
		return err
	}
	job.Status = domain.JobStatusRunning
	job.UpdatedAt = time.Now().UTC()
	if err := r.store.UpdateJob(ctx, job); err != nil {
		return err
	}
	if r.onUpdate != nil {
		_ = r.onUpdate(ctx, job)
	}

	req, err = r.refreshConversationContext(ctx, req)
	if err != nil {
		job.Status = domain.JobStatusFailed
		job.ErrorText = err.Error()
		job.ResultSummary = ""
		job.UpdatedAt = time.Now().UTC()
		if updateErr := r.store.UpdateJob(ctx, job); updateErr != nil {
			return updateErr
		}
		if r.onUpdate != nil {
			_ = r.onUpdate(ctx, job)
		}
		return nil
	}

	runCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	updatedReq, result, runErr := r.runClaude(runCtx, req)
	if runErr == nil {
		if err := r.persistConversationSession(ctx, updatedReq, result); err != nil {
			runErr = err
		}
	}
	ws := domain.WorkerSession{
		JobID:      req.JobID,
		PID:        result.PID,
		Command:    result.Command,
		Status:     "finished",
		StartedAt:  result.StartedAt,
		FinishedAt: &result.FinishedAt,
		ExitCode:   &result.ExitCode,
		Stdout:     result.Stdout,
		Stderr:     result.Stderr,
	}
	if runErr != nil {
		ws.Status = "failed"
		ws.ErrorText = runErr.Error()
	}
	_ = r.store.SaveWorkerSession(ctx, ws)

	if runErr != nil {
		job.Status = domain.JobStatusFailed
		job.ErrorText = runErr.Error()
		job.ResultSummary = summarizeResult(result)
	} else if result.RequesterConfirmation != nil && r.onConfirmation != nil {
		job, runErr = r.onConfirmation(ctx, job, result)
		if runErr != nil {
			job.Status = domain.JobStatusFailed
			job.ErrorText = runErr.Error()
			job.ResultSummary = summarizeResult(result)
		}
	} else {
		job.Status = domain.JobStatusSucceeded
		job.ResultSummary = summarizeResult(result)
	}
	job.UpdatedAt = time.Now().UTC()
	if err := r.store.UpdateJob(ctx, job); err != nil {
		return err
	}
	if r.onUpdate != nil {
		_ = r.onUpdate(ctx, job)
	}
	return nil
}

func (r *Runner) runClaude(ctx context.Context, req WorkerRequest) (WorkerRequest, claudecode.Result, error) {
	request := claudecode.Request{
		JobID:           req.JobID,
		ClaudePath:      req.ClaudePath,
		WorkspaceRoot:   req.WorkspaceRoot,
		Prompt:          claudecode.AppendRequesterConfirmationContract(req.Prompt),
		Mutating:        req.Mutating,
		Async:           true,
		OutputFormat:    "json",
		ResumeSessionID: req.ResumeSessionID,
	}
	result, err := r.worker.Run(ctx, request)
	if err == nil || strings.TrimSpace(req.ResumeSessionID) == "" || !claudecode.IsMissingSessionError(err, result) {
		return req, result, err
	}
	if clearErr := r.store.ClearConversationSession(ctx, req.ChatID, time.Now().UTC()); clearErr != nil && !store.IsNotFound(clearErr) {
		return req, result, fmt.Errorf("clear conversation session: %w", clearErr)
	}
	req.ResumeSessionID = ""
	req.SessionGeneration++
	request.ResumeSessionID = ""
	result, err = r.worker.Run(ctx, request)
	return req, result, err
}

func (r *Runner) persistConversationSession(ctx context.Context, req WorkerRequest, result claudecode.Result) error {
	resumedSessionID := strings.TrimSpace(req.ResumeSessionID)
	sessionID := strings.TrimSpace(result.SessionID)
	if sessionID == "" && resumedSessionID != "" {
		sessionID = resumedSessionID
	}
	if sessionID == "" {
		return nil
	}
	if resumedSessionID != "" && sessionID == resumedSessionID {
		return nil
	}
	err := r.store.UpdateConversationSession(ctx, req.ChatID, sessionID, req.SessionGeneration, time.Now().UTC())
	if err != nil && store.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("persist conversation session: %w", err)
	}
	return nil
}

func (r *Runner) refreshConversationContext(ctx context.Context, req WorkerRequest) (WorkerRequest, error) {
	conversation, err := r.store.GetConversation(ctx, req.ChatID)
	if err != nil {
		if store.IsNotFound(err) {
			if req.SessionGeneration == 0 && strings.TrimSpace(req.ResumeSessionID) == "" {
				return req, nil
			}
			return req, fmt.Errorf("conversation context changed; please send the request again")
		}
		return req, err
	}
	if conversation.SessionGeneration != req.SessionGeneration {
		return req, fmt.Errorf("conversation context changed; please send the request again")
	}
	currentSessionID := strings.TrimSpace(conversation.ClaudeSessionID)
	if strings.TrimSpace(req.ResumeSessionID) != currentSessionID {
		return req, fmt.Errorf("conversation context changed; please send the request again")
	}
	return req, nil
}

func ReconcileOrphanedWorkers(ctx context.Context, st *store.Store) error {
	sessions, err := st.ListActiveWorkerSessions(ctx)
	if err != nil {
		return err
	}
	for _, session := range sessions {
		if session.PID != nil {
			_ = claudecode.KillProcess(*session.PID)
		}
	}
	if err := st.MarkWorkerSessionsInterrupted(ctx); err != nil {
		return err
	}
	return st.InterruptInProgressJobs(ctx)
}

func summarizeResult(result claudecode.Result) string {
	if trimmed := trimForSummary(result.Text); trimmed != "" {
		return trimmed
	}
	trimmedOut := trimForSummary(result.Stdout)
	trimmedErr := trimForSummary(result.Stderr)
	switch {
	case trimmedOut != "" && trimmedErr != "":
		return fmt.Sprintf("stdout: %s\nstderr: %s", trimmedOut, trimmedErr)
	case trimmedOut != "":
		return trimmedOut
	case trimmedErr != "":
		return trimmedErr
	default:
		return "job completed with no output"
	}
}

func trimForSummary(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 800 {
		return value[:800]
	}
	return value
}
