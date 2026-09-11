package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func clearConfigEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"SPIDER_LISTEN_ADDRESS", "SPIDER_API_KEY", "SPIDER_DATA_DIR",
		"GMAIL_CLIENT_ID", "GMAIL_CLIENT_SECRET", "GMAIL_CREDENTIALS_FILE", "GMAIL_REDIRECT_URL",
	} {
		t.Setenv(name, "")
	}
}

func TestLoadAcceptsGoogleCredentialsFileWithoutSplitSecretsOrAPIKeyOnLoopback(t *testing.T) {
	clearConfigEnvironment(t)
	credentialsPath := filepath.Join(t.TempDir(), "client_secret.json")
	credentials := `{"web":{"client_id":"file-client","client_secret":"file-secret","auth_uri":"https://accounts.google.com/o/oauth2/auth","token_uri":"https://oauth2.googleapis.com/token","redirect_uris":["http://localhost:8080/auth/google/callback"]}}`
	if err := os.WriteFile(credentialsPath, []byte(credentials), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GMAIL_CREDENTIALS_FILE", credentialsPath)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "" {
		t.Fatalf("APIKey = %q, want empty local-development key", cfg.APIKey)
	}
	if cfg.OAuth.ClientID != "file-client" || cfg.OAuth.ClientSecret != "file-secret" {
		t.Fatal("OAuth client credentials were not loaded from the Google JSON file")
	}
	if cfg.OAuth.RedirectURL != "http://localhost:8080/auth/google/callback" {
		t.Fatalf("RedirectURL = %q", cfg.OAuth.RedirectURL)
	}
}

func TestLoadRequiresAPIKeyOutsideLoopback(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("SPIDER_LISTEN_ADDRESS", "0.0.0.0:8080")
	t.Setenv("GMAIL_CLIENT_ID", "client")
	t.Setenv("GMAIL_CLIENT_SECRET", "secret")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "SPIDER_API_KEY") {
		t.Fatalf("Load() error = %v, want SPIDER_API_KEY requirement", err)
	}
}
