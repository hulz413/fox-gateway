package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestResolveCommandDefaultsToStart(t *testing.T) {
	if got := resolveCommand(nil); got != "start" {
		t.Fatalf("resolveCommand(nil) = %q, want start", got)
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
