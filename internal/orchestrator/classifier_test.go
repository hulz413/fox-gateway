package orchestrator

import "testing"

func TestClassifyRequest(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantIntent   string
		wantKind     string
		wantApproval bool
		wantQueue    bool
	}{
		{name: "status", input: "what is the status of my last job?", wantIntent: "status_query", wantKind: "read_only"},
		{name: "mutation", input: "please modify the handler and patch the bug", wantIntent: "mutation", wantKind: "mutation", wantApproval: true, wantQueue: true},
		{name: "read only", input: "analyze the repo and explain the design", wantIntent: "read_only", wantKind: "read_only", wantQueue: true},
		{name: "qa", input: "what is lark", wantIntent: "question", wantKind: "read_only", wantQueue: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyRequest(tt.input)
			if got.Intent != tt.wantIntent || got.Kind != tt.wantKind || got.NeedsApproval != tt.wantApproval || got.ShouldQueue != tt.wantQueue {
				t.Fatalf("ClassifyRequest(%q) = %+v", tt.input, got)
			}
		})
	}
}
