package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Tsinling0525/Spider/internal/dashboard"
)

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func run() error {
	address := env("SPIDER_DASHBOARD_LISTEN_ADDRESS", "127.0.0.1:8083")
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	key := os.Getenv("SPIDER_API_KEY")
	ip := net.ParseIP(host)
	if key == "" && host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("SPIDER_API_KEY is required for a non-loopback dashboard listener")
	}
	modelBase := env("AGENT_MODEL_BASE_URL", "http://127.0.0.1:11434/v1")
	modelKey := os.Getenv("AGENT_MODEL_API_KEY")
	service, err := dashboard.NewService(filepath.Join(env("SPIDER_DATA_DIR", "./data"), "dashboard", "state.json"), nil)
	if err != nil {
		return err
	}
	voice := dashboard.VoiceOptions{
		STT:   dashboard.AudioModel{BaseURL: env("VOICE_STT_BASE_URL", modelBase), APIKey: env("VOICE_STT_API_KEY", modelKey), Model: os.Getenv("VOICE_STT_MODEL")},
		TTS:   dashboard.AudioModel{BaseURL: env("VOICE_TTS_BASE_URL", modelBase), APIKey: env("VOICE_TTS_API_KEY", modelKey), Model: os.Getenv("VOICE_TTS_MODEL")},
		Voice: env("VOICE_TTS_VOICE", "alloy"),
	}
	defer service.StopChannels()
	ids := func(value string) []string { return strings.Fields(strings.ReplaceAll(value, ",", " ")) }
	if err := service.SetVoiceChannels(ids(os.Getenv("SPIDER_XIAOZHI_VOICE_CHANNELS")), ids(os.Getenv("SPIDER_XIAOZHI_VOICE_CONNECTOR_IDS"))); err != nil {
		return err
	}
	if err := service.ImportEnvironmentModels(modelBase, modelKey, os.Getenv("AGENT_MODEL"), voice); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := service.StartMemory(ctx); err != nil {
		return err
	}
	defer func() { service.StopChannels(); service.StopMemory() }()
	api := dashboard.NewHandler(service, dashboard.HTTPOptions{APIKey: key, Voice: voice, AllowedOrigins: strings.Fields(env("SPIDER_DASHBOARD_ORIGINS", "http://localhost:5174 http://127.0.0.1:5174"))})
	mux := http.NewServeMux()
	mux.Handle("/v1/dashboard/", api)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","service":"dashboardd"}`))
	})
	dist := env("SPIDER_DASHBOARD_DIST", "./dashboard/dist")
	files := http.FileServer(http.Dir(dist))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		files.ServeHTTP(w, r)
	})
	mux.Handle("GET /assets/", files)
	// A local unauthenticated deployment accepts only loopback Host values,
	// so a remote website cannot reach it by rebinding its domain to localhost.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if key == "" {
			requestHost := r.Host
			if value, _, err := net.SplitHostPort(requestHost); err == nil {
				requestHost = value
			}
			requestIP := net.ParseIP(requestHost)
			if !strings.EqualFold(requestHost, "localhost") && (requestIP == nil || !requestIP.IsLoopback()) {
				http.Error(w, "invalid dashboard host", http.StatusForbidden)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 4 * time.Minute, IdleTimeout: 90 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	slog.Info("dashboardd listening", "address", address)
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(ctx)
	}
}

func main() {
	if err := run(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("dashboardd failed", "error", err)
		os.Exit(1)
	}
}
