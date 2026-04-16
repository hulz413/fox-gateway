package registry

import (
	"path/filepath"
	"testing"
)

func TestSetConfigPersistsConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fox-gateway.json")
	reg, err := Open(path)
	if err != nil {
		t.Fatalf("Open error = %v", err)
	}
	if reg.HasConfig() {
		t.Fatal("expected empty registry to have no config")
	}

	cfg := RuntimeConfig{
		DBPath:                "~/.fox-gateway/data/test.db",
		LarkAppID:             "cli_test",
		LarkAppSecret:         "secret",
		LarkVerificationToken: "token",
		ClaudePath:            "claude",
		WorkspaceRoot:         "~/workspace",
		MaxReadOnlyWorkers:    2,
	}
	if err := reg.SetConfig(cfg); err != nil {
		t.Fatalf("SetConfig error = %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("Open after save error = %v", err)
	}
	if !reopened.HasConfig() {
		t.Fatal("expected saved config to validate")
	}
	if got := reopened.Config(); got != cfg {
		t.Fatalf("Config() = %+v, want %+v", got, cfg)
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
