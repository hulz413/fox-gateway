package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	Version               = 1
	BootstrapModeChatKey  = "chat_key"
	UserStatusActive      = "active"
	RegisterCommandPrefix = "fox-gateway register"
)

type User struct {
	OpenID        string    `json:"open_id"`
	RegisteredAt  time.Time `json:"registered_at"`
	RegisteredVia string    `json:"registered_via"`
	ChatID        string    `json:"chat_id"`
	Status        string    `json:"status"`
}

type Bootstrap struct {
	Mode                string     `json:"mode"`
	Key                 string     `json:"key"`
	IssuedAt            time.Time  `json:"issued_at"`
	ConsumedAt          *time.Time `json:"consumed_at,omitempty"`
	InitializedByOpenID string     `json:"initialized_by_open_id,omitempty"`
}

type State struct {
	Version   int           `json:"version"`
	Config    RuntimeConfig `json:"config"`
	Users     []User        `json:"users"`
	Bootstrap Bootstrap     `json:"bootstrap"`
}

type Registry struct {
	path  string
	mu    sync.RWMutex
	state State
}

func DefaultPath() string {
	if override := strings.TrimSpace(os.Getenv("FOX_GATEWAY_CONFIG_FILE")); override != "" {
		return expandHome(override)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".fox-gateway", "fox-gateway.json")
	}
	return filepath.Join(home, ".fox-gateway", "fox-gateway.json")
}

func Open(path string) (*Registry, error) {
	path = expandHome(path)
	if path == "" {
		path = DefaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create registry dir: %w", err)
	}

	r := &Registry{path: path}
	if _, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		r.state = newState()
		if err := r.saveLocked(); err != nil {
			return nil, err
		}
		return r, nil
	}

	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &r.state); err != nil {
		return nil, fmt.Errorf("parse registry: %w", err)
	}
	if r.state.Version == 0 {
		r.state.Version = Version
	}
	if r.state.Version != Version {
		return nil, fmt.Errorf("unsupported registry version: %d", r.state.Version)
	}
	if len(r.state.Users) == 0 && (r.state.Bootstrap.Key == "" || r.state.Bootstrap.ConsumedAt != nil) {
		r.state.Bootstrap = newBootstrap()
		if err := r.saveLocked(); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *Registry) Path() string {
	return r.path
}

func (r *Registry) BootstrapMessage() (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.state.Users) > 0 || r.state.Bootstrap.Key == "" || r.state.Bootstrap.ConsumedAt != nil {
		return "", false
	}
	return fmt.Sprintf("%s %s", RegisterCommandPrefix, r.state.Bootstrap.Key), true
}

func (r *Registry) HasUser(openID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, user := range r.state.Users {
		if user.Status == UserStatusActive && strings.EqualFold(strings.TrimSpace(user.OpenID), strings.TrimSpace(openID)) {
			return true
		}
	}
	return false
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

func (r *Registry) RegisterUserWithBootstrap(openID, chatID, key string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, user := range r.state.Users {
		if user.Status == UserStatusActive && strings.EqualFold(strings.TrimSpace(user.OpenID), strings.TrimSpace(openID)) {
			return false, nil
		}
	}
	if len(r.state.Users) > 0 {
		return false, fmt.Errorf("bootstrap registration is closed")
	}
	if r.state.Bootstrap.Key == "" || r.state.Bootstrap.ConsumedAt != nil {
		return false, fmt.Errorf("bootstrap registration is unavailable")
	}
	if strings.TrimSpace(key) != strings.TrimSpace(r.state.Bootstrap.Key) {
		return false, fmt.Errorf("invalid registration key")
	}

	now := time.Now().UTC()
	r.state.Users = append(r.state.Users, User{
		OpenID:        openID,
		RegisteredAt:  now,
		RegisteredVia: "feishu_message",
		ChatID:        chatID,
		Status:        UserStatusActive,
	})
	r.state.Bootstrap.InitializedByOpenID = openID
	r.state.Bootstrap.ConsumedAt = &now
	if err := r.saveLocked(); err != nil {
		return false, err
	}
	return true, nil
}

func newState() State {
	return State{
		Version:   Version,
		Config:    RuntimeConfig{},
		Users:     []User{},
		Bootstrap: newBootstrap(),
	}
}

func newBootstrap() Bootstrap {
	return Bootstrap{
		Mode:     BootstrapModeChatKey,
		Key:      RandomHex(16),
		IssuedAt: time.Now().UTC(),
	}
}

func (r *Registry) saveLocked() error {
	body, err := json.MarshalIndent(r.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

func expandHome(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}
