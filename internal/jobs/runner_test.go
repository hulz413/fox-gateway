package jobs

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"fox-gateway/internal/domain"
	"fox-gateway/internal/store"
	"fox-gateway/internal/worker/claudecode"
)

func TestPersistConversationSessionIgnoresStaleGeneration(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fox-gateway.db"))
	if err != nil {
		t.Fatalf("store.Open error = %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	if err := st.UpsertConversation(ctx, domain.Conversation{
		ChatID:            "chat_1",
		LastMessageID:     "msg_1",
		LastSenderOpenID:  "ou_1",
		LastMessageText:   "hello",
		LastIntent:        "question",
		SessionGeneration: 0,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("UpsertConversation error = %v", err)
	}
	if err := st.ClearConversationSession(ctx, "chat_1", now.Add(time.Second)); err != nil {
		t.Fatalf("ClearConversationSession error = %v", err)
	}

	r := &Runner{store: st}
	err = r.persistConversationSession(ctx, WorkerRequest{
		ChatID:            "chat_1",
		ResumeSessionID:   "session-old",
		SessionGeneration: 0,
	}, claudecode.Result{})
	if err != nil {
		t.Fatalf("persistConversationSession error = %v", err)
	}

	conversation, err := st.GetConversation(ctx, "chat_1")
	if err != nil {
		t.Fatalf("GetConversation error = %v", err)
	}
	if conversation.ClaudeSessionID != "" {
		t.Fatalf("ClaudeSessionID = %q, want empty", conversation.ClaudeSessionID)
	}
	if conversation.SessionGeneration != 1 {
		t.Fatalf("SessionGeneration = %d, want 1", conversation.SessionGeneration)
	}
}

func TestSummarizeFailureHidesExitStatusNoise(t *testing.T) {
	summary := summarizeFailure(errors.New("exit status 1"), claudecode.Result{})
	if summary != "command failed" {
		t.Fatalf("summarizeFailure() = %q, want command failed", summary)
	}
}

func TestSummarizeFailurePrefersCleanStderr(t *testing.T) {
	summary := summarizeFailure(errors.New("exit status 1"), claudecode.Result{Stderr: "exit status 1\nreal failure"})
	if summary != "real failure" {
		t.Fatalf("summarizeFailure() = %q, want real failure", summary)
	}
}

func TestRefreshConversationContextRejectsChangedConversation(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fox-gateway.db"))
	if err != nil {
		t.Fatalf("store.Open error = %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	if err := st.UpsertConversation(ctx, domain.Conversation{
		ChatID:            "chat_1",
		LastMessageID:     "msg_1",
		LastSenderOpenID:  "ou_1",
		LastMessageText:   "hello",
		LastIntent:        "question",
		ClaudeSessionID:   "session-1",
		SessionGeneration: 1,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("UpsertConversation error = %v", err)
	}

	r := &Runner{store: st}
	_, err = r.refreshConversationContext(ctx, WorkerRequest{
		ChatID:            "chat_1",
		ResumeSessionID:   "session-1",
		SessionGeneration: 0,
	})
	if err == nil {
		t.Fatal("expected refreshConversationContext to reject changed generation")
	}

	_, err = r.refreshConversationContext(ctx, WorkerRequest{
		ChatID:            "chat_1",
		ResumeSessionID:   "session-old",
		SessionGeneration: 1,
	})
	if err == nil {
		t.Fatal("expected refreshConversationContext to reject changed session id")
	}
}
