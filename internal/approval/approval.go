package approval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

type Payload struct {
	WorkspaceID            string   `json:"workspace_id"`
	BaseRepoState          string   `json:"base_repo_state"`
	ConversationSessionID  string   `json:"conversation_session_id"`
	ConversationGeneration int64    `json:"conversation_generation"`
	ConversationMessageID  string   `json:"conversation_message_id"`
	IntentCategory         string   `json:"intent_category"`
	AllowedActions         []string `json:"allowed_actions"`
	AllowedPaths           []string `json:"allowed_paths"`
	BlockedPathClasses     []string `json:"blocked_path_classes"`
	RuntimeLimitSec        int      `json:"runtime_limit_sec"`
	Async                  bool     `json:"async"`
	Nonce                  string   `json:"nonce"`
}

func HashPayload(payload Payload) (string, error) {
	canonical := payload
	canonical.AllowedActions = sortedCopy(payload.AllowedActions)
	canonical.AllowedPaths = sortedCopy(payload.AllowedPaths)
	canonical.BlockedPathClasses = sortedCopy(payload.BlockedPathClasses)

	body, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func ValidateHash(payload Payload, expected string) bool {
	actual, err := HashPayload(payload)
	if err != nil {
		return false
	}
	return actual == expected
}

func ParsePayload(raw string) (Payload, error) {
	var payload Payload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return Payload{}, err
	}
	return payload, nil
}

func IsApproverAllowed(allowlist []string, openID string) bool {
	for _, candidate := range allowlist {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(openID)) {
			return true
		}
	}
	return false
}

func sortedCopy(values []string) []string {
	copied := append([]string(nil), values...)
	sort.Strings(copied)
	return copied
}
