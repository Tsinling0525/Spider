package main

import (
	"encoding/json"
	"github.com/Tsinling0525/Spider/internal/device"
	"log/slog"
	"net/http"
	"os"
	"time"
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func main() {
	b, err := os.ReadFile(env("SPIDER_DEVICE_BINDINGS", "./data/device/bindings.json"))
	if err != nil {
		slog.Error("read device bindings", "error", err)
		os.Exit(1)
	}
	var bindings []device.Binding
	if err = json.Unmarshal(b, &bindings); err != nil || len(bindings) == 0 {
		slog.Error("invalid device bindings")
		os.Exit(1)
	}
	seen := map[string]bool{}
	for _, b := range bindings {
		if b.Node == "" || b.Principal == "" || len(b.Token) < 24 || seen[b.Node] {
			slog.Error("invalid or duplicate device binding")
			os.Exit(1)
		}
		seen[b.Node] = true
	}
	admin := os.Getenv("SPIDER_DEVICE_ADMIN_TOKEN")
	if len(admin) < 24 {
		slog.Error("SPIDER_DEVICE_ADMIN_TOKEN must contain at least 24 characters")
		os.Exit(1)
	}
	store, err := device.NewStore(env("SPIDER_DEVICE_STORE", "./data/device/approvals.json"))
	if err != nil {
		slog.Error("open approvals", "error", err)
		os.Exit(1)
	}
	server := &device.Server{Bindings: bindings, AdminToken: admin, Store: store}
	if key := os.Getenv("SPIDER_VOICE_DIFY_API_KEY"); key != "" {
		server.Provider = &device.Dify{BaseURL: env("SPIDER_VOICE_DIFY_BASE_URL", "http://localhost/v1"), APIKey: key, Client: &http.Client{Timeout: 90 * time.Second}}
	}
	if os.Getenv("SPIDER_DEVICE_DIAGNOSTIC") == "1" {
		server.Provider = device.Diagnostic{}
		slog.Warn("device diagnostic mode: recorded audio echo, no AI inference")
	}
	addr := env("SPIDER_DEVICE_LISTEN", "127.0.0.1:8083")
	slog.Info("Spider device service", "address", addr, "voice_configured", server.Provider != nil)
	h := &http.Server{Addr: addr, Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 90 * time.Second}
	if err = h.ListenAndServe(); err != nil {
		slog.Error("device listener", "error", err)
		os.Exit(1)
	}
}
