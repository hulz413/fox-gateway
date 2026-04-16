package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const RegisterCommandPrefix = "fox-gateway register"

type Registry struct {
	path   string
	mu     sync.RWMutex
	config RuntimeConfig
}

func DefaultPath() string {
	if override := strings.TrimSpace(os.Getenv("FOX_GATEWAY_CONFIG_FILE")); override != "" {
		return ExpandHome(override)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".fox-gateway", "fox-gateway.json")
	}
	return filepath.Join(home, ".fox-gateway", "fox-gateway.json")
}

func Open(path string) (*Registry, error) {
	path = ExpandHome(path)
	if path == "" {
		path = DefaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create registry dir: %w", err)
	}

	r := &Registry{path: path}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, err
	}
	if strings.TrimSpace(string(body)) == "" {
		return r, nil
	}
	if err := json.Unmarshal(body, &r.config); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return r, nil
}

func (r *Registry) Path() string {
	return r.path
}

func ParseRegistrationMessage(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	parts := strings.Fields(text)
	if len(parts) != 3 {
		return "", false
	}
	if !strings.EqualFold(parts[0], "fox-gateway") || !strings.EqualFold(parts[1], "register") {
		return "", false
	}
	return parts[2], true
}

func (r *Registry) saveLocked() error {
	body, err := json.MarshalIndent(r.config, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}
