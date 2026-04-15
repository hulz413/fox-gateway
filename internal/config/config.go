package config

import (
	"fmt"
	"path/filepath"

	"fox-gateway/internal/registry"
)

type Config struct {
	DBPath                string
	LarkAppID             string
	LarkAppSecret         string
	LarkVerificationToken string
	ClaudePath            string
	WorkspaceRoot         string
	MaxReadOnlyWorkers    int
}

func Load() (Config, error) {
	reg, err := registry.Open(registry.DefaultPath())
	if err != nil {
		return Config{}, fmt.Errorf("open config registry: %w", err)
	}
	raw := reg.Config()
	if err := raw.Validate(); err != nil {
		return Config{}, fmt.Errorf("fox-gateway is not configured yet. Run `fox-gateway setup` first: %w", err)
	}

	cfg := Config{
		DBPath:                raw.DBPath,
		LarkAppID:             raw.LarkAppID,
		LarkAppSecret:         raw.LarkAppSecret,
		LarkVerificationToken: raw.LarkVerificationToken,
		ClaudePath:            raw.ClaudePath,
		WorkspaceRoot:         raw.WorkspaceRoot,
		MaxReadOnlyWorkers:    raw.MaxReadOnlyWorkers,
	}

	cfg.WorkspaceRoot = registry.ExpandHome(cfg.WorkspaceRoot)
	cfg.DBPath = registry.ExpandHome(cfg.DBPath)

	if abs, err := filepath.Abs(cfg.WorkspaceRoot); err == nil {
		cfg.WorkspaceRoot = abs
	}
	if abs, err := filepath.Abs(cfg.DBPath); err == nil {
		cfg.DBPath = abs
	}

	return cfg, nil
}
