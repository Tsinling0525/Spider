package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Tsinling0525/Spider/internal/llm"
)

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func run() error {
	address := env("LLM_LISTEN_ADDRESS", "127.0.0.1:8084")
	apiKey := env("LLM_API_KEY", os.Getenv("SPIDER_API_KEY"))
	if err := llm.ValidateListener(address, apiKey); err != nil {
		return err
	}
	handler, err := llm.NewHandler(env("LLM_DIFY_BASE_URL", "http://localhost/v1"), apiKey,
		os.Getenv("LLM_DIFY_API_KEY"), env("LLM_DIFY_USER", "spider-llm-owner"))
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr: address, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 90 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	slog.Info("llmd listening", "address", address)
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return err
		}
		return nil
	}
}

func main() {
	if err := run(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("llmd failed", "error", err)
		os.Exit(1)
	}
}
