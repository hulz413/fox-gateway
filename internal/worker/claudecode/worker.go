package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type Request struct {
	JobID                     string
	ClaudePath                string
	WorkspaceRoot             string
	Prompt                    string
	Mutating                  bool
	Async                     bool
	OutputFormat              string
	DisableSessionPersistence bool
	ResumeSessionID           string
}

type ConfirmationChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Style string `json:"style,omitempty"`
}

type RequesterConfirmation struct {
	Type    string               `json:"type"`
	Title   string               `json:"title"`
	Body    string               `json:"body"`
	Choices []ConfirmationChoice `json:"choices"`
}

type Result struct {
	PID                   *int
	Stdout                string
	Stderr                string
	ExitCode              int
	StartedAt             time.Time
	FinishedAt            time.Time
	Command               string
	Text                  string
	SessionID             string
	RequesterConfirmation *RequesterConfirmation
}

const requesterConfirmationContract = "If you need the requester to confirm before continuing, respond with ONLY a JSON object (no markdown fences, no extra text) using this schema: {\"type\":\"requester_confirmation\",\"title\":\"short title\",\"body\":\"short explanation\",\"choices\":[{\"id\":\"continue\",\"label\":\"Continue\",\"style\":\"primary\"},{\"id\":\"cancel\",\"label\":\"Cancel\",\"style\":\"danger\"}]}. Constraints: title 1-120 chars, body 1-800 chars, choices 2-3 items, choice ids use only lowercase letters, digits, underscores, or dashes, labels are 1-30 chars, and style may be primary/default/danger. If you do not need confirmation, reply normally."

type Runner struct{}

func New() *Runner {
	return &Runner{}
}

func AppendRequesterConfirmationContract(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return requesterConfirmationContract
	}
	return prompt + "\n\n" + requesterConfirmationContract
}

func (r *Runner) Run(ctx context.Context, req Request) (Result, error) {
	return r.execute(ctx, req)
}

func (r *Runner) Check(ctx context.Context, req Request) (Result, error) {
	return r.executeCommand(ctx, req, buildHealthCheckCommand)
}

func (r *Runner) Probe(ctx context.Context, req Request) (Result, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		req.Prompt = "Reply with OK only."
	}
	if strings.TrimSpace(req.OutputFormat) == "" {
		req.OutputFormat = "text"
	}
	req.DisableSessionPersistence = true
	req.ResumeSessionID = ""
	return r.execute(ctx, req)
}

func (r *Runner) execute(ctx context.Context, req Request) (Result, error) {
	return r.executeCommand(ctx, req, buildCommand)
}

func (r *Runner) executeCommand(ctx context.Context, req Request, builder func(context.Context, Request) (*exec.Cmd, []string)) (Result, error) {
	startedAt := time.Now().UTC()
	cmd, args := builder(ctx, req)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return Result{}, err
	}
	var pid *int
	if cmd.Process != nil {
		value := cmd.Process.Pid
		pid = &value
	}

	err := cmd.Wait()
	finishedAt := time.Now().UTC()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	result := Result{
		PID:        pid,
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		ExitCode:   exitCode,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		Command:    fmt.Sprintf("%s %s", req.ClaudePath, strings.Join(args, " ")),
	}
	if err == nil && usesJSONOutput(req) {
		if parseErr := parseJSONResult(&result); parseErr != nil {
			return result, parseErr
		}
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

func buildCommand(ctx context.Context, req Request) (*exec.Cmd, []string) {
	outputFormat := strings.TrimSpace(req.OutputFormat)
	if outputFormat == "" {
		outputFormat = "text"
	}
	args := []string{"-p", req.Prompt, "--output-format", outputFormat}
	if strings.TrimSpace(req.ResumeSessionID) != "" {
		args = append(args, "--resume", req.ResumeSessionID)
	}
	if req.DisableSessionPersistence && strings.TrimSpace(req.ResumeSessionID) == "" {
		args = append(args, "--no-session-persistence")
	}
	cmd := exec.CommandContext(ctx, req.ClaudePath, args...)
	cmd.Dir = req.WorkspaceRoot
	cmd.Env = sanitizedEnv(os.Environ())
	return cmd, args
}

func buildHealthCheckCommand(ctx context.Context, req Request) (*exec.Cmd, []string) {
	args := []string{"--version"}
	cmd := exec.CommandContext(ctx, req.ClaudePath, args...)
	cmd.Dir = req.WorkspaceRoot
	cmd.Env = sanitizedEnv(os.Environ())
	return cmd, args
}

func usesJSONOutput(req Request) bool {
	return strings.EqualFold(strings.TrimSpace(req.OutputFormat), "json")
}

func parseJSONResult(result *Result) error {
	trimmed := strings.TrimSpace(result.Stdout)
	if trimmed == "" {
		return fmt.Errorf("claude returned empty JSON output")
	}
	var payload struct {
		Result    string `json:"result"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return fmt.Errorf("parse claude json output: %w", err)
	}
	confirmation, err := parseRequesterConfirmationText(payload.Result)
	if err != nil {
		return err
	}
	result.Text = payload.Result
	result.SessionID = payload.SessionID
	result.RequesterConfirmation = confirmation
	return nil
}

func parseRequesterConfirmationText(value string) (*RequesterConfirmation, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return nil, nil
	}
	var confirmation RequesterConfirmation
	if err := json.Unmarshal([]byte(trimmed), &confirmation); err != nil {
		return nil, nil
	}
	if confirmation.Type != "requester_confirmation" {
		return nil, nil
	}
	if err := validateRequesterConfirmation(&confirmation); err != nil {
		return nil, err
	}
	return &confirmation, nil
}

func validateRequesterConfirmation(value *RequesterConfirmation) error {
	if value == nil {
		return nil
	}
	if value.Type != "requester_confirmation" {
		return fmt.Errorf("unsupported requester confirmation type %q", value.Type)
	}
	title := strings.TrimSpace(value.Title)
	body := strings.TrimSpace(value.Body)
	if title == "" || body == "" {
		return fmt.Errorf("requester confirmation requires non-empty title and body")
	}
	if len(title) > 120 {
		return fmt.Errorf("requester confirmation title is too long")
	}
	if len(body) > 800 {
		return fmt.Errorf("requester confirmation body is too long")
	}
	if len(value.Choices) < 2 || len(value.Choices) > 3 {
		return fmt.Errorf("requester confirmation requires 2-3 choices")
	}
	seen := map[string]struct{}{}
	for _, choice := range value.Choices {
		id := strings.TrimSpace(choice.ID)
		label := strings.TrimSpace(choice.Label)
		if id == "" || label == "" {
			return fmt.Errorf("requester confirmation choice requires id and label")
		}
		if len(id) > 32 || len(label) > 30 {
			return fmt.Errorf("requester confirmation choice is too long")
		}
		for _, r := range id {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
				return fmt.Errorf("requester confirmation choice id %q is invalid", id)
			}
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("requester confirmation choice id %q is duplicated", id)
		}
		seen[id] = struct{}{}
		style := strings.TrimSpace(choice.Style)
		if style != "" && style != "primary" && style != "default" && style != "danger" {
			return fmt.Errorf("requester confirmation choice style %q is invalid", style)
		}
	}
	return nil
}

func IsMissingSessionError(err error, result Result) bool {
	if err == nil {
		return false
	}
	combined := strings.ToLower(strings.Join([]string{err.Error(), result.Stdout, result.Stderr}, "\n"))
	return strings.Contains(combined, "no conversation found with session id")
}

func KillProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		return nil
	}
	return process.Kill()
}

func sanitizedEnv(env []string) []string {
	blocked := map[string]struct{}{
		"CMUX_SURFACE_ID":                    {},
		"CMUX_SOCKET_PATH":                   {},
		"CMUX_BUNDLED_CLI_PATH":              {},
		"CMUX_CLAUDE_PID":                    {},
		"CMUX_CLAUDE_WRAPPER":                {},
		"_CMUX_CLAUDE_WRAPPER":               {},
		"CMUX_CUSTOM_CLAUDE_PATH":            {},
		"CMUX_ORIGINAL_NODE_OPTIONS":         {},
		"CMUX_ORIGINAL_NODE_OPTIONS_PRESENT": {},
	}
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		key := entry
		if idx := strings.IndexRune(entry, '='); idx >= 0 {
			key = entry[:idx]
		}
		if _, ok := blocked[key]; ok {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}
