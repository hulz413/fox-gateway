package httpserver

import (
	"context"
	"strings"
	"testing"

	"fox-gateway/internal/approval"
)

func TestBuildDecisionCardPayload(t *testing.T) {
	payload, err := buildDecisionCardPayload(approval.DecisionCard{
		JobID:       "job_1",
		RequestKind: approval.KindRequesterConfirmation,
		Title:       "Need confirmation",
		Body:        "Continue with this operation?",
		Theme:       "blue",
		Choices: []approval.Choice{
			{ID: "continue", Label: "Continue", Style: "primary"},
			{ID: "cancel", Label: "Cancel", Style: "danger"},
		},
	})
	if err != nil {
		t.Fatalf("buildDecisionCardPayload error = %v", err)
	}
	var card = payload
	header := card["header"].(map[string]any)
	if header["template"] != "blue" {
		t.Fatalf("template = %v, want blue", header["template"])
	}
	elements := card["elements"].([]any)
	action := elements[1].(map[string]any)
	actions := action["actions"].([]any)
	if len(actions) != 2 {
		t.Fatalf("actions len = %d, want 2", len(actions))
	}
	first := actions[0].(map[string]any)
	value := first["value"].(map[string]string)
	if value["job_id"] != "job_1" || value["request_kind"] != approval.KindRequesterConfirmation || value["choice_id"] != "continue" {
		t.Fatalf("first button value = %+v", value)
	}
}

func TestBuildDecisionCardPayloadTruncatesBody(t *testing.T) {
	payload, err := buildDecisionCardPayload(approval.DecisionCard{
		JobID:       "job_1",
		RequestKind: approval.KindApproval,
		Title:       strings.Repeat("T", 150),
		Body:        strings.Repeat("B", 900),
		Choices: []approval.Choice{
			{ID: "approve", Label: "Approve"},
			{ID: "reject", Label: "Reject"},
		},
	})
	if err != nil {
		t.Fatalf("buildDecisionCardPayload error = %v", err)
	}
	header := payload["header"].(map[string]any)
	title := header["title"].(map[string]any)["content"].(string)
	body := payload["elements"].([]any)[0].(map[string]any)["content"].(string)
	if !strings.Contains(title, "...") || !strings.Contains(body, "...") {
		t.Fatalf("expected truncated title/body, got title=%q body=%q", title, body)
	}
}

func TestSendDecisionCardUsesInteractiveMessage(t *testing.T) {
	client := NewLarkClient("", "", nil)
	err := client.SendDecisionCard(context.Background(), "chat_1", approval.DecisionCard{
		JobID:       "job_1",
		RequestKind: approval.KindRequesterConfirmation,
		Title:       "Need confirmation",
		Body:        "Continue?",
		Choices: []approval.Choice{
			{ID: "continue", Label: "Continue"},
			{ID: "cancel", Label: "Cancel"},
		},
	})
	if err == nil {
		t.Fatal("expected send decision card to fail without credentials")
	}
	if !strings.Contains(err.Error(), "LARK_APP_ID or LARK_APP_SECRET") {
		t.Fatalf("unexpected error: %v", err)
	}
}
