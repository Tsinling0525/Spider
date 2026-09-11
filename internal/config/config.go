package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
)

type Config struct {
	ListenAddress string
	APIKey        string
	DataDirectory string
	DifyBaseURL   string
	DifyAPIKey    string
	DifyUser      string
	DifyMode      string
	OAuth         *oauth2.Config
}

func Load() (Config, error) {
	listenAddress := env("SPIDER_LISTEN_ADDRESS", "127.0.0.1:8080")
	dataDirectory := env("SPIDER_DATA_DIR", "./data")
	absDataDirectory, err := filepath.Abs(dataDirectory)
	if err != nil {
		return Config{}, err
	}
	apiKey := strings.TrimSpace(os.Getenv("SPIDER_API_KEY"))
	if apiKey == "" && !isLoopbackListenAddress(listenAddress) {
		return Config{}, errors.New("SPIDER_API_KEY is required when SPIDER_LISTEN_ADDRESS is not loopback")
	}
	oauthConfig, err := loadOAuthConfig()
	if err != nil {
		return Config{}, err
	}
	return Config{
		ListenAddress: listenAddress,
		APIKey:        apiKey, DataDirectory: absDataDirectory,
		DifyBaseURL: env("DIFY_BASE_URL", "https://api.dify.ai/v1"),
		DifyAPIKey:  strings.TrimSpace(os.Getenv("DIFY_API_KEY")),
		DifyUser:    env("DIFY_USER", "spider-mail-service"),
		DifyMode:    env("DIFY_MODE", "workflow"),
		OAuth:       oauthConfig,
	}, nil
}

func loadOAuthConfig() (*oauth2.Config, error) {
	clientID := strings.TrimSpace(os.Getenv("GMAIL_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("GMAIL_CLIENT_SECRET"))
	credentialsFile := strings.TrimSpace(os.Getenv("GMAIL_CREDENTIALS_FILE"))
	scopes := []string{gmail.GmailReadonlyScope, gmail.GmailSendScope}

	var config *oauth2.Config
	if clientID != "" || clientSecret != "" {
		if clientID == "" || clientSecret == "" {
			return nil, errors.New("GMAIL_CLIENT_ID and GMAIL_CLIENT_SECRET must be configured together")
		}
		config = &oauth2.Config{ClientID: clientID, ClientSecret: clientSecret, Endpoint: google.Endpoint, Scopes: scopes}
	} else if credentialsFile != "" {
		contents, err := os.ReadFile(credentialsFile)
		if err != nil {
			return nil, fmt.Errorf("read GMAIL_CREDENTIALS_FILE: %w", err)
		}
		config, err = google.ConfigFromJSON(contents, scopes...)
		if err != nil {
			return nil, fmt.Errorf("parse GMAIL_CREDENTIALS_FILE: %w", err)
		}
	} else {
		return nil, errors.New("GMAIL_CREDENTIALS_FILE or GMAIL_CLIENT_ID and GMAIL_CLIENT_SECRET is required")
	}

	if redirectURL := strings.TrimSpace(os.Getenv("GMAIL_REDIRECT_URL")); redirectURL != "" {
		config.RedirectURL = redirectURL
	} else if config.RedirectURL == "" {
		config.RedirectURL = "http://localhost:8080/auth/google/callback"
	}
	return config, nil
}

func isLoopbackListenAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
