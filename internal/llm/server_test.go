package llm

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat-messages" || r.URL.RawQuery != "" || r.Method != "POST" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer dify-secret" || r.Header.Get("Cookie") != "" {
			t.Error("client credentials leaked")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body["query"] != "hello" || body["user"] != "owner" || body["conversation_id"] != "conv-123" || body["response_mode"] != "blocking" {
			t.Errorf("body: %#v", body)
		}
		if inputs, ok := body["inputs"].(map[string]any); !ok || inputs["language"] != "zh" {
			t.Errorf("inputs: %#v", body["inputs"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":"model missing"}`))
	}))
	defer upstream.Close()
	handler, err := NewHandler(upstream.URL+"/v1/", "secret", "dify-secret", "owner")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/v1/chat-messages?test=1", strings.NewReader(`{"query":"hello","inputs":{"language":"zh"},"conversation_id":"conv-123","user":"spoofed","response_mode":"blocking"}`))
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Cookie", "session=private")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 422 || response.Body.String() != `{"error":"model missing"}` {
		t.Fatalf("response: %d %s", response.Code, response.Body)
	}
}

func TestAuthAndRoutes(t *testing.T) {
	handler, _ := NewHandler("http://localhost/v1", "secret", "dify-secret", "owner")
	for _, tc := range []struct {
		path, token string
		status      int
	}{
		{"/api/tags", "", 401}, {"/healthz", "Bearer wrong", 401},
		{"/healthz", "Bearer secret", 200}, {"/unknown", "Bearer secret", 404},
		{"/v1/chat-messages", "Bearer secret", 405}, {"/api/chat", "Bearer secret", 404}, {"/v1/chat/completions", "Bearer secret", 404},
	} {
		request := httptest.NewRequest("GET", tc.path, nil)
		request.Header.Set("Authorization", tc.token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Errorf("%s: got %d want %d", tc.path, response.Code, tc.status)
		}
	}
}

func TestStreamAndCancellation(t *testing.T) {
	for _, path := range []string{"/v1/chat-messages"} {
		t.Run(path, func(t *testing.T) {
			cancelled := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != path {
					t.Errorf("path: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: first\n\n"))
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(cancelled)
			}))
			defer upstream.Close()
			handler, _ := NewHandler(upstream.URL+"/v1", "", "dify-secret", "owner")
			gateway := httptest.NewServer(handler)
			defer gateway.Close()
			client := &http.Client{Timeout: 3 * time.Second}
			response, err := client.Post(gateway.URL+path, "application/json", strings.NewReader(`{"query":"hello"}`))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			line, err := bufio.NewReader(response.Body).ReadString('\n')
			if err != nil || line != "data: first\n" {
				t.Fatalf("stream: %q %v", line, err)
			}
			response.Body.Close()
			select {
			case <-cancelled:
			case <-time.After(3 * time.Second):
				t.Fatal("upstream was not cancelled")
			}
		})
	}
}

func TestUnavailable(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	upstream.Close()
	handler, _ := NewHandler(upstream.URL+"/v1", "", "dify-secret", "owner")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/v1/chat-messages", strings.NewReader(`{"query":"hello"}`)))
	if response.Code != 502 || !strings.Contains(response.Body.String(), "dify unavailable") {
		t.Fatalf("response: %d %s", response.Code, response.Body)
	}
}

func TestConfiguration(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8084", "[::1]:8084"} {
		if err := ValidateListener(address, ""); err != nil {
			t.Error(err)
		}
	}
	for _, address := range []string{"0.0.0.0:8084", ":8084", "localhost:8084", "invalid"} {
		if err := ValidateListener(address, ""); err == nil {
			t.Errorf("accepted %s without key", address)
		}
	}
	if err := ValidateListener("0.0.0.0:8084", "secret"); err != nil {
		t.Error(err)
	}
	for _, target := range []string{"", "ftp://localhost", "http://", "http://user:pass@localhost", "http://localhost?x=1", "http://localhost/#fragment"} {
		if _, err := NewHandler(target, "", "dify-secret", "owner"); err == nil {
			t.Errorf("accepted %q", target)
		}
	}
}

func TestInvalidRequests(t *testing.T) {
	handler, _ := NewHandler("http://localhost/v1", "", "dify-secret", "owner")
	for _, body := range []string{`null`, `[]`, `{}`, `{"query":" "}`, `{"query":12}`, `{"query":"hi","response_mode":"invalid"}`, `{"query":"hi"}{}`} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("POST", "/v1/chat-messages", strings.NewReader(body)))
		if response.Code != 400 {
			t.Errorf("%s: %d", body, response.Code)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/v1/chat-messages", strings.NewReader(strings.Repeat("x", (2<<20)+1))))
	if response.Code != 413 {
		t.Errorf("oversize: %d", response.Code)
	}
	if _, err := NewHandler("http://localhost/v1", "", "", "owner"); err == nil {
		t.Error("accepted missing app key")
	}
	if _, err := NewHandler("http://localhost/v1", "", "key", ""); err == nil {
		t.Error("accepted missing user")
	}
}

func TestDefaultsAndResponsePassthrough(t *testing.T) {
	const reply = `{"answer":"hello","conversation_id":"conv-123","message_id":"msg-123"}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["response_mode"] != "streaming" || body["user"] != "owner" || body["inputs"] == nil {
			t.Errorf("defaults: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(reply))
	}))
	defer upstream.Close()
	handler, _ := NewHandler(upstream.URL+"/v1", "", "dify-secret", "owner")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/v1/chat-messages", strings.NewReader(`{"query":"hi"}`)))
	if response.Code != 200 || response.Body.String() != reply {
		t.Fatalf("%d %s", response.Code, response.Body)
	}
}
