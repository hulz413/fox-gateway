package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"fox-gateway/internal/approval"
	"fox-gateway/internal/config"
	"fox-gateway/internal/domain"
	"fox-gateway/internal/jobs"
	"fox-gateway/internal/larkutil"
	"fox-gateway/internal/registry"
	"fox-gateway/internal/store"
	"fox-gateway/internal/worker/claudecode"
)

type Messenger interface {
	SendText(context.Context, string, string) error
	SendApprovalCard(context.Context, string, string, string, string) error
	SendOneSecondAck(context.Context, string) error
}

type Service struct {
	cfg       config.Config
	store     *store.Store
	registry  *registry.Registry
	messenger Messenger
	jobRunner *jobs.Runner
	chatLock  *jobs.KeyedLock
}

func NewService(cfg config.Config, st *store.Store, reg *registry.Registry, messenger Messenger) *Service {
	svc := &Service{cfg: cfg, store: st, registry: reg, messenger: messenger, chatLock: jobs.NewKeyedLock()}
	svc.jobRunner = jobs.NewRunner(st, claudecode.New(), cfg.MaxReadOnlyWorkers, svc.onJobUpdate)
	return svc
}

func (s *Service) Runner() *jobs.Runner {
	return s.jobRunner
}

func (s *Service) HandleLarkEvent(ctx context.Context, event domain.LarkMessageEvent) error {
	if s.messenger != nil {
		_ = s.messenger.SendOneSecondAck(ctx, event.MessageID)
	}
	s.chatLock.Acquire(event.ChatID)
	defer s.chatLock.Release(event.ChatID)
	if handled, err := s.handleRegistration(ctx, event); handled || err != nil {
		return err
	}

	now := time.Now().UTC()
	conversationContext, err := s.currentConversation(ctx, event.ChatID)
	if err != nil {
		return err
	}
	conversation := conversationContext.conversation
	trimmed := strings.TrimSpace(event.Text)
	if isConversationResetCommand(trimmed) {
		if conversation.ChatID != "" {
			if err := s.store.ClearConversationSession(ctx, event.ChatID, now); err != nil && !store.IsNotFound(err) {
				return err
			}
			conversationContext, err = s.currentConversation(ctx, event.ChatID)
			if err != nil {
				return err
			}
			conversation = conversationContext.conversation
		} else {
			conversation.ChatID = event.ChatID
			conversation.SessionGeneration++
		}
		conversation = applyConversationEvent(conversation, event, conversationResetIntent)
		conversation.ClaudeSessionID = ""
		conversation.UpdatedAt = now
		if err := s.store.UpsertConversation(ctx, conversation); err != nil {
			return err
		}
		conversationContext.conversation = conversation
		conversationContext.sessionID = ""
		if s.messenger != nil {
			return s.messenger.SendText(ctx, event.ChatID, "上下文已清理，下条消息会开启新的会话。")
		}
		return nil
	}

	classification := ClassifyRequest(event.Text)
	conversation = applyConversationEvent(conversation, event, classification.Intent)
	conversation.ClaudeSessionID = conversationContext.sessionID
	conversation.UpdatedAt = now
	if err := s.store.UpsertConversation(ctx, conversation); err != nil {
		return err
	}
	conversationContext.conversation = conversation

	if classification.Intent == "status_query" {
		return s.replyStatus(ctx, event.ChatID)
	}
	return s.createAndRunJob(ctx, event, classification, conversationContext)
}

func (s *Service) HandleLarkAction(ctx context.Context, action larkutil.ActionRequest) error {
	job, err := s.store.GetJob(ctx, action.JobID)
	if err != nil {
		return err
	}
	s.chatLock.Acquire(job.ChatID)
	defer s.chatLock.Release(job.ChatID)
	job, err = s.store.GetJob(ctx, action.JobID)
	if err != nil {
		return err
	}
	approvalRecord, err := s.store.GetApproval(ctx, action.JobID)
	if err != nil {
		return err
	}
	if approvalRecord.Status != domain.ApprovalStatusPending {
		return nil
	}
	if s.registry == nil || !s.registry.IsApprover(action.ApproverOpenID) {
		return fmt.Errorf("approver is not registered")
	}

	payload, err := approval.ParsePayload(approvalRecord.PayloadJSON)
	if err != nil {
		return err
	}
	conversationContext, err := s.currentConversation(ctx, job.ChatID)
	if err != nil {
		return err
	}
	conversation := conversationContext.conversation
	if payload.ConversationGeneration != conversation.SessionGeneration || strings.TrimSpace(payload.ConversationSessionID) != conversationContext.sessionID || payload.ConversationMessageID != job.MessageID {
		approvalRecord.Status = domain.ApprovalStatusInvalidated
		approvalRecord.ApproverOpenID = action.ApproverOpenID
		approvalRecord.DecisionReason = "conversation context changed before approval"
		approvalRecord.UpdatedAt = time.Now().UTC()
		job.Status = domain.JobStatusRejected
		job.ErrorText = "conversation context changed before approval"
		job.UpdatedAt = time.Now().UTC()
		if err := s.store.SaveApproval(ctx, approvalRecord); err != nil {
			return err
		}
		if err := s.store.UpdateJob(ctx, job); err != nil {
			return err
		}
		return s.messenger.SendText(ctx, job.ChatID, "会话上下文已变化，请重新发送请求后再审批。")
	}
	payload.BaseRepoState = currentRepoState(s.cfg.WorkspaceRoot)
	if !approval.ValidateHash(payload, approvalRecord.Hash) {
		approvalRecord.Status = domain.ApprovalStatusInvalidated
		approvalRecord.ApproverOpenID = action.ApproverOpenID
		approvalRecord.DecisionReason = "approval payload drifted before execution"
		approvalRecord.UpdatedAt = time.Now().UTC()
		job.Status = domain.JobStatusRejected
		job.ErrorText = "approval invalidated because workspace state drifted"
		job.UpdatedAt = time.Now().UTC()
		if err := s.store.SaveApproval(ctx, approvalRecord); err != nil {
			return err
		}
		if err := s.store.UpdateJob(ctx, job); err != nil {
			return err
		}
		return s.messenger.SendText(ctx, job.ChatID, "Pairing or approval context changed. Please send the request again.")
	}

	approvalRecord.ApproverOpenID = action.ApproverOpenID
	approvalRecord.UpdatedAt = time.Now().UTC()
	if strings.EqualFold(action.Decision, "approve") {
		approvalRecord.Status = domain.ApprovalStatusApproved
		job.Status = domain.JobStatusApproved
	} else {
		approvalRecord.Status = domain.ApprovalStatusRejected
		job.Status = domain.JobStatusRejected
	}
	if err := s.store.SaveApproval(ctx, approvalRecord); err != nil {
		return err
	}
	job.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateJob(ctx, job); err != nil {
		return err
	}
	if approvalRecord.Status == domain.ApprovalStatusApproved {
		s.jobRunner.Enqueue(jobs.WorkerRequest{
			JobID:             job.ID,
			ChatID:            job.ChatID,
			Prompt:            job.RequestText,
			WorkspaceRoot:     s.cfg.WorkspaceRoot,
			ClaudePath:        s.cfg.ClaudePath,
			Mutating:          true,
			ResumeSessionID:   conversationContext.sessionID,
			SessionGeneration: conversationContext.conversation.SessionGeneration,
		})
		return nil
	}
	return nil
}

func (s *Service) createAndRunJob(ctx context.Context, event domain.LarkMessageEvent, classification Classification, conversationContext conversationContext) error {
	now := time.Now().UTC()
	job := domain.Job{
		ID:               domain.NewID("job"),
		ChatID:           event.ChatID,
		MessageID:        event.MessageID,
		RequesterOpenID:  event.SenderOpenID,
		Kind:             domain.JobKind(classification.Kind),
		Status:           domain.JobStatusQueued,
		RequestText:      event.Text,
		RequiresApproval: classification.NeedsApproval,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if classification.NeedsApproval {
		payload := approval.Payload{
			WorkspaceID:            s.cfg.WorkspaceRoot,
			BaseRepoState:          currentRepoState(s.cfg.WorkspaceRoot),
			ConversationSessionID:  conversationContext.sessionID,
			ConversationGeneration: conversationContext.conversation.SessionGeneration,
			ConversationMessageID:  event.MessageID,
			IntentCategory:         classification.Intent,
			AllowedActions:         classification.AllowedActions,
			AllowedPaths:           []string{s.cfg.WorkspaceRoot},
			BlockedPathClasses:     []string{"secrets", "env", "deploy"},
			RuntimeLimitSec:        900,
			Async:                  true,
			Nonce:                  job.ID,
		}
		hash, err := approval.HashPayload(payload)
		if err != nil {
			return err
		}
		payloadJSON, _ := json.Marshal(payload)
		job.Status = domain.JobStatusWaitingApproval
		job.ApprovalHash = hash
		if err := s.store.CreateJob(ctx, job); err != nil {
			return err
		}
		if err := s.store.SaveApproval(ctx, domain.Approval{
			JobID:       job.ID,
			PayloadJSON: string(payloadJSON),
			Hash:        hash,
			Status:      domain.ApprovalStatusPending,
			RequestedBy: event.SenderOpenID,
			CreatedAt:   now,
			UpdatedAt:   now,
		}); err != nil {
			return err
		}
		return s.messenger.SendApprovalCard(ctx, event.ChatID, job.ID, hash, event.Text)
	}
	if err := s.store.CreateJob(ctx, job); err != nil {
		return err
	}
	s.jobRunner.Enqueue(jobs.WorkerRequest{
		JobID:             job.ID,
		ChatID:            job.ChatID,
		Prompt:            job.RequestText,
		WorkspaceRoot:     s.cfg.WorkspaceRoot,
		ClaudePath:        s.cfg.ClaudePath,
		Mutating:          false,
		ResumeSessionID:   conversationContext.sessionID,
		SessionGeneration: conversationContext.conversation.SessionGeneration,
	})
	return nil
}

func (s *Service) replyStatus(ctx context.Context, chatID string) error {
	job, err := s.store.FindLatestJobByChat(ctx, chatID)
	if err != nil {
		if store.IsNotFound(err) {
			return s.messenger.SendText(ctx, chatID, "还没有任务记录。")
		}
		return err
	}
	if strings.TrimSpace(job.ResultSummary) != "" {
		return s.messenger.SendText(ctx, chatID, job.ResultSummary)
	}
	if strings.TrimSpace(job.ErrorText) != "" {
		return s.messenger.SendText(ctx, chatID, "执行失败："+job.ErrorText)
	}
	return s.messenger.SendText(ctx, chatID, "任务仍在处理中，请稍后再问我一次。")
}

func (s *Service) onJobUpdate(ctx context.Context, job domain.Job) error {
	if job.Status == domain.JobStatusSucceeded && strings.TrimSpace(job.ResultSummary) != "" {
		return s.messenger.SendText(ctx, job.ChatID, job.ResultSummary)
	}
	if job.Status == domain.JobStatusFailed && strings.TrimSpace(job.ErrorText) != "" {
		return s.messenger.SendText(ctx, job.ChatID, "执行失败："+job.ErrorText)
	}
	return nil
}

func (s *Service) handleRegistration(ctx context.Context, event domain.LarkMessageEvent) (bool, error) {
	if s.registry == nil {
		return false, nil
	}
	key, ok := registry.ParseRegistrationMessage(event.Text)
	if !ok {
		return false, nil
	}
	registered, err := s.registry.RegisterWithBootstrap(event.SenderOpenID, event.ChatID, key)
	if err != nil {
		return true, s.messenger.SendText(ctx, event.ChatID, fmt.Sprintf("Fox Gateway pairing failed: %v", err))
	}
	if !registered {
		return true, s.messenger.SendText(ctx, event.ChatID, "Fox Gateway already paired with this approver.")
	}
	return true, s.messenger.SendText(ctx, event.ChatID, "Fox Gateway pairing success :)")
}

func currentRepoState(workspaceRoot string) string {
	cmd := exec.Command("git", "-C", workspaceRoot, "rev-parse", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "nogit"
	}
	return strings.TrimSpace(string(output))
}
