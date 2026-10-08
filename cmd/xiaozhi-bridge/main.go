package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Tsinling0525/Spider/internal/xiaozhi"
)

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func run() error {
	ids := []string{}
	for _, id := range strings.Split(os.Getenv("SPIDER_XIAOZHI_CONNECTOR_IDS"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	waitSeconds, err := strconv.Atoi(env("SPIDER_XIAOZHI_WAIT_SECONDS", "10"))
	if err != nil {
		return errors.New("SPIDER_XIAOZHI_WAIT_SECONDS must be an integer between 0 and 15")
	}
	bridge, err := xiaozhi.New(xiaozhi.Config{Endpoint: os.Getenv("MCP_ENDPOINT"), DashboardURL: env("SPIDER_DASHBOARD_BASE_URL", "http://127.0.0.1:8083"), APIKey: os.Getenv("SPIDER_API_KEY"), WaitTimeout: time.Duration(waitSeconds) * time.Second,
		ChannelID: env("SPIDER_XIAOZHI_CHANNEL_ID", "personal-xiaozhi"), ChannelName: env("SPIDER_XIAOZHI_CHANNEL_NAME", "小智"), ConnectorIDs: ids})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return bridge.Run(ctx)
}

func main() {
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("xiaozhi-bridge failed", "error", err)
		os.Exit(1)
	}
}
