package orchestrator

import "strings"

type Classification struct {
	Intent            string
	Kind              string
	NeedsApproval     bool
	ShouldQueue       bool
	ConfigurationHint string
	AllowedActions    []string
}

func ClassifyRequest(input string) Classification {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" {
		return Classification{Intent: "empty", Kind: "read_only", ShouldQueue: true, AllowedActions: []string{"read_only_analysis"}}
	}
	if containsAny(lower, "status", "job status", "progress", "where is my task") {
		return Classification{Intent: "status_query", Kind: "read_only", AllowedActions: []string{"status_lookup"}}
	}
	if containsAny(lower, "modify", "change", "patch", "update code", "edit code", "refactor", "fix bug") {
		return Classification{Intent: "mutation", Kind: "mutation", NeedsApproval: true, ShouldQueue: true, AllowedActions: []string{"code_modify", "lint_test"}}
	}
	if containsAny(lower, "analyze", "read", "explain", "what does", "summarize", "review") {
		return Classification{Intent: "read_only", Kind: "read_only", ShouldQueue: true, AllowedActions: []string{"read_only_analysis"}}
	}
	return Classification{Intent: "question", Kind: "read_only", ShouldQueue: true, AllowedActions: []string{"read_only_analysis"}}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
