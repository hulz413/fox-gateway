package larkutil

import (
	"bytes"
	"encoding/json"
	"strings"
)

type ActionRequest struct {
	JobID       string
	RequestKind string
	ChoiceID    string
	ActorOpenID string
}

func ExtractText(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return raw
	}
	if text, ok := payload["text"].(string); ok {
		return text
	}
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(payload)
	return strings.TrimSpace(buf.String())
}
