package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"fox-gateway/internal/config"
	"fox-gateway/internal/daemon"
	"fox-gateway/internal/httpserver"
	"fox-gateway/internal/jobs"
	"fox-gateway/internal/larkconn"
	"fox-gateway/internal/orchestrator"
	"fox-gateway/internal/registry"
	setupcmd "fox-gateway/internal/setup"
	"fox-gateway/internal/store"
	"fox-gateway/internal/worker/claudecode"
)

const (
	startupTimeout      = 30 * time.Second
	shutdownTimeout     = 10 * time.Second
	feishuReadyTimeout  = 20 * time.Second
	claudeProbeTimeout  = 20 * time.Second
	pollInterval        = 250 * time.Millisecond
	endpointProbeTimout = 1 * time.Second
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	command := resolveCommand(args)
	switch command {
	case "", "help", "-h", "--help":
		_, _ = io.WriteString(stdout, usageText())
		return nil
	case "setup":
		return setupcmd.Run(stdin, stdout, registry.DefaultPath())
	case "serve":
		return runServe(stdout)
	case "start":
		return runStart(stdout)
	case "stop":
		return runStop(stdout)
	case "restart":
		if err := runStop(io.Discard); err != nil {
			return err
		}
		return runStart(stdout)
	case "status":
		return runStatus(stdout)
	case "version":
		return runVersion(stdout)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", command, usageText())
	}
}

func resolveCommand(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func usageText() string {
	return "Usage:\n  fox-gateway <command>\n\nCommands:\n  setup    Configure fox-gateway locally\n  start    Start fox-gateway in the background\n  stop     Stop the running fox-gateway service\n  restart  Restart the fox-gateway service\n  status   Show current fox-gateway status\n  version  Show fox-gateway build version\n  help     Show this help\n"
}

func runServe(stdout io.Writer) (runErr error) {
	cfg, err := loadConfigWithGuidance()
	if err != nil {
		return err
	}

	fileLogger, logFile, logPath, err := openDailyLogger(registry.DefaultPath())
	if err != nil {
		return fmt.Errorf("failed to initialize log file: %w", err)
	}
	defer logFile.Close()

	listener, port, err := listenLocal()
	if err != nil {
		fileLogger.Printf("listen error: %v", err)
		return fmt.Errorf("failed to acquire local port: %w", err)
	}
	defer listener.Close()

	ctx := context.Background()
	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		fileLogger.Printf("open store error: %v", err)
		return fmt.Errorf("failed to open local database: %w", err)
	}
	defer st.Close()

	if err := jobs.ReconcileOrphanedWorkers(ctx, st); err != nil {
		fileLogger.Printf("reconcile workers error: %v", err)
		return fmt.Errorf("failed to reconcile previous workers: %w", err)
	}

	reg, err := registry.Open(registry.DefaultPath())
	if err != nil {
		fileLogger.Printf("open registry error: %v", err)
		return fmt.Errorf("failed to open registry: %w", err)
	}

	instanceID := os.Getenv("FOX_GATEWAY_INSTANCE_ID")
	if instanceID == "" {
		instanceID = registry.RandomHex(8)
	}
	runtimeState := daemon.NewState(instanceID, os.Getpid(), logPath)
	runtimeState.Port = port
	runtimeFile, err := daemon.NewRuntime(daemon.DefaultPath(), runtimeState)
	if err != nil {
		fileLogger.Printf("open runtime state error: %v", err)
		return fmt.Errorf("failed to initialize runtime state: %w", err)
	}
	defer func() {
		if runErr == nil {
			_ = runtimeFile.Remove()
			return
		}
		_ = runtimeFile.SetFailed(runErr)
	}()

	larkClient := httpserver.NewLarkClient(cfg.LarkAppID, cfg.LarkAppSecret, fileLogger)
	service := orchestrator.NewService(cfg, st, reg, larkClient)
	longConn := larkconn.New(cfg.LarkAppID, cfg.LarkAppSecret, service, fileLogger)
	larkHandler := httpserver.NewLarkHandler(cfg.LarkVerificationToken, cfg.LarkAppSecret, service)
	server := httpserver.NewWithListener(listener, larkHandler, fileLogger, func() (int, any) {
		snapshot := runtimeFile.Snapshot()
		if snapshot.IsReady() {
			return http.StatusOK, snapshot
		}
		return http.StatusServiceUnavailable, snapshot
	})

	shutdownCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 2)

	go func() {
		<-shutdownCtx.Done()
		_ = runtimeFile.MarkStopping()
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	go func() {
		if err := server.Serve(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http server: %w", err)
		}
	}()
	if err := runtimeFile.MarkHTTPReady("http server listening on 127.0.0.1:" + port); err != nil {
		return fmt.Errorf("mark http ready: %w", err)
	}

	fileLogger.Printf("gateway starting on :%s", port)
	fileLogger.Printf("listening on: http://127.0.0.1:%s", port)
	fileLogger.Printf("Feishu delivery mode: websocket connection")

	if err := runtimeFile.MarkFeishuWaiting("validating Feishu credentials"); err != nil {
		return fmt.Errorf("update Feishu readiness: %w", err)
	}
	feishuValidationCtx, cancelFeishuValidation := context.WithTimeout(shutdownCtx, feishuReadyTimeout)
	if err := larkClient.ValidateCredentials(feishuValidationCtx); err != nil {
		cancelFeishuValidation()
		fileLogger.Printf("validate Feishu credentials error: %v", err)
		return fmt.Errorf("validate Feishu credentials: %w", err)
	}
	cancelFeishuValidation()
	if err := runtimeFile.MarkFeishuWaiting("credentials validated; waiting for websocket connection"); err != nil {
		return fmt.Errorf("update Feishu readiness: %w", err)
	}

	go func() {
		if err := longConn.Start(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
			errCh <- fmt.Errorf("feishu websocket connection: %w", err)
		}
	}()
	feishuReadyCtx, cancelFeishuReady := context.WithTimeout(shutdownCtx, feishuReadyTimeout)
	if err := longConn.WaitUntilReady(feishuReadyCtx); err != nil {
		cancelFeishuReady()
		return fmt.Errorf("wait for Feishu websocket readiness: %w", err)
	}
	cancelFeishuReady()
	_, feishuDetail := longConn.ReadyState()
	if err := runtimeFile.MarkFeishuReady(feishuDetail); err != nil {
		return fmt.Errorf("mark Feishu ready: %w", err)
	}

	if err := runtimeFile.MarkClaudeWaiting("running Claude Code preflight"); err != nil {
		return fmt.Errorf("update Claude readiness: %w", err)
	}
	claudeCtx, cancelClaude := context.WithTimeout(shutdownCtx, claudeProbeTimeout)
	probeResult, err := claudecode.New().Probe(claudeCtx, claudecode.Request{
		ClaudePath:    cfg.ClaudePath,
		WorkspaceRoot: cfg.WorkspaceRoot,
		Prompt:        "Reply with OK only.",
	})
	cancelClaude()
	if err != nil {
		fileLogger.Printf("Claude Code preflight failed: stdout=%q stderr=%q err=%v", probeResult.Stdout, probeResult.Stderr, err)
		return fmt.Errorf("claude code preflight: %w", err)
	}
	if err := runtimeFile.MarkClaudeReady("Claude Code CLI preflight succeeded"); err != nil {
		return fmt.Errorf("mark Claude ready: %w", err)
	}

	fileLogger.Printf("gateway is running")
	if message, ok := reg.BootstrapMessage(); ok {
		fileLogger.Printf("pair code message: %s", message)
	}
	if stdout != nil && stdout != io.Discard {
		fmt.Fprintln(stdout, "Fox Gateway is running.")
		fmt.Fprintf(stdout, "Listening on: http://127.0.0.1:%s\n", port)
		fmt.Fprintln(stdout, "Feishu delivery mode: websocket connection")
	}

	select {
	case err := <-errCh:
		if err != nil {
			fileLogger.Printf("gateway runtime error: %v", err)
			return err
		}
	case <-shutdownCtx.Done():
	}
	service.Runner().Wait()
	return nil
}

func runStart(stdout io.Writer) error {
	if _, err := loadConfigWithGuidance(); err != nil {
		return err
	}
	if state, ok, err := currentRuntime(); err != nil {
		return err
	} else if ok {
		condition := runtimeCondition(state)
		switch condition {
		case "ready":
			fmt.Fprintln(stdout, "Fox Gateway is already running.")
			printRuntimeSummary(stdout, state, condition)
			return nil
		case "starting":
			fmt.Fprintln(stdout, "Fox Gateway is already starting.")
			printRuntimeSummary(stdout, state, condition)
			return nil
		case "stale", "failed":
			if err := daemon.Remove(daemon.DefaultPath()); err != nil {
				return err
			}
		}
	}
	if err := daemon.Remove(daemon.DefaultPath()); err != nil {
		return err
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	_, logFile, _, err := openDailyLogger(registry.DefaultPath())
	if err != nil {
		return fmt.Errorf("initialize log file: %w", err)
	}
	defer logFile.Close()

	instanceID := registry.RandomHex(8)
	cmd := exec.Command(execPath, "serve")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), "FOX_GATEWAY_INSTANCE_ID="+instanceID)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start gateway process: %w", err)
	}
	childPID := cmd.Process.Pid
	_ = cmd.Process.Release()

	if stdout != nil {
		fmt.Fprintln(stdout, "Starting Fox Gateway in background...")
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()
	state, err := waitForReadyInstance(waitCtx, instanceID, childPID, stdout)
	if err != nil {
		return err
	}

	fmt.Fprintln(stdout, "Fox Gateway is running in background.")
	printRuntimeSummary(stdout, state, runtimeCondition(state))

	reg, err := registry.Open(registry.DefaultPath())
	if err == nil {
		if message, ok := reg.BootstrapMessage(); ok {
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "First approver pairing")
			fmt.Fprintln(stdout, "----------------------")
			fmt.Fprintln(stdout, "Send this message in the Feishu bot chat:")
			fmt.Fprintf(stdout, "  %s\n", message)
		}
	}
	return nil
}

func runStop(stdout io.Writer) error {
	state, ok, err := currentRuntime()
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(stdout, "Fox Gateway is already stopped.")
		return nil
	}
	if !processExists(state.PID) {
		_ = daemon.Remove(daemon.DefaultPath())
		fmt.Fprintln(stdout, "Fox Gateway was not running. Cleared stale runtime state.")
		return nil
	}

	process, err := os.FindProcess(state.PID)
	if err != nil {
		return fmt.Errorf("find gateway process: %w", err)
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal gateway process: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	for {
		if !processExists(state.PID) {
			_ = daemon.Remove(daemon.DefaultPath())
			fmt.Fprintln(stdout, "Fox Gateway stopped.")
			return nil
		}
		if _, err := os.Stat(daemon.DefaultPath()); os.IsNotExist(err) {
			fmt.Fprintln(stdout, "Fox Gateway stopped.")
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timed out waiting for Fox Gateway to stop")
		case <-time.After(pollInterval):
		}
	}
}

func runStatus(stdout io.Writer) error {
	state, ok, err := currentRuntime()
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(stdout, "Fox Gateway is stopped.")
		return nil
	}
	condition := runtimeCondition(state)
	fmt.Fprintf(stdout, "Fox Gateway is %s.\n", condition)
	printRuntimeSummary(stdout, state, condition)
	return nil
}

func runVersion(stdout io.Writer) error {
	fmt.Fprintf(stdout, "fox-gateway version %s\n", version)
	fmt.Fprintf(stdout, "commit: %s\n", commit)
	fmt.Fprintf(stdout, "built: %s\n", date)
	return nil
}

func loadConfigWithGuidance() (config.Config, error) {
	cfg, err := config.Load()
	if err == nil {
		return cfg, nil
	}
	return config.Config{}, fmt.Errorf("%v\n\nQuick start:\n  1. Run: fox-gateway setup\n  2. Start: fox-gateway start", err)
}

func currentRuntime() (daemon.State, bool, error) {
	state, err := daemon.Load(daemon.DefaultPath())
	if err != nil {
		if os.IsNotExist(err) {
			return daemon.State{}, false, nil
		}
		return daemon.State{}, false, err
	}
	return state, true, nil
}

func waitForReadyInstance(ctx context.Context, instanceID string, childPID int, stdout io.Writer) (daemon.State, error) {
	lastPhase := ""
	for {
		state, ok, err := currentRuntime()
		if err != nil {
			return daemon.State{}, err
		}
		if ok && state.InstanceID == instanceID {
			phase := startupPhaseMessage(state)
			if stdout != nil && stdout != io.Discard && phase != "" && phase != lastPhase {
				fmt.Fprintf(stdout, "  %s\n", phase)
				lastPhase = phase
			}
			if state.Status == daemon.StatusFailed {
				return daemon.State{}, fmt.Errorf("fox-gateway failed to start: %s\nlog: %s", state.LastError, state.LogPath)
			}
			if state.IsReady() {
				if ready, err := endpointOK(state.Port, "/readyz"); err == nil && ready {
					return state, nil
				}
			}
		}
		if !processExists(childPID) {
			if ok && state.InstanceID == instanceID && state.LastError != "" {
				return daemon.State{}, fmt.Errorf("fox-gateway failed to start: %s\nlog: %s", state.LastError, state.LogPath)
			}
			return daemon.State{}, fmt.Errorf("fox-gateway process exited before becoming ready")
		}
		select {
		case <-ctx.Done():
			if ok && state.InstanceID == instanceID && state.LastError != "" {
				return daemon.State{}, fmt.Errorf("fox-gateway failed to start: %s\nlog: %s", state.LastError, state.LogPath)
			}
			return daemon.State{}, fmt.Errorf("timed out waiting for Fox Gateway readiness")
		case <-time.After(pollInterval):
		}
	}
}

func startupPhaseMessage(state daemon.State) string {
	if !state.HTTP.Ready {
		if state.HTTP.Detail != "" {
			return state.HTTP.Detail
		}
		return "waiting for local HTTP server"
	}
	if !state.Feishu.Ready {
		if state.Feishu.Detail != "" {
			return state.Feishu.Detail
		}
		return "waiting for Feishu readiness"
	}
	if !state.Claude.Ready {
		if state.Claude.Detail != "" {
			return state.Claude.Detail
		}
		return "waiting for Claude Code readiness"
	}
	return ""
}

func runtimeCondition(state daemon.State) string {
	if !processExists(state.PID) {
		return "stale"
	}
	if ready, err := endpointOK(state.Port, "/readyz"); err == nil && ready {
		return "ready"
	}
	if healthy, err := endpointOK(state.Port, "/healthz"); err == nil && healthy {
		return "starting"
	}
	if state.Status == daemon.StatusFailed {
		return "failed"
	}
	return state.Status
}

func printRuntimeSummary(w io.Writer, state daemon.State, condition string) {
	fmt.Fprintf(w, "  PID: %d\n", state.PID)
	if state.Port != "" {
		fmt.Fprintf(w, "  Listening on: http://127.0.0.1:%s\n", state.Port)
	}
	fmt.Fprintf(w, "  Status: %s\n", condition)
	if state.LogPath != "" {
		fmt.Fprintf(w, "  Log file: %s\n", state.LogPath)
	}
	if state.LastError != "" {
		fmt.Fprintf(w, "  Last error: %s\n", state.LastError)
	}
}

func endpointOK(port, path string) (bool, error) {
	if port == "" {
		return false, fmt.Errorf("missing port")
	}
	client := &http.Client{Timeout: endpointProbeTimout}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%s%s", port, path))
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

func listenLocal() (net.Listener, string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	return listener, fmt.Sprintf("%d", port), nil
}

func openDailyLogger(configPath string) (*log.Logger, *os.File, string, error) {
	baseDir := filepath.Dir(configPath)
	logDir := filepath.Join(baseDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, nil, "", err
	}
	logPath := filepath.Join(logDir, time.Now().Format("2006-01-02")+".log")
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, "", err
	}
	return log.New(file, "", log.LstdFlags), file, logPath, nil
}
