package orchestrator

import (
	"context"
	"path/filepath"
	"testing"

	"fox-gateway/internal/config"
	"fox-gateway/internal/domain"
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
