package daemon

import (
	"path/filepath"
	"testing"
)

func TestSaveLoadAndRemoveRuntimeState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	state := NewState("inst_1", 1234, "/tmp/fox.log")
	state.Port = "9123"

	if err := Save(path, state); err != nil {
		t.Fatalf("Save error = %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if loaded.InstanceID != state.InstanceID || loaded.PID != state.PID || loaded.Port != state.Port {
		t.Fatalf("loaded state = %+v, want %+v", loaded, state)
	}

	if err := Remove(path); err != nil {
		t.Fatalf("Remove error = %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected load after remove to fail")
	}
}

func TestRuntimeBecomesReadyAfterAllComponentsPass(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	r, err := NewRuntime(path, NewState("inst_1", 1234, "/tmp/fox.log"))
	if err != nil {
		t.Fatalf("NewRuntime error = %v", err)
	}

	if err := r.MarkHTTPReady("http ok"); err != nil {
		t.Fatalf("MarkHTTPReady error = %v", err)
	}
	if snapshot := r.Snapshot(); snapshot.IsReady() {
		t.Fatal("runtime should not be ready with only HTTP")
	}

	if err := r.MarkFeishuReady("feishu ok"); err != nil {
		t.Fatalf("MarkFeishuReady error = %v", err)
	}
	if snapshot := r.Snapshot(); snapshot.IsReady() {
		t.Fatal("runtime should not be ready before Claude is ready")
	}

	if err := r.MarkClaudeReady("claude ok"); err != nil {
		t.Fatalf("MarkClaudeReady error = %v", err)
	}
	if snapshot := r.Snapshot(); !snapshot.IsReady() || snapshot.Status != StatusReady {
		t.Fatalf("snapshot = %+v, expected ready runtime", snapshot)
	}
}
