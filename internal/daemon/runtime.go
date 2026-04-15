package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"fox-gateway/internal/registry"
)

const (
	Version        = 1
	StatusStarting = "starting"
	StatusReady    = "ready"
	StatusStopping = "stopping"
	StatusFailed   = "failed"
)

type ComponentStatus struct {
	Ready   bool       `json:"ready"`
	Detail  string     `json:"detail,omitempty"`
	ReadyAt *time.Time `json:"ready_at,omitempty"`
}

type State struct {
	Version    int             `json:"version"`
	InstanceID string          `json:"instance_id"`
	PID        int             `json:"pid"`
	Port       string          `json:"port"`
	Status     string          `json:"status"`
	StartedAt  time.Time       `json:"started_at"`
	ReadyAt    *time.Time      `json:"ready_at,omitempty"`
	LogPath    string          `json:"log_path"`
	LastError  string          `json:"last_error,omitempty"`
	HTTP       ComponentStatus `json:"http"`
	Feishu     ComponentStatus `json:"feishu"`
	Claude     ComponentStatus `json:"claude"`
}

type Runtime struct {
	mu    sync.RWMutex
	path  string
	state State
}

func DefaultPath() string {
	return filepath.Join(filepath.Dir(registry.DefaultPath()), "runtime.json")
}

func NewState(instanceID string, pid int, logPath string) State {
	return State{
		Version:    Version,
		InstanceID: instanceID,
		PID:        pid,
		Status:     StatusStarting,
		StartedAt:  time.Now().UTC(),
		LogPath:    logPath,
		HTTP: ComponentStatus{
			Detail: "waiting for HTTP server",
		},
		Feishu: ComponentStatus{
			Detail: "waiting for Feishu credentials",
		},
		Claude: ComponentStatus{
			Detail: "waiting for Claude Code preflight",
		},
	}
}

func NewRuntime(path string, state State) (*Runtime, error) {
	r := &Runtime{path: path, state: state}
	if err := Save(path, state); err != nil {
		return nil, err
	}
	return r, nil
}

func Load(path string) (State, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(body, &state); err != nil {
		return State{}, fmt.Errorf("parse runtime state: %w", err)
	}
	if state.Version == 0 {
		state.Version = Version
	}
	if state.Version != Version {
		return State{}, fmt.Errorf("unsupported runtime state version: %d", state.Version)
	}
	return state, nil
}

func Save(path string, state State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create runtime dir: %w", err)
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func Remove(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s State) IsReady() bool {
	return s.Status == StatusReady && s.HTTP.Ready && s.Feishu.Ready && s.Claude.Ready
}

func (r *Runtime) Snapshot() State {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state
}

func (r *Runtime) SetPort(port string) error {
	return r.update(func(state *State) {
		state.Port = port
	})
}

func (r *Runtime) MarkHTTPReady(detail string) error {
	return r.update(func(state *State) {
		markComponent(&state.HTTP, true, detail)
	})
}

func (r *Runtime) MarkFeishuWaiting(detail string) error {
	return r.update(func(state *State) {
		markComponent(&state.Feishu, false, detail)
	})
}

func (r *Runtime) MarkFeishuReady(detail string) error {
	return r.update(func(state *State) {
		markComponent(&state.Feishu, true, detail)
	})
}

func (r *Runtime) MarkClaudeWaiting(detail string) error {
	return r.update(func(state *State) {
		markComponent(&state.Claude, false, detail)
	})
}

func (r *Runtime) MarkClaudeReady(detail string) error {
	return r.update(func(state *State) {
		markComponent(&state.Claude, true, detail)
	})
}

func (r *Runtime) MarkStopping() error {
	return r.update(func(state *State) {
		state.Status = StatusStopping
		state.LastError = ""
	})
}

func (r *Runtime) SetFailed(err error) error {
	return r.update(func(state *State) {
		state.Status = StatusFailed
		if err != nil {
			state.LastError = err.Error()
		}
		state.ReadyAt = nil
	})
}

func (r *Runtime) Remove() error {
	return Remove(r.path)
}

func (r *Runtime) update(mutator func(*State)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	mutator(&r.state)
	reconcileOverallStatus(&r.state)
	return Save(r.path, r.state)
}

func markComponent(component *ComponentStatus, ready bool, detail string) {
	component.Ready = ready
	component.Detail = detail
	if ready {
		if component.ReadyAt == nil {
			now := time.Now().UTC()
			component.ReadyAt = &now
		}
		return
	}
	component.ReadyAt = nil
}

func reconcileOverallStatus(state *State) {
	if state.Status == StatusFailed || state.Status == StatusStopping {
		return
	}
	if state.HTTP.Ready && state.Feishu.Ready && state.Claude.Ready {
		state.Status = StatusReady
		if state.ReadyAt == nil {
			now := time.Now().UTC()
			state.ReadyAt = &now
		}
		state.LastError = ""
		return
	}
	state.Status = StatusStarting
	state.ReadyAt = nil
}
