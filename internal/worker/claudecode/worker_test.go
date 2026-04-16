package claudecode

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBuildCommandIncludesResumeSession(t *testing.T) {
	_, args := buildCommand(context.Background(), Request{
		ClaudePath:      "claude",
		WorkspaceRoot:   "/tmp",
		Prompt:          "hello",
		OutputFormat:    "json",
		ResumeSessionID: "session-1",
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--output-format json") {
		t.Fatalf("args = %v, expected json output", args)
	}
	if !strings.Contains(joined, "--resume session-1") {
		t.Fatalf("args = %v, expected resume session", args)
	}
	if strings.Contains(joined, "--no-session-persistence") {
		t.Fatalf("args = %v, should not disable session persistence when resuming", args)
	}
}

func TestBuildCommandDisablesSessionPersistenceWhenRequested(t *testing.T) {
	_, args := buildCommand(context.Background(), Request{
		ClaudePath:                "claude",
		WorkspaceRoot:             "/tmp",
		Prompt:                    "hello",
		OutputFormat:              "text",
		DisableSessionPersistence: true,
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--no-session-persistence") {
		t.Fatalf("args = %v, expected no-session-persistence", args)
	}
}

func TestBuildHealthCheckCommandUsesVersion(t *testing.T) {
	_, args := buildHealthCheckCommand(context.Background(), Request{
		ClaudePath:    "claude",
		WorkspaceRoot: "/tmp",
	})
	if len(args) != 1 || args[0] != "--version" {
		t.Fatalf("args = %v, want [--version]", args)
	}
}

func TestParseJSONResult(t *testing.T) {
	result := Result{Stdout: `{"result":"hello","session_id":"session-1"}`}
	if err := parseJSONResult(&result); err != nil {
		t.Fatalf("parseJSONResult error = %v", err)
	}
	if result.Text != "hello" {
		t.Fatalf("Text = %q, want hello", result.Text)
	}
	if result.SessionID != "session-1" {
		t.Fatalf("SessionID = %q, want session-1", result.SessionID)
	}
}

func TestParseJSONResultRequesterConfirmation(t *testing.T) {
	result := Result{Stdout: `{"result":"{\"type\":\"requester_confirmation\",\"title\":\"Need confirmation\",\"body\":\"Continue?\",\"choices\":[{\"id\":\"continue\",\"label\":\"Continue\",\"style\":\"primary\"},{\"id\":\"cancel\",\"label\":\"Cancel\",\"style\":\"danger\"}]}","session_id":"session-1"}`}
	if err := parseJSONResult(&result); err != nil {
		t.Fatalf("parseJSONResult error = %v", err)
	}
	if result.RequesterConfirmation == nil {
		t.Fatal("expected requester confirmation")
	}
	if result.RequesterConfirmation.Title != "Need confirmation" {
		t.Fatalf("Title = %q, want Need confirmation", result.RequesterConfirmation.Title)
	}
}

func TestAppendRequesterConfirmationContract(t *testing.T) {
	got := AppendRequesterConfirmationContract("analyze this repo")
	if !strings.Contains(got, "analyze this repo") || !strings.Contains(got, "requester_confirmation") || !strings.Contains(got, "choices") {
		t.Fatalf("AppendRequesterConfirmationContract() = %q", got)
	}
}

func TestIsMissingSessionError(t *testing.T) {
	err := errors.New("No conversation found with session ID: deadbeef")
	if !IsMissingSessionError(err, Result{}) {
		t.Fatal("expected missing session error to match")
	}
}
