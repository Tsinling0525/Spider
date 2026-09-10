package config

import (
	"errors"
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
	OAuth         *oauth2.Config
}

func Load() (Config, error) {
	dataDirectory := env("SPIDER_DATA_DIR", "./data")
	absDataDirectory, err := filepath.Abs(dataDirectory)
	if err != nil {
		return Config{}, err
	}
	clientID := strings.TrimSpace(os.Getenv("GMAIL_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("GMAIL_CLIENT_SECRET"))
	redirectURL := env("GMAIL_REDIRECT_URL", "http://127.0.0.1:8080/v1/oauth/gmail/callback")
	apiKey := strings.TrimSpace(os.Getenv("SPIDER_API_KEY"))
	if apiKey == "" {
		return Config{}, errors.New("SPIDER_API_KEY is required")
	}
	if clientID == "" || clientSecret == "" {
		return Config{}, errors.New("GMAIL_CLIENT_ID and GMAIL_CLIENT_SECRET are required")
	}
	return Config{
		ListenAddress: env("SPIDER_LISTEN_ADDRESS", "127.0.0.1:8080"),
		APIKey:        apiKey, DataDirectory: absDataDirectory,
		DifyBaseURL: env("DIFY_BASE_URL", "https://api.dify.ai/v1"),
		DifyAPIKey:  strings.TrimSpace(os.Getenv("DIFY_API_KEY")),
		DifyUser:    env("DIFY_USER", "spider-mail-service"),
		OAuth: &oauth2.Config{
			ClientID: clientID, ClientSecret: clientSecret, RedirectURL: redirectURL,
			Endpoint: google.Endpoint,
			Scopes:   []string{gmail.GmailReadonlyScope, gmail.GmailSendScope},
		},
	}, nil
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
