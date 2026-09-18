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

	"github.com/Tsinling0525/Spider/internal/life/food"
)

func env(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}
func main() {
	address := env("LIFE_LISTEN_ADDRESS", "127.0.0.1:8081")
	adapterAddress := env("LIFE_ADAPTER_LISTEN_ADDRESS", "127.0.0.1:8082")
	apiKey := env("LIFE_API_KEY", os.Getenv("SPIDER_API_KEY"))
	adapterToken := os.Getenv("LIFE_ADAPTER_TOKEN")
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		slog.Error("invalid LIFE_LISTEN_ADDRESS")
		os.Exit(1)
	}
	if apiKey == "" && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback()) {
		slog.Error("LIFE_API_KEY is required for non-loopback API listeners")
		os.Exit(1)
	}
	if adapterToken == "" {
		slog.Error("LIFE_ADAPTER_TOKEN is required")
		os.Exit(1)
	}
	provider := &food.MCP{URL: env("LIFE_MCP_URL", "http://127.0.0.1:3917/mcp"), Token: os.Getenv("LIFE_MCP_TOKEN")}
	data := env("LIFE_DATA_DIR", "./data/life")
	adapter, err := food.NewAdapter(provider, filepath.Join(data, "food.json"))
	if err != nil {
		slog.Error("food store failed", "error", err)
		os.Exit(1)
	}
	workflow := &food.Workflow{BaseURL: env("LIFE_DIFY_BASE_URL", "http://localhost/v1"), APIKey: os.Getenv("LIFE_FOOD_DIFY_API_KEY"), User: env("LIFE_PRINCIPAL", "spider-life-owner")}
	public := &http.Server{Addr: address, Handler: food.PublicHandler(workflow, adapter, apiKey), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 250 * time.Second, IdleTimeout: 90 * time.Second}
	internal := &http.Server{Addr: adapterAddress, Handler: food.AdapterHandler(adapter, adapterToken), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 200 * time.Second, IdleTimeout: 90 * time.Second}
	errorsCh := make(chan error, 2)
	go func() { slog.Info("lifed API listening", "address", address); errorsCh <- public.ListenAndServe() }()
	go func() {
		slog.Info("lifed Dify adapter listening", "address", adapterAddress)
		errorsCh <- internal.ListenAndServe()
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-stop:
	case err := <-errorsCh:
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("lifed listener failed", "error", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = public.Shutdown(ctx)
	_ = internal.Shutdown(ctx)
}
