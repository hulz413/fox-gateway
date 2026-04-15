package claudecode

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type Request struct {
	JobID         string
	ClaudePath    string
	WorkspaceRoot string
	Prompt        string
	Mutating      bool
	Async         bool
}

type Result struct {
	PID        *int
	Stdout     string
	Stderr     string
	ExitCode   int
	StartedAt  time.Time
	FinishedAt time.Time
	Command    string
}

type Runner struct{}

func New() *Runner {
	return &Runner{}
}

func (r *Runner) Run(ctx context.Context, req Request) (Result, error) {
	return r.execute(ctx, req)
}

func (r *Runner) Probe(ctx context.Context, req Request) (Result, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		req.Prompt = "Reply with OK only."
	}
	return r.execute(ctx, req)
}

func (r *Runner) execute(ctx context.Context, req Request) (Result, error) {
	startedAt := time.Now().UTC()
	cmd, args := buildCommand(ctx, req)

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
	if err != nil {
		return result, err
	}
	return result, nil
}

func buildCommand(ctx context.Context, req Request) (*exec.Cmd, []string) {
	args := []string{"-p", req.Prompt, "--output-format", "text", "--no-session-persistence"}
	cmd := exec.CommandContext(ctx, req.ClaudePath, args...)
	cmd.Dir = req.WorkspaceRoot
	cmd.Env = sanitizedEnv(os.Environ())
	return cmd, args
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
