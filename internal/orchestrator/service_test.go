package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fox-gateway/internal/approval"
	"fox-gateway/internal/config"
	"fox-gateway/internal/domain"
	"fox-gateway/internal/larkutil"
	"fox-gateway/internal/registry"
	"fox-gateway/internal/store"
)

type fakeMessenger struct {
	texts []string
	cards []struct {
		chatID  string
		jobID   string
		hash    string
		summary string
	}
	decisionCards []approval.DecisionCard
}

func (f *fakeMessenger) SendText(_ context.Context, _ string, text string) error {
	f.texts = append(f.texts, text)
	return nil
}

func (f *fakeMessenger) SendApprovalCard(_ context.Context, chatID, jobID, hash, summary string) error {
	f.cards = append(f.cards, struct {
		chatID  string
		jobID   string
		hash    string
		summary string
	}{chatID: chatID, jobID: jobID, hash: hash, summary: summary})
	return nil
}

func (f *fakeMessenger) SendDecisionCard(_ context.Context, _ string, card approval.DecisionCard) error {
	f.decisionCards = append(f.decisionCards, card)
	return nil
}

func (f *fakeMessenger) SendOneSecondAck(_ context.Context, _ string) error {
	return nil
}

func TestHandleLarkEventSendsApprovalCardForMutation(t *testing.T) {
	tempDir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(tempDir, "fox-gateway.db"))
	if err != nil {
		t.Fatalf("store.Open error = %v", err)
	}
	defer st.Close()

	messenger := &fakeMessenger{}
	reg, err := registry.Open(filepath.Join(tempDir, "fox-gateway.json"))
	if err != nil {
		t.Fatalf("registry.Open error = %v", err)
	}

	svc := NewService(config.Config{
		WorkspaceRoot:      tempDir,
		ClaudePath:         "claude",
		MaxReadOnlyWorkers: 1,
	}, st, reg, messenger)

	err = svc.HandleLarkEvent(context.Background(), domain.LarkMessageEvent{
		SenderOpenID: "ou_requester",
		ChatID:       "chat_1",
		Text:         "please modify the handler and patch the bug",
		MessageID:    "msg_1",
	})
	if err != nil {
		t.Fatalf("HandleLarkEvent error = %v", err)
	}
	if len(messenger.cards) != 1 {
		t.Fatalf("expected 1 approval card, got %d", len(messenger.cards))
	}
	if len(messenger.texts) != 0 {
		t.Fatalf("expected no plain text approval prompt, got %d text messages", len(messenger.texts))
	}

	job, err := st.FindLatestJobByChat(context.Background(), "chat_1")
	if err != nil {
		t.Fatalf("FindLatestJobByChat error = %v", err)
	}
	if job.Status != domain.JobStatusWaitingApproval {
		t.Fatalf("job status = %q, want %q", job.Status, domain.JobStatusWaitingApproval)
	}
	if messenger.cards[0].jobID != job.ID {
		t.Fatalf("card job id = %q, want %q", messenger.cards[0].jobID, job.ID)
	}
}

func TestHandleLarkEventRegistersApproverWithBootstrap(t *testing.T) {
	tempDir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(tempDir, "fox-gateway.db"))
	if err != nil {
		t.Fatalf("store.Open error = %v", err)
	}
	defer st.Close()

	messenger := &fakeMessenger{}
	reg, err := registry.Open(filepath.Join(tempDir, "fox-gateway.json"))
	if err != nil {
		t.Fatalf("registry.Open error = %v", err)
	}
	message, ok := reg.BootstrapMessage()
	if !ok {
		t.Fatal("expected bootstrap message")
	}

	svc := NewService(config.Config{
		WorkspaceRoot:      tempDir,
		ClaudePath:         "claude",
		MaxReadOnlyWorkers: 1,
	}, st, reg, messenger)

	err = svc.HandleLarkEvent(context.Background(), domain.LarkMessageEvent{
		SenderOpenID: "ou_first",
		ChatID:       "chat_1",
		Text:         message,
		MessageID:    "msg_1",
	})
	if err != nil {
		t.Fatalf("HandleLarkEvent error = %v", err)
	}
	if !reg.IsApprover("ou_first") {
		t.Fatal("expected first user to be registered")
	}
	if len(messenger.texts) == 0 || messenger.texts[len(messenger.texts)-1] != "Fox Gateway pairing success :)" {
		t.Fatalf("unexpected registration message: %+v", messenger.texts)
	}
}

func TestHandleLarkEventReusesClaudeSessionByChat(t *testing.T) {
	tempDir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(tempDir, "fox-gateway.db"))
	if err != nil {
		t.Fatalf("store.Open error = %v", err)
	}
	defer st.Close()

	claudePath := writeFakeClaudeScript(t, `resume=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--resume" ]; then
    resume="$2"
    shift 2
    continue
  fi
  shift
done
if [ -n "$resume" ]; then
  printf '{"result":"resumed:%s","session_id":"%s"}\n' "$resume" "$resume"
else
  printf '{"result":"fresh","session_id":"session-1"}\n'
fi
`)
	messenger := &fakeMessenger{}
	svc := NewService(config.Config{
		WorkspaceRoot:      tempDir,
		ClaudePath:         claudePath,
		MaxReadOnlyWorkers: 1,
	}, st, nil, messenger)

	ctx := context.Background()
	if err := svc.HandleLarkEvent(ctx, domain.LarkMessageEvent{
		SenderOpenID: "ou_requester",
		ChatID:       "chat_1",
		Text:         "read this repo",
		MessageID:    "msg_1",
	}); err != nil {
		t.Fatalf("first HandleLarkEvent error = %v", err)
	}
	svc.Runner().Wait()

	conversation, err := st.GetConversation(ctx, "chat_1")
	if err != nil {
		t.Fatalf("GetConversation error = %v", err)
	}
	if conversation.ClaudeSessionID != "session-1" {
		t.Fatalf("ClaudeSessionID = %q, want session-1", conversation.ClaudeSessionID)
	}
	if got := messenger.texts[len(messenger.texts)-1]; got != "fresh" {
		t.Fatalf("first reply = %q, want fresh", got)
	}

	if err := svc.HandleLarkEvent(ctx, domain.LarkMessageEvent{
		SenderOpenID: "ou_requester",
		ChatID:       "chat_1",
		Text:         "read more details",
		MessageID:    "msg_2",
	}); err != nil {
		t.Fatalf("second HandleLarkEvent error = %v", err)
	}
	svc.Runner().Wait()

	conversation, err = st.GetConversation(ctx, "chat_1")
	if err != nil {
		t.Fatalf("GetConversation after resume error = %v", err)
	}
	if conversation.ClaudeSessionID != "session-1" {
		t.Fatalf("ClaudeSessionID after resume = %q, want session-1", conversation.ClaudeSessionID)
	}
	if got := messenger.texts[len(messenger.texts)-1]; got != "resumed:session-1" {
		t.Fatalf("second reply = %q, want resumed:session-1", got)
	}
}

func TestHandleLarkEventResetCommandsClearConversationSession(t *testing.T) {
	commands := []string{"/clear", "/new"}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			tempDir := t.TempDir()
			st, err := store.Open(context.Background(), filepath.Join(tempDir, "fox-gateway.db"))
			if err != nil {
				t.Fatalf("store.Open error = %v", err)
			}
			defer st.Close()

			now := time.Now().UTC()
			if err := st.UpsertConversation(context.Background(), domain.Conversation{
				ChatID:           "chat_1",
				LastMessageID:    "seed_msg",
				LastSenderOpenID: "ou_seed",
				LastMessageText:  "seed",
				LastIntent:       "read_only",
				ClaudeSessionID:  "session-1",
				UpdatedAt:        now,
			}); err != nil {
				t.Fatalf("UpsertConversation error = %v", err)
			}

			messenger := &fakeMessenger{}
			svc := NewService(config.Config{
				WorkspaceRoot:      tempDir,
				ClaudePath:         "claude",
				MaxReadOnlyWorkers: 1,
			}, st, nil, messenger)

			if err := svc.HandleLarkEvent(context.Background(), domain.LarkMessageEvent{
				SenderOpenID: "ou_requester",
				ChatID:       "chat_1",
				Text:         command,
				MessageID:    "msg_1",
			}); err != nil {
				t.Fatalf("HandleLarkEvent error = %v", err)
			}

			conversation, err := st.GetConversation(context.Background(), "chat_1")
			if err != nil {
				t.Fatalf("GetConversation error = %v", err)
			}
			if conversation.ClaudeSessionID != "" {
				t.Fatalf("ClaudeSessionID = %q, want empty", conversation.ClaudeSessionID)
			}
			if conversation.SessionGeneration != 1 {
				t.Fatalf("SessionGeneration = %d, want 1", conversation.SessionGeneration)
			}
			if conversation.LastIntent != "conversation_reset" {
				t.Fatalf("LastIntent = %q, want conversation_reset", conversation.LastIntent)
			}
			if got := messenger.texts[len(messenger.texts)-1]; got != "上下文已清理，下条消息会开启新的会话。" {
				t.Fatalf("reset reply = %q", got)
			}
		})
	}
}

func TestHandleLarkActionApprovedMutationUsesConversationSession(t *testing.T) {
	tempDir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(tempDir, "fox-gateway.db"))
	if err != nil {
		t.Fatalf("store.Open error = %v", err)
	}
	defer st.Close()

	claudePath := writeFakeClaudeScript(t, `resume=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--resume" ]; then
    resume="$2"
    shift 2
    continue
  fi
  shift
done
if [ -n "$resume" ]; then
  printf '{"result":"resumed:%s","session_id":"%s"}\n' "$resume" "$resume"
else
  printf '{"result":"fresh","session_id":"session-2"}\n'
fi
`)
	messenger := &fakeMessenger{}
	reg, err := registry.Open(filepath.Join(tempDir, "fox-gateway.json"))
	if err != nil {
		t.Fatalf("registry.Open error = %v", err)
	}
	message, ok := reg.BootstrapMessage()
	if !ok {
		t.Fatal("expected bootstrap message")
	}
	key, ok := registry.ParseRegistrationMessage(message)
	if !ok {
		t.Fatalf("ParseRegistrationMessage(%q) failed", message)
	}
	registered, err := reg.RegisterWithBootstrap("ou_approver", "chat_1", key)
	if err != nil {
		t.Fatalf("RegisterWithBootstrap error = %v", err)
	}
	if !registered {
		t.Fatal("expected approver registration")
	}

	now := time.Now().UTC()
	if err := st.UpsertConversation(context.Background(), domain.Conversation{
		ChatID:           "chat_1",
		LastMessageID:    "seed_msg",
		LastSenderOpenID: "ou_requester",
		LastMessageText:  "seed",
		LastIntent:       "read_only",
		ClaudeSessionID:  "session-1",
		UpdatedAt:        now,
	}); err != nil {
		t.Fatalf("UpsertConversation error = %v", err)
	}

	svc := NewService(config.Config{
		WorkspaceRoot:      tempDir,
		ClaudePath:         claudePath,
		MaxReadOnlyWorkers: 1,
	}, st, reg, messenger)

	ctx := context.Background()
	if err := svc.HandleLarkEvent(ctx, domain.LarkMessageEvent{
		SenderOpenID: "ou_requester",
		ChatID:       "chat_1",
		Text:         "please modify the handler and fix bug",
		MessageID:    "msg_1",
	}); err != nil {
		t.Fatalf("HandleLarkEvent mutation error = %v", err)
	}

	job, err := st.FindLatestJobByChat(ctx, "chat_1")
	if err != nil {
		t.Fatalf("FindLatestJobByChat error = %v", err)
	}
	if err := svc.HandleLarkAction(ctx, larkutil.ActionRequest{
		JobID:       job.ID,
		RequestKind: approval.KindApproval,
		ChoiceID:    "approve",
		ActorOpenID: "ou_approver",
	}); err != nil {
		t.Fatalf("HandleLarkAction error = %v", err)
	}
	svc.Runner().Wait()

	if got := messenger.texts[len(messenger.texts)-1]; got != "resumed:session-1" {
		t.Fatalf("approved mutation reply = %q, want resumed:session-1", got)
	}
}

func TestHandleLarkActionRejectsStaleConversationContext(t *testing.T) {
	tempDir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(tempDir, "fox-gateway.db"))
	if err != nil {
		t.Fatalf("store.Open error = %v", err)
	}
	defer st.Close()

	claudePath := writeFakeClaudeScript(t, `printf '{"result":"should-not-run","session_id":"session-2"}\n'`)
	messenger := &fakeMessenger{}
	reg, err := registry.Open(filepath.Join(tempDir, "fox-gateway.json"))
	if err != nil {
		t.Fatalf("registry.Open error = %v", err)
	}
	message, ok := reg.BootstrapMessage()
	if !ok {
		t.Fatal("expected bootstrap message")
	}
	key, ok := registry.ParseRegistrationMessage(message)
	if !ok {
		t.Fatalf("ParseRegistrationMessage(%q) failed", message)
	}
	registered, err := reg.RegisterWithBootstrap("ou_approver", "chat_1", key)
	if err != nil {
		t.Fatalf("RegisterWithBootstrap error = %v", err)
	}
	if !registered {
		t.Fatal("expected approver registration")
	}

	now := time.Now().UTC()
	if err := st.UpsertConversation(context.Background(), domain.Conversation{
		ChatID:            "chat_1",
		LastMessageID:     "seed_msg",
		LastSenderOpenID:  "ou_requester",
		LastMessageText:   "seed",
		LastIntent:        "read_only",
		ClaudeSessionID:   "session-1",
		SessionGeneration: 0,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("UpsertConversation error = %v", err)
	}

	svc := NewService(config.Config{
		WorkspaceRoot:      tempDir,
		ClaudePath:         claudePath,
		MaxReadOnlyWorkers: 1,
	}, st, reg, messenger)

	ctx := context.Background()
	if err := svc.HandleLarkEvent(ctx, domain.LarkMessageEvent{
		SenderOpenID: "ou_requester",
		ChatID:       "chat_1",
		Text:         "please modify the handler and fix bug",
		MessageID:    "msg_1",
	}); err != nil {
		t.Fatalf("HandleLarkEvent mutation error = %v", err)
	}
	if err := svc.HandleLarkEvent(ctx, domain.LarkMessageEvent{
		SenderOpenID: "ou_requester",
		ChatID:       "chat_1",
		Text:         "/clear",
		MessageID:    "msg_2",
	}); err != nil {
		t.Fatalf("HandleLarkEvent reset error = %v", err)
	}

	job, err := st.FindLatestJobByChat(ctx, "chat_1")
	if err != nil {
		t.Fatalf("FindLatestJobByChat error = %v", err)
	}
	if err := svc.HandleLarkAction(ctx, larkutil.ActionRequest{
		JobID:       job.ID,
		RequestKind: approval.KindApproval,
		ChoiceID:    "approve",
		ActorOpenID: "ou_approver",
	}); err != nil {
		t.Fatalf("HandleLarkAction error = %v", err)
	}
	svc.Runner().Wait()

	updatedJob, err := st.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob error = %v", err)
	}
	if updatedJob.Status != domain.JobStatusRejected {
		t.Fatalf("job status = %q, want %q", updatedJob.Status, domain.JobStatusRejected)
	}
	if updatedJob.ErrorText != "conversation context changed before approval" {
		t.Fatalf("job error = %q", updatedJob.ErrorText)
	}
	if got := messenger.texts[len(messenger.texts)-1]; got != "会话上下文已变化，请重新发送请求后再审批。" {
		t.Fatalf("stale approval reply = %q", got)
	}
}

func writeFakeClaudeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-claude")
	content := "#!/bin/sh\nset -eu\n" + body
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile fake claude script error = %v", err)
	}
	return path
}
