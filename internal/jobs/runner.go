package jobs

import (
	"context"
	"fmt"
	"sync"
	"time"

	"fox-gateway/internal/domain"
	"fox-gateway/internal/store"
	"fox-gateway/internal/worker/claudecode"
)

type WorkerRequest struct {
	JobID         string
	ChatID        string
	Prompt        string
	WorkspaceRoot string
	ClaudePath    string
	Mutating      bool
}

type Callback func(context.Context, domain.Job) error

type Runner struct {
	store          *store.Store
	worker         *claudecode.Runner
	workspaceLock  *WorkspaceLock
	readonlyTokens chan struct{}
	onUpdate       Callback
	wg             sync.WaitGroup
}

func NewRunner(st *store.Store, worker *claudecode.Runner, maxReadOnly int, onUpdate Callback) *Runner {
	return &Runner{
		store:          st,
		worker:         worker,
		workspaceLock:  NewWorkspaceLock(),
		readonlyTokens: make(chan struct{}, maxReadOnly),
		onUpdate:       onUpdate,
	}
}

func (r *Runner) Enqueue(req WorkerRequest) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		ctx := context.Background()
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

	runCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	result, runErr := r.worker.Run(runCtx, claudecode.Request{
		JobID:         req.JobID,
		ClaudePath:    req.ClaudePath,
		WorkspaceRoot: req.WorkspaceRoot,
		Prompt:        req.Prompt,
		Mutating:      req.Mutating,
		Async:         true,
	})
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
		job.ResultSummary = summarizeOutput(result.Stdout, result.Stderr)
	} else {
		job.Status = domain.JobStatusSucceeded
		job.ResultSummary = summarizeOutput(result.Stdout, result.Stderr)
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

func summarizeOutput(stdout, stderr string) string {
	trimmedOut := trimForSummary(stdout)
	trimmedErr := trimForSummary(stderr)
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
	if len(value) > 800 {
		return value[:800]
	}
	return value
}
