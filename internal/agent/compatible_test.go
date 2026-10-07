package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCompatibleBackendUsesNativeToolProtocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/chat/completions" || req.Header.Get("Authorization") != "Bearer private-key" || req.Method != http.MethodPost {
			t.Error("invalid model request")
		}
		var body struct {
			Model    string `json:"model"`
			Parallel bool   `json:"parallel_tool_calls"`
			Messages []Message
			Tools    []struct {
				Type     string   `json:"type"`
				Function ToolSpec `json:"function"`
			}
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Model != "tool-model" || body.Parallel || len(body.Tools) != 1 || body.Tools[0].Function.Name != "read" {
			t.Errorf("invalid payload: %+v %v", body, err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"tool_calls":[{"id":"call-1","type":"function","function":{"name":"read","arguments":"{}"}}]}}]}`))
	}))
	defer server.Close()
	backend, err := NewCompatibleBackend(server.URL+"/v1", "private-key", "tool-model", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	message, err := backend.Next(context.Background(), []Message{{Role: "user", Content: "Read"}}, []ToolSpec{{Name: "read", Description: "Read", Parameters: json.RawMessage(`{"type":"object"}`)}})
	if err != nil || len(message.ToolCalls) != 1 || message.ToolCalls[0].ID != "call-1" {
		t.Fatalf("native tool call lost: %+v %v", message, err)
	}
}

func TestCompatibleBackendRejectsPartialResponsesAndRedactsUpstreamErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"partial", 200, `{"choices":[{"finish_reason":"length","message":{"role":"assistant","content":"Partial"}}]}`},
		{"multiple_choices", 200, `{"choices":[{},{}]}`},
		{"upstream_secret", 401, "Authorization: private-key"},
		{"oversize", 200, strings.Repeat("x", (1<<20)+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			backend, _ := NewCompatibleBackend(server.URL, "private-key", "model", nil)
			_, err := backend.Next(context.Background(), nil, nil)
			if err == nil || strings.Contains(err.Error(), "private-key") {
				t.Fatalf("unsafe response accepted or leaked: %v", err)
			}
		})
	}
}

func TestCompatibleBackendNeverFollowsRedirects(t *testing.T) {
	var leaked atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { leaked.Add(1) }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	backend, _ := NewCompatibleBackend(source.URL, "private-key", "model", nil)
	if _, err := backend.Next(context.Background(), nil, nil); err == nil || leaked.Load() != 0 {
		t.Fatal("provider redirect followed")
	}
}

func TestCompatibleBackendValidatesServerConfiguration(t *testing.T) {
	for _, url := range []string{"file:///etc/passwd", "http://user:secret@example.com/v1", "https://example.com/v1?key=secret", "https://example.com/v1#fragment"} {
		if _, err := NewCompatibleBackend(url, "", "model", nil); err == nil {
			t.Fatalf("invalid configuration accepted: %s", url)
		}
	}
}
