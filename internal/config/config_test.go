package config

import (
	"path/filepath"
	"testing"

	"fox-gateway/internal/registry"
)

func TestLoadExpandsHomePaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fox-gateway.json")
	t.Setenv("FOX_GATEWAY_CONFIG_FILE", path)

	reg, err := registry.Open(path)
	if err != nil {
		t.Fatalf("registry.Open error = %v", err)
	}
	if err := reg.SetConfig(registry.RuntimeConfig{
		DBPath:                "~/.fox-gateway/data/test.db",
		LarkAppID:             "cli_test",
		LarkAppSecret:         "secret",
		LarkVerificationToken: "token",
		ClaudePath:            "claude",
		WorkspaceRoot:         "~/workspace",
		MaxReadOnlyWorkers:    2,
	}); err != nil {
		t.Fatalf("SetConfig error = %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}

	if !filepath.IsAbs(cfg.DBPath) {
		t.Fatalf("DBPath = %q, expected absolute path", cfg.DBPath)
	}
	if !filepath.IsAbs(cfg.WorkspaceRoot) {
		t.Fatalf("WorkspaceRoot = %q, expected absolute path", cfg.WorkspaceRoot)
	}
}
