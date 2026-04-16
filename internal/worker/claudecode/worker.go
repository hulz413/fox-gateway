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

type Result struct {
	PID        *int
	Stdout     string
	Stderr     string
	ExitCode   int
	StartedAt  time.Time
	FinishedAt time.Time
	Command    string
	Text       string
	SessionID  string
}

type Runner struct{}

func New() *Runner {
	return &Runner{}
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
	result.Text = payload.Result
	result.SessionID = payload.SessionID
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
