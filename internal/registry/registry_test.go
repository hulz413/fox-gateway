package registry

import (
	"path/filepath"
	"testing"
)

func TestOpenCreatesBootstrapMessage(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "fox-gateway.json"))
	if err != nil {
		t.Fatalf("Open error = %v", err)
	}
	message, ok := r.BootstrapMessage()
	if !ok || message == "" {
		t.Fatal("expected bootstrap message")
	}
}

func TestRegisterUserWithBootstrapConsumesKey(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "fox-gateway.json"))
	if err != nil {
		t.Fatalf("Open error = %v", err)
	}
	message, ok := r.BootstrapMessage()
	if !ok {
		t.Fatal("expected bootstrap message")
	}
	key := message[len(RegisterCommandPrefix)+1:]
	registered, err := r.RegisterUserWithBootstrap("ou_1", "chat_1", key)
	if err != nil {
		t.Fatalf("RegisterUserWithBootstrap error = %v", err)
	}
	if !registered {
		t.Fatal("expected first registration to succeed")
	}
	if !r.HasUser("ou_1") {
		t.Fatal("expected user to be active")
	}
	if _, ok := r.BootstrapMessage(); ok {
		t.Fatal("bootstrap message should be unavailable after first registration")
	}
}

func TestRegisterUserWithBootstrapRejectsWrongKey(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "fox-gateway.json"))
	if err != nil {
		t.Fatalf("Open error = %v", err)
	}
	if _, err := r.RegisterUserWithBootstrap("ou_1", "chat_1", "wrong"); err == nil {
		t.Fatal("expected invalid registration key to fail")
	}
}

func TestParseRegistrationMessage(t *testing.T) {
	key, ok := ParseRegistrationMessage("fox-gateway register abc123")
	if !ok || key != "abc123" {
		t.Fatalf("ParseRegistrationMessage returned %q, %v", key, ok)
	}
	if _, ok := ParseRegistrationMessage("register abc123"); ok {
		t.Fatal("expected invalid message to fail")
	}
}
