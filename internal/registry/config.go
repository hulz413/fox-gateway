package registry

import (
	"fmt"
	"strings"
)

type RuntimeConfig struct {
	DBPath                string `json:"db_path"`
	LarkAppID             string `json:"lark_app_id"`
	LarkAppSecret         string `json:"lark_app_secret"`
	LarkVerificationToken string `json:"lark_verification_token"`
	ClaudePath            string `json:"claude_path"`
	WorkspaceRoot         string `json:"workspace_root"`
	MaxReadOnlyWorkers    int    `json:"max_readonly_workers"`
}

func (c RuntimeConfig) Validate() error {
	if strings.TrimSpace(c.DBPath) == "" {
		return fmt.Errorf("db_path is required")
	}
	if strings.TrimSpace(c.LarkAppID) == "" {
		return fmt.Errorf("lark_app_id is required")
	}
	if strings.TrimSpace(c.LarkAppSecret) == "" {
		return fmt.Errorf("lark_app_secret is required")
	}
	if strings.TrimSpace(c.LarkVerificationToken) == "" {
		return fmt.Errorf("lark_verification_token is required")
	}
	if strings.TrimSpace(c.ClaudePath) == "" {
		return fmt.Errorf("claude_path is required")
	}
	if strings.TrimSpace(c.WorkspaceRoot) == "" {
		return fmt.Errorf("workspace_root is required")
	}
	if c.MaxReadOnlyWorkers < 1 {
		return fmt.Errorf("max_readonly_workers must be >= 1")
	}
	return nil
}

func (r *Registry) Config() RuntimeConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state.Config
}

func (r *Registry) SetConfig(cfg RuntimeConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Config = cfg
	if r.state.Bootstrap.Key == "" {
		r.state.Bootstrap = newBootstrap()
	}
	return r.saveLocked()
}

func (r *Registry) HasConfig() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state.Config.Validate() == nil
}
