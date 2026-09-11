package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Tsinling0525/Spider/internal/audit"
	"github.com/Tsinling0525/Spider/internal/config"
	"github.com/Tsinling0525/Spider/internal/dify"
	"github.com/Tsinling0525/Spider/internal/gmail"
	"github.com/Tsinling0525/Spider/internal/httpapi"
	maildomain "github.com/Tsinling0525/Spider/internal/mail"
	mailoauth "github.com/Tsinling0525/Spider/internal/oauth"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration failed", "error", err)
		os.Exit(1)
	}
	tokenStore := mailoauth.NewFileTokenStore(filepath.Join(cfg.DataDirectory, "gmail-token.json"))
	auditStore, err := audit.NewJSONLStore(filepath.Join(cfg.DataDirectory, "audit.jsonl"))
	if err != nil {
		logger.Error("audit store failed", "error", err)
		os.Exit(1)
	}
	provider := gmail.NewProvider(cfg.OAuth, tokenStore)
	drafter := dify.NewClient(cfg.DifyBaseURL, cfg.DifyAPIKey, cfg.DifyUser, nil).WithMode(cfg.DifyMode)
	service := maildomain.NewService(provider, drafter, auditStore)
	handler := httpapi.NewServer(service, mailoauth.NewFlow(cfg.OAuth, tokenStore), cfg.APIKey, logger)
	server := &http.Server{
		Addr: cfg.ListenAddress, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 60 * time.Second, IdleTimeout: 90 * time.Second,
	}

	go func() {
		logger.Info("server listening", "address", cfg.ListenAddress)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}
