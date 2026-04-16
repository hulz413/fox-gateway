package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"fox-gateway/internal/domain"
	"fox-gateway/internal/registry"
)

func TestOpenMigratesConversationSessionColumn(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fox-gateway.db")
	legacyDB, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("sql.Open error = %v", err)
	}
	defer legacyDB.Close()
	if _, err := legacyDB.Exec(`CREATE TABLE conversations (
		chat_id TEXT PRIMARY KEY,
		last_message_id TEXT NOT NULL,
		last_sender_open_id TEXT NOT NULL,
		last_message_text TEXT NOT NULL,
		last_intent TEXT NOT NULL,
		updated_at TIMESTAMP NOT NULL
	);`); err != nil {
		t.Fatalf("create legacy conversations table error = %v", err)
	}
	if _, err := legacyDB.Exec(`INSERT INTO conversations(chat_id,last_message_id,last_sender_open_id,last_message_text,last_intent,updated_at) VALUES('chat_1','msg_1','ou_1','hello','question',CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("insert legacy conversation error = %v", err)
	}
	_ = legacyDB.Close()

	st, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("store.Open error = %v", err)
	}
	defer st.Close()

	conversation, err := st.GetConversation(ctx, "chat_1")
	if err != nil {
		t.Fatalf("GetConversation error = %v", err)
	}
	if conversation.ClaudeSessionID != "" {
		t.Fatalf("ClaudeSessionID = %q, want empty string", conversation.ClaudeSessionID)
	}
	if conversation.SessionGeneration != 0 {
		t.Fatalf("SessionGeneration = %d, want 0", conversation.SessionGeneration)
	}
}

func TestBootstrapMessageAndRegisterFirstUser(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fox-gateway.db"))
	if err != nil {
		t.Fatalf("store.Open error = %v", err)
	}
	defer st.Close()

	message, ok, err := st.BootstrapMessage(ctx)
	if err != nil {
		t.Fatalf("BootstrapMessage error = %v", err)
	}
	if !ok || !strings.HasPrefix(message, registry.RegisterCommandPrefix+" ") {
		t.Fatalf("unexpected bootstrap message: %q, %v", message, ok)
	}
	key, ok := registry.ParseRegistrationMessage(message)
	if !ok {
		t.Fatalf("ParseRegistrationMessage(%q) failed", message)
	}

	registered, err := st.RegisterFirstUserWithBootstrap(ctx, "ou_1", "chat_1", key)
	if err != nil {
		t.Fatalf("RegisterFirstUserWithBootstrap error = %v", err)
	}
	if !registered {
		t.Fatal("expected first registration to succeed")
	}
	if hasUser, err := st.HasRegisteredUser(ctx, "ou_1"); err != nil {
		t.Fatalf("HasRegisteredUser error = %v", err)
	} else if !hasUser {
		t.Fatal("expected registered user to be active")
	}
	if message, ok, err := st.BootstrapMessage(ctx); err != nil {
		t.Fatalf("BootstrapMessage after registration error = %v", err)
	} else if ok || message != "" {
		t.Fatalf("expected bootstrap message to be unavailable after first registration, got %q, %v", message, ok)
	}
}

func TestRegisterFirstUserWithBootstrapRejectsWrongKey(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fox-gateway.db"))
	if err != nil {
		t.Fatalf("store.Open error = %v", err)
	}
	defer st.Close()

	if _, _, err := st.BootstrapMessage(ctx); err != nil {
		t.Fatalf("BootstrapMessage error = %v", err)
	}
	if _, err := st.RegisterFirstUserWithBootstrap(ctx, "ou_1", "chat_1", "wrong"); err == nil {
		t.Fatal("expected invalid registration key to fail")
	}
}

func TestUpdateAndClearConversationSession(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fox-gateway.db"))
	if err != nil {
		t.Fatalf("store.Open error = %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	if err := st.UpsertConversation(ctx, domain.Conversation{
		ChatID:           "chat_1",
		LastMessageID:    "msg_1",
		LastSenderOpenID: "ou_1",
		LastMessageText:  "hello",
		LastIntent:       "question",
		UpdatedAt:        now,
	}); err != nil {
		t.Fatalf("UpsertConversation error = %v", err)
	}
	if err := st.UpdateConversationSession(ctx, "chat_1", "session-1", 0, now.Add(time.Second)); err != nil {
		t.Fatalf("UpdateConversationSession error = %v", err)
	}
	conversation, err := st.GetConversation(ctx, "chat_1")
	if err != nil {
		t.Fatalf("GetConversation after update error = %v", err)
	}
	if conversation.ClaudeSessionID != "session-1" {
		t.Fatalf("ClaudeSessionID = %q, want session-1", conversation.ClaudeSessionID)
	}
	if conversation.SessionGeneration != 0 {
		t.Fatalf("SessionGeneration after update = %d, want 0", conversation.SessionGeneration)
	}
	if err := st.ClearConversationSession(ctx, "chat_1", now.Add(2*time.Second)); err != nil {
		t.Fatalf("ClearConversationSession error = %v", err)
	}
	conversation, err = st.GetConversation(ctx, "chat_1")
	if err != nil {
		t.Fatalf("GetConversation after clear error = %v", err)
	}
	if conversation.ClaudeSessionID != "" {
		t.Fatalf("ClaudeSessionID after clear = %q, want empty string", conversation.ClaudeSessionID)
	}
	if conversation.SessionGeneration != 1 {
		t.Fatalf("SessionGeneration after clear = %d, want 1", conversation.SessionGeneration)
	}
	if err := st.UpdateConversationSession(ctx, "chat_1", "session-stale", 0, now.Add(3*time.Second)); err == nil {
		t.Fatal("expected stale generation update to fail")
	} else if !IsNotFound(err) {
		t.Fatalf("expected stale generation update to be not found, got %v", err)
	}
	conversation, err = st.GetConversation(ctx, "chat_1")
	if err != nil {
		t.Fatalf("GetConversation after stale update error = %v", err)
	}
	if conversation.ClaudeSessionID != "" {
		t.Fatalf("ClaudeSessionID after stale update = %q, want empty string", conversation.ClaudeSessionID)
	}
	if conversation.SessionGeneration != 1 {
		t.Fatalf("SessionGeneration after stale update = %d, want 1", conversation.SessionGeneration)
	}
}
