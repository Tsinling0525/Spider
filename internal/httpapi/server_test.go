package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Tsinling0525/Spider/internal/audit"
	"github.com/Tsinling0525/Spider/internal/humantask"
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

type taskResolver struct{}

func (taskResolver) SubmitHumanInput(context.Context, string, string, map[string]any) error {
	return nil
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

func TestLoopbackDevelopmentWithoutConfiguredKeyAllowsRequests(t *testing.T) {
	directory := t.TempDir()
	auditStore, err := audit.NewJSONLStore(filepath.Join(directory, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	tokenStore := mailoauth.NewFileTokenStore(filepath.Join(directory, "token.json"))
	flow := mailoauth.NewFlow(&oauth2.Config{}, tokenStore)
	handler := NewServer(maildomain.NewService(emptyProvider{}, emptyDrafter{}, auditStore), flow, "", slog.New(slog.NewTextHandler(io.Discard, nil)))

	request := httptest.NewRequest(http.MethodGet, "/v1/email/threads", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusOK)
	}
}

func TestHumanTaskListAndDecision(t *testing.T) {
	directory := t.TempDir()
	auditStore, err := audit.NewJSONLStore(filepath.Join(directory, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := humantask.NewService(filepath.Join(directory, "human-tasks.json"), taskResolver{})
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.RecordPause(context.Background(), humantask.Pause{WorkflowRunID: "run-1", FormID: "form-1", FormToken: "secret", NodeTitle: "Review", Actions: []humantask.Action{{ID: "approve", Title: "Approve"}}}); err != nil {
		t.Fatal(err)
	}
	flow := mailoauth.NewFlow(&oauth2.Config{}, mailoauth.NewFileTokenStore(filepath.Join(directory, "token.json")))
	handler := NewServer(maildomain.NewService(emptyProvider{}, emptyDrafter{}, auditStore), flow, "", slog.New(slog.NewTextHandler(io.Discard, nil)), tasks)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/human-tasks", nil))
	if response.Code != http.StatusOK || bytes.Contains(response.Body.Bytes(), []byte("secret")) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var page struct {
		Tasks    []humantask.Task `json:"tasks"`
		Reviewer string           `json:"reviewer"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Tasks) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if page.Reviewer != "principal:owner" {
		t.Fatalf("reviewer=%q", page.Reviewer)
	}

	body := bytes.NewBufferString(`{"assignee":"principal:owner","expected_version":1}`)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/human-tasks/"+page.Tasks[0].ID+"/claims", body))
	if response.Code != http.StatusOK {
		t.Fatalf("claim status=%d body=%s", response.Code, response.Body.String())
	}

	body = bytes.NewBufferString(`{"action":"approve","inputs":{},"expected_version":2,"idempotency_key":"decision-1","reviewer":"principal:owner"}`)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/human-tasks/"+page.Tasks[0].ID+"/decisions", body))
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"status":"resumed"`)) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHumanTaskIdentityComesFromAuthenticationBoundary(t *testing.T) {
	directory := t.TempDir()
	auditStore, _ := audit.NewJSONLStore(filepath.Join(directory, "audit.jsonl"))
	tasks, _ := humantask.NewService(filepath.Join(directory, "human-tasks.json"), taskResolver{})
	_ = tasks.RecordPause(context.Background(), humantask.Pause{WorkflowRunID: "run-1", FormID: "form-1", FormToken: "secret", Actions: []humantask.Action{{ID: "approve"}}})
	listed, _ := tasks.List()
	flow := mailoauth.NewFlow(&oauth2.Config{}, mailoauth.NewFileTokenStore(filepath.Join(directory, "token.json")))
	handler := NewServerForPrincipal(maildomain.NewService(emptyProvider{}, emptyDrafter{}, auditStore), flow, "secret", "principal:alice", slog.New(slog.NewTextHandler(io.Discard, nil)), tasks)

	body := bytes.NewBufferString(`{"assignee":"principal:bob","expected_version":1}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/human-tasks/"+listed[0].ID+"/claims", body)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("claim status=%d body=%s", response.Code, response.Body.String())
	}

	body = bytes.NewBufferString(`{"assignee":"principal:alice","expected_version":1}`)
	request = httptest.NewRequest(http.MethodPost, "/v1/human-tasks/"+listed[0].ID+"/claims", body)
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("claim status=%d body=%s", response.Code, response.Body.String())
	}

	body = bytes.NewBufferString(`{"action":"approve","inputs":{"note":"form inputs"},"expected_version":2,"idempotency_key":"decision-1","reviewer":"principal:bob"}`)
	request = httptest.NewRequest(http.MethodPost, "/v1/human-tasks/"+listed[0].ID+"/decisions", body)
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("decision status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/human-tasks/"+listed[0].ID+"/history", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || bytes.Contains(response.Body.Bytes(), []byte("form inputs")) || !bytes.Contains(response.Body.Bytes(), []byte("principal:alice")) {
		t.Fatalf("history status=%d body=%s", response.Code, response.Body.String())
	}
}
