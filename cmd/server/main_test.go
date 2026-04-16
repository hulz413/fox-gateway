package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestResolveCommandDefaultsToEmpty(t *testing.T) {
	if got := resolveCommand(nil); got != "" {
		t.Fatalf("resolveCommand(nil) = %q, want empty string", got)
	}
}

func TestRunWithoutArgsShowsHelp(t *testing.T) {
	var out bytes.Buffer
	if err := run(nil, strings.NewReader(""), &out, &out); err != nil {
		t.Fatalf("run help error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Usage:") || !strings.Contains(got, "start    Start fox-gateway in the background") || !strings.Contains(got, "upgrade  Upgrade fox-gateway to latest or a specific version") {
		t.Fatalf("help output = %q", got)
	}
}

func TestRunStatusWhenStopped(t *testing.T) {
	t.Setenv("FOX_GATEWAY_CONFIG_FILE", t.TempDir()+"/fox-gateway.json")
	var out bytes.Buffer
	if err := run([]string{"status"}, strings.NewReader(""), &out, &out); err != nil {
		t.Fatalf("run status error = %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Fox Gateway is stopped.") {
		t.Fatalf("status output = %q", got)
	}
}

func TestLoadConfigWithGuidanceUsesNewCommands(t *testing.T) {
	t.Setenv("FOX_GATEWAY_CONFIG_FILE", t.TempDir()+"/fox-gateway.json")
	_, err := loadConfigWithGuidance()
	if err == nil {
		t.Fatal("expected loadConfigWithGuidance to fail without config")
	}
	message := err.Error()
	if !strings.Contains(message, "fox-gateway setup") || !strings.Contains(message, "fox-gateway start") {
		t.Fatalf("unexpected guidance: %q", message)
	}
	if strings.Contains(message, "./fox-gateway") {
		t.Fatalf("guidance should not use ./fox-gateway: %q", message)
	}
}

func TestRunUpgradeRejectsMultipleArgs(t *testing.T) {
	var out bytes.Buffer
	err := run([]string{"upgrade", "v0.1.1", "extra"}, strings.NewReader(""), &out, &out)
	if err == nil {
		t.Fatal("expected upgrade with multiple args to fail")
	}
	if !strings.Contains(err.Error(), "upgrade accepts at most one version argument") {
		t.Fatalf("unexpected error: %v", err)
	}
}
