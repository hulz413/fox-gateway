package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"fox-gateway/internal/config"
	"fox-gateway/internal/httpserver"
	"fox-gateway/internal/jobs"
	"fox-gateway/internal/larkconn"
	"fox-gateway/internal/orchestrator"
	"fox-gateway/internal/registry"
	setupcmd "fox-gateway/internal/setup"
	"fox-gateway/internal/store"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		if err := setupcmd.Run(os.Stdin, os.Stdout, registry.DefaultPath()); err != nil {
			log.Fatalf("setup: %v", err)
		}
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Quick start:")
		fmt.Fprintln(os.Stderr, "  1. Run: ./fox-gateway setup")
		fmt.Fprintln(os.Stderr, "  2. Start: ./fox-gateway")
		os.Exit(1)
	}

	fileLogger, logFile, _, err := openDailyLogger(registry.DefaultPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize log file: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()

	listener, port, err := listenLocal()
	if err != nil {
		fileLogger.Printf("listen error: %v", err)
		fmt.Fprintf(os.Stderr, "failed to acquire local port: %v\n", err)
		os.Exit(1)
	}
	defer listener.Close()

	ctx := context.Background()
	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		fileLogger.Printf("open store error: %v", err)
		fmt.Fprintf(os.Stderr, "failed to open local database: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	if err := jobs.ReconcileOrphanedWorkers(ctx, st); err != nil {
		fileLogger.Printf("reconcile workers error: %v", err)
		fmt.Fprintf(os.Stderr, "failed to reconcile previous workers: %v\n", err)
		os.Exit(1)
	}

	reg, err := registry.Open(registry.DefaultPath())
	if err != nil {
		fileLogger.Printf("open registry error: %v", err)
		fmt.Fprintf(os.Stderr, "failed to open registry: %v\n", err)
		os.Exit(1)
	}

	larkClient := httpserver.NewLarkClient(cfg.LarkAppID, cfg.LarkAppSecret, fileLogger)
	service := orchestrator.NewService(cfg, st, reg, larkClient)
	larkHandler := httpserver.NewLarkHandler(cfg.LarkVerificationToken, cfg.LarkAppSecret, service)
	server := httpserver.NewWithListener(listener, larkHandler, fileLogger)
	longConn := larkconn.New(cfg.LarkAppID, cfg.LarkAppSecret, service, fileLogger)

	shutdownCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 2)

	go func() {
		<-shutdownCtx.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	go func() {
		if err := server.Serve(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http server: %w", err)
		}
	}()
	go func() {
		if err := longConn.Start(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
			errCh <- fmt.Errorf("feishu websocket connection: %w", err)
		}
	}()

	fileLogger.Printf("gateway starting on :%s", port)
	fileLogger.Printf("gateway is running")
	fileLogger.Printf("listening on: http://127.0.0.1:%s", port)
	fileLogger.Printf("Feishu delivery mode: websocket connection")
	fmt.Fprintln(os.Stdout, "Fox Gateway is running.")
	fmt.Fprintf(os.Stdout, "Listening on: http://127.0.0.1:%s\n", port)
	fmt.Fprintln(os.Stdout, "Feishu delivery mode: websocket connection")
	if message, ok := reg.BootstrapMessage(); ok {
		fileLogger.Printf("pair code message: %s", message)
	}

	select {
	case err := <-errCh:
		if err != nil {
			fileLogger.Printf("gateway runtime error: %v", err)
			fmt.Fprintf(os.Stderr, "gateway stopped with error: %v\n", err)
			os.Exit(1)
		}
	case <-shutdownCtx.Done():
	}
	service.Runner().Wait()
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
