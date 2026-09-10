package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Tsinling0525/Spider/internal/audit"
	maildomain "github.com/Tsinling0525/Spider/internal/mail"
	mailoauth "github.com/Tsinling0525/Spider/internal/oauth"
	"golang.org/x/oauth2"
)

type emptyProvider struct{}

func (emptyProvider) ListThreads(context.Context, maildomain.ListOptions) (maildomain.ThreadPage, error) {
	return maildomain.ThreadPage{Threads: []maildomain.ThreadSummary{}}, nil
}
func (emptyProvider) GetThread(context.Context, string) (maildomain.Thread, error) {
	return maildomain.Thread{}, nil
}
func (emptyProvider) Send(context.Context, maildomain.SendRequest) (maildomain.SendReceipt, error) {
	return maildomain.SendReceipt{}, nil
}

type emptyDrafter struct{}

func (emptyDrafter) Generate(context.Context, maildomain.DraftRequest, maildomain.Thread) (maildomain.Draft, error) {
	return maildomain.Draft{}, nil
}

func TestProtectedEndpointRequiresBearerToken(t *testing.T) {
	directory := t.TempDir()
	auditStore, err := audit.NewJSONLStore(filepath.Join(directory, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	tokenStore := mailoauth.NewFileTokenStore(filepath.Join(directory, "token.json"))
	flow := mailoauth.NewFlow(&oauth2.Config{}, tokenStore)
	handler := NewServer(maildomain.NewService(emptyProvider{}, emptyDrafter{}, auditStore), flow, "secret", slog.New(slog.NewTextHandler(io.Discard, nil)))

	request := httptest.NewRequest(http.MethodGet, "/v1/email/threads", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusUnauthorized)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/email/threads", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusOK)
	}
}
