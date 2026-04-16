package approval

import (
	"encoding/json"
	"testing"
)

func TestHashPayloadDeterministicAcrossOrder(t *testing.T) {
	left := Payload{
		WorkspaceID:            "/tmp/workspace",
		BaseRepoState:          "nogit",
		ConversationSessionID:  "session-1",
		ConversationGeneration: 2,
		ConversationMessageID:  "msg_1",
		IntentCategory:         "mutation",
		AllowedActions:         []string{"code_modify", "lint_test"},
		AllowedPaths:           []string{"/repo/a.go", "/repo/b.go"},
		BlockedPathClasses:     []string{"env", "secrets"},
		RuntimeLimitSec:        900,
		Async:                  true,
		Nonce:                  "job_123",
	}
	right := Payload{
		WorkspaceID:            "/tmp/workspace",
		BaseRepoState:          "nogit",
		ConversationSessionID:  "session-1",
		ConversationGeneration: 2,
		ConversationMessageID:  "msg_1",
		IntentCategory:         "mutation",
		AllowedActions:         []string{"lint_test", "code_modify"},
		AllowedPaths:           []string{"/repo/b.go", "/repo/a.go"},
		BlockedPathClasses:     []string{"secrets", "env"},
		RuntimeLimitSec:        900,
		Async:                  true,
		Nonce:                  "job_123",
	}

	leftHash, err := HashPayload(left)
	if err != nil {
		t.Fatalf("HashPayload(left) error = %v", err)
	}
	rightHash, err := HashPayload(right)
	if err != nil {
		t.Fatalf("HashPayload(right) error = %v", err)
	}

	if leftHash != rightHash {
		t.Fatalf("expected stable hash, got %q and %q", leftHash, rightHash)
	}
}

func TestValidateHashDetectsDrift(t *testing.T) {
	payload := Payload{
		WorkspaceID:            "/tmp/workspace",
		BaseRepoState:          "abc123",
		ConversationSessionID:  "session-1",
		ConversationGeneration: 1,
		ConversationMessageID:  "msg_1",
		IntentCategory:         "mutation",
		AllowedActions:         []string{"code_modify"},
		AllowedPaths:           []string{"/repo"},
		RuntimeLimitSec:        900,
		Async:                  true,
		Nonce:                  "job_123",
	}

	hash, err := HashPayload(payload)
	if err != nil {
		t.Fatalf("HashPayload error = %v", err)
	}

	payload.BaseRepoState = "def456"
	if ValidateHash(payload, hash) {
		t.Fatal("expected validation failure when payload drifted")
	}
}

func TestIsApproverAllowed(t *testing.T) {
	allowlist := []string{"ou_1", "ou_2"}
	if !IsApproverAllowed(allowlist, "ou_2") {
		t.Fatal("expected approver to be allowlisted")
	}
	if IsApproverAllowed(allowlist, "ou_3") {
		t.Fatal("expected approver to be rejected")
	}
}

func TestParsePayloadRoundTrip(t *testing.T) {
	payload := Payload{
		WorkspaceID:            "/tmp/workspace",
		BaseRepoState:          "abc123",
		ConversationSessionID:  "session-1",
		ConversationGeneration: 1,
		ConversationMessageID:  "msg_1",
		IntentCategory:         "mutation",
		AllowedActions:         []string{"code_modify", "lint_test"},
		AllowedPaths:           []string{"/repo"},
		BlockedPathClasses:     []string{"env", "secrets"},
		RuntimeLimitSec:        900,
		Async:                  true,
		Nonce:                  "job_123",
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}
	got, err := ParsePayload(string(raw))
	if err != nil {
		t.Fatalf("ParsePayload error = %v", err)
	}
	if got.WorkspaceID != payload.WorkspaceID || got.BaseRepoState != payload.BaseRepoState || got.IntentCategory != payload.IntentCategory || got.Nonce != payload.Nonce {
		t.Fatalf("ParsePayload mismatch = %+v", got)
	}
}
