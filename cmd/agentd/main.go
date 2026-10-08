package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/Tsinling0525/Spider/internal/agent"
	"github.com/Tsinling0525/Spider/internal/audit"
	"github.com/Tsinling0525/Spider/internal/calendar"
	"github.com/Tsinling0525/Spider/internal/config"
	"github.com/Tsinling0525/Spider/internal/dify"
	"github.com/Tsinling0525/Spider/internal/gmail"
	"github.com/Tsinling0525/Spider/internal/httpapi"
	"github.com/Tsinling0525/Spider/internal/humantask"
	maildomain "github.com/Tsinling0525/Spider/internal/mail"
	mailoauth "github.com/Tsinling0525/Spider/internal/oauth"
)

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	backend, err := agent.NewCompatibleBackend(env("AGENT_MODEL_BASE_URL", "http://127.0.0.1:11434/v1"), os.Getenv("AGENT_MODEL_API_KEY"), os.Getenv("AGENT_MODEL"), nil)
	if err != nil {
		return err
	}
	steps, err := strconv.Atoi(env("AGENT_MAX_STEPS", "16"))
	if err != nil || steps <= 0 {
		return errors.New("AGENT_MAX_STEPS must be a positive integer")
	}
	timeout, err := time.ParseDuration(env("AGENT_RUN_TIMEOUT", "3m"))
	if err != nil || timeout <= 0 {
		return errors.New("AGENT_RUN_TIMEOUT must be a positive duration")
	}
	tokens := mailoauth.NewFileTokenStore(filepath.Join(cfg.DataDirectory, "gmail-token.json"))
	audits, err := audit.NewJSONLStore(filepath.Join(cfg.DataDirectory, "audit.jsonl"))
	if err != nil {
		return err
	}
	provider := gmail.NewProvider(cfg.OAuth, tokens)
	// The existing fixed workflow remains an optional legacy draft capability.
	drafter := dify.NewClient(cfg.DifyBaseURL, cfg.DifyAPIKey, cfg.DifyUser, nil).WithMode(cfg.DifyMode)
	humanTasks, err := humantask.NewService(filepath.Join(cfg.DataDirectory, "human-tasks.json"), drafter)
	if err != nil {
		return err
	}
	drafter.WithPauseSink(humanTasks)
	mailService := maildomain.NewService(provider, drafter, audits)
	runtime, err := agent.NewRuntime(filepath.Join(cfg.DataDirectory, "agent-runs.json"), backend,
		agent.NewMailTools(mailService, provider.AccountEmail, calendar.NewProvider(cfg.OAuth, tokens)),
		agent.Options{MaxSteps: steps, Timeout: timeout, Timezone: env("AGENT_TIMEZONE", "UTC")})
	if err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			slog.Error("agent shutdown failed", "error", err)
		}
	}()
	flow := mailoauth.NewFlow(cfg.OAuth, tokens)
	legacy := httpapi.NewServerForPrincipal(mailService, flow, cfg.APIKey, cfg.ReviewerID, slog.Default(), humanTasks)
	server := &http.Server{
		Addr: cfg.ListenAddress, Handler: agent.NewHandler(runtime, flow, cfg.APIKey, cfg.ReviewerID, legacy),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 90 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	slog.Info("agentd listening", "address", cfg.ListenAddress)
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
		slog.Error("agentd failed", "error", err)
		os.Exit(1)
	}
}
