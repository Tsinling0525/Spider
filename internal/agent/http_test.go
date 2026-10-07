package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentHTTPAuthenticatesAndBindsPrincipal(t *testing.T) {
	runtime, _, _ := newMailRuntime(t, filepath.Join(t.TempDir(), "runs.json"), backendFunc(func(context.Context, []Message, []ToolSpec) (Message, error) {
		return Message{Role: "assistant", Content: "Done"}, nil
	}), Options{})
	handler := NewHandler(runtime, nil, "api-key", "owner", nil)
	request := func(body, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/agent/runs", strings.NewReader(body))
		req.Header.Set("Authorization", key)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	for _, key := range []string{"", "api-key", "Bearer wrong"} {
		if response := request(`{"goal":"Read mail"}`, key); response.Code != http.StatusUnauthorized {
			t.Fatalf("bad authentication accepted: %d", response.Code)
		}
	}
	for _, body := range []string{`{"goal":"Read","principal":"attacker"}`, `{"goal":"Read","model":"other"}`, `{"goal":"Read"}{}`, `null`, `{"goal":""}`} {
		if response := request(body, "Bearer api-key"); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid body accepted: %s (%d)", body, response.Code)
		}
	}
	response := request(`{"goal":"Read mail"}`, "Bearer api-key")
	var run Run
	if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil || response.Code != http.StatusAccepted || run.Principal != "owner" {
		t.Fatalf("principal was not server bound: %s", response.Body.String())
	}
}

func TestAgentHTTPRequiresExplicitImmutableApproval(t *testing.T) {
	runtime, mail, _ := newMailRuntime(t, filepath.Join(t.TempDir(), "runs.json"), mailScript(), Options{})
	run, _ := runtime.Start("Draft meeting reply", "owner")
	pending := waitStatus(t, runtime, run.ID, "waiting_approval")
	handler := NewHandler(runtime, nil, "api-key", "owner", nil)
	for _, body := range []string{
		`{"expected_version":1,"approval_id":"fake"}`,
		`{"expected_version":1,"approval_id":"fake","approve":true,"body_text":"Changed"}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/v1/agent/runs/"+run.ID+"/decisions", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer api-key")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid decision accepted: %d", response.Code)
		}
	}
	input, _ := json.Marshal(Decision{ApprovalID: pending.Pending.ID, ExpectedVersion: pending.Version, Approve: true})
	for i := range 2 {
		req := httptest.NewRequest(http.MethodPost, "/v1/agent/runs/"+run.ID+"/decisions", strings.NewReader(string(input)))
		req.Header.Set("Authorization", "Bearer api-key")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		want := http.StatusAccepted
		if i == 1 {
			want = http.StatusConflict
		}
		if response.Code != want {
			t.Fatalf("decision response=%d want=%d: %s", response.Code, want, response.Body.String())
		}
	}
	waitStatus(t, runtime, run.ID, "completed")
	if mail.sends.Load() != 1 {
		t.Fatal("HTTP approval replayed")
	}
}
