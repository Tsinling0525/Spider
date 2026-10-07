package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tsinling0525/Spider/internal/agent"
)

type backendFunc func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Message, error)

func (f backendFunc) Next(ctx context.Context, messages []agent.Message, tools []agent.ToolSpec) (agent.Message, error) {
	return f(ctx, messages, tools)
}

func serviceForTest(t *testing.T, backend agent.Backend) *Service {
	t.Helper()
	service, err := NewService(filepath.Join(t.TempDir(), "state.json"), backend)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// Stateful MCP fixture deliberately returns a paginated SSE tools/list response.
func mcpForTest(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-secret" {
			t.Error("MCP credential missing")
		}
		if r.Method == "DELETE" {
			w.WriteHeader(204)
			return
		}
		var request struct {
			ID     int                        `json:"id"`
			Method string                     `json:"method"`
			Params map[string]json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request.Method != "initialize" && r.Header.Get("Mcp-Session-Id") != "fixture-session" {
			t.Error("session id missing")
		}
		if request.Method == "initialize" {
			w.Header().Set("Mcp-Session-Id", "fixture-session")
			respond(w, 200, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}}})
			return
		}
		if r.Header.Get("MCP-Protocol-Version") != "2025-11-25" {
			t.Error("negotiated version not preserved")
		}
		switch request.Method {
		case "notifications/initialized":
			w.WriteHeader(202)
		case "tools/list":
			tools := []MCPTool{{Name: "lookup", Description: "Read a record", InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`)}}
			result := map[string]any{"tools": tools, "nextCursor": "page2"}
			if len(request.Params["cursor"]) > 0 {
				result = map[string]any{"tools": []MCPTool{}}
			}
			raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "id: prime\ndata:\n\nevent: message\ndata: %s\n\n", raw)
		case "tools/call":
			calls.Add(1)
			respond(w, 200, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"content": []map[string]string{{"type": "text", "text": "found a record"}}}})
		default:
			t.Errorf("unexpected method %s", request.Method)
		}
	}))
	t.Cleanup(server.Close)
	return server, calls
}

func testedConnector(t *testing.T, s *Service, server *httptest.Server) Connector {
	t.Helper()
	headers := map[string]string{"Authorization": "Bearer fixture-secret"}
	c, err := s.saveConnector("", Connector{Name: "My MCP", URL: server.URL, Enabled: true}, &headers)
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.probe(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestToolApprovalPersistenceAndReplay(t *testing.T) {
	mcp, calls := mcpForTest(t)
	var modelCalls int
	backend := backendFunc(func(_ context.Context, messages []agent.Message, tools []agent.ToolSpec) (agent.Message, error) {
		modelCalls++
		if modelCalls == 1 {
			if len(tools) != 1 {
				t.Fatalf("tools = %d", len(tools))
			}
			return agent.Message{Role: "assistant", ToolCalls: []agent.ToolCall{{ID: "call1", Type: "function", Function: agent.FunctionCall{Name: tools[0].Name, Arguments: `{"query":"example"}`}}}}, nil
		}
		if messages[len(messages)-1].Role != "tool" || !strings.Contains(messages[len(messages)-1].Content, "found a record") {
			t.Fatal("missing tool receipt")
		}
		return agent.Message{Role: "assistant", Content: "找到了记录。"}, nil
	})
	s := serviceForTest(t, backend)
	connector := testedConnector(t, s, mcp)
	if !connector.HasHeaders || connector.Headers != nil || len(connector.Tools) != 1 {
		t.Fatalf("public connector = %+v", connector)
	}
	c, err := s.createConversation()
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.send(context.Background(), c.ID, c.Version, "查询记录", []string{connector.ID})
	if err != nil {
		t.Fatal(err)
	}
	if c.Pending == nil || calls.Load() != 0 {
		t.Fatal("tool executed before approval")
	}
	info, err := os.Stat(s.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("state file must be private")
	}
	// A pending approval survives a restart, but no external call is replayed.
	restarted, err := NewService(s.path, backend)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := restarted.conversation(c.ID)
	if err != nil || loaded.Pending == nil || calls.Load() != 0 {
		t.Fatal("approval was not retained")
	}
	approval := *loaded.Pending
	version := loaded.Version
	loaded, err = restarted.decide(context.Background(), loaded.ID, version, approval.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != "idle" || calls.Load() != 1 || loaded.Pending != nil {
		t.Fatal("approval did not complete exactly once")
	}
	_, err = restarted.decide(context.Background(), loaded.ID, version, approval.ID, true)
	if !errors.Is(err, errConflict) || calls.Load() != 1 {
		t.Fatal("approval replay was allowed")
	}
}

func TestRejectAndMultiTurnContext(t *testing.T) {
	mcp, calls := mcpForTest(t)
	var count int
	backend := backendFunc(func(_ context.Context, messages []agent.Message, tools []agent.ToolSpec) (agent.Message, error) {
		count++
		if count == 1 {
			return agent.Message{Role: "assistant", ToolCalls: []agent.ToolCall{{ID: "c", Type: "function", Function: agent.FunctionCall{Name: tools[0].Name, Arguments: `{}`}}}}, nil
		}
		if count == 2 && !strings.Contains(messages[len(messages)-1].Content, "rejected_by_user") {
			t.Fatal("rejection not reported")
		}
		if count == 3 {
			if messages[len(messages)-1].Content != "继续聊天" || len(messages) < 6 {
				t.Fatal("history missing on follow-up")
			}
		}
		return agent.Message{Role: "assistant", Content: "收到。"}, nil
	})
	s := serviceForTest(t, backend)
	connector := testedConnector(t, s, mcp)
	c, _ := s.createConversation()
	c, err := s.send(context.Background(), c.ID, c.Version, "使用工具", []string{connector.ID})
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.decide(context.Background(), c.ID, c.Version, c.Pending.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("rejected call executed")
	}
	_, err = s.send(context.Background(), c.ID, c.Version, "继续聊天", nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentTurnAndRestartInterruption(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	s := serviceForTest(t, backendFunc(func(_ context.Context, _ []agent.Message, _ []agent.ToolSpec) (agent.Message, error) {
		close(entered)
		<-release
		return agent.Message{Role: "assistant", Content: "ok"}, nil
	}))
	c, _ := s.createConversation()
	done := make(chan error, 1)
	go func() { _, err := s.send(context.Background(), c.ID, c.Version, "hello", nil); done <- err }()
	<-entered
	_, err := s.send(context.Background(), c.ID, c.Version, "duplicate", nil)
	if !errors.Is(err, errBusy) {
		t.Fatal("concurrent request accepted")
	}
	restarted, err := NewService(s.path, nil)
	if err != nil {
		t.Fatal(err)
	}
	recovered, _ := restarted.conversation(c.ID)
	if recovered.Status != "failed" || recovered.Error == "" {
		t.Fatal("interrupted run not marked")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCredentialsValidationAndHTTPBoundaries(t *testing.T) {
	s := serviceForTest(t, nil)
	handler := NewHandler(s, HTTPOptions{APIKey: "dashboard-key"})
	request := func(method, path, body, key, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := request("GET", "/v1/dashboard/connectors", "", "", ""); got.Code != 401 {
		t.Fatal("unauthenticated access accepted")
	}
	if got := request("POST", "/v1/dashboard/conversations", "", "dashboard-key", "https://untrusted.example"); got.Code != 403 {
		t.Fatal("foreign origin accepted")
	}
	if got := request("POST", "/v1/dashboard/connectors", `{"name":"X","url":"http://localhost/mcp","enabled":true,"command":"shell"}`, "dashboard-key", ""); got.Code != 400 {
		t.Fatal("undeclared connector fields accepted")
	}
	w := request("POST", "/v1/dashboard/connectors", `{"name":"X","url":"http://localhost/mcp","enabled":true,"headers":{"Authorization":"Bearer hidden-token"}}`, "dashboard-key", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "hidden-token") {
		t.Fatalf("credential leak: %s", w.Body)
	}
	var connector Connector
	_ = json.Unmarshal(w.Body.Bytes(), &connector)
	updated, err := s.saveConnector(connector.ID, Connector{Name: "edited", URL: connector.URL, Enabled: false}, nil)
	if err != nil || !updated.HasHeaders {
		t.Fatal("editing dropped saved headers")
	}
	empty := map[string]string{}
	updated, err = s.saveConnector(connector.ID, Connector{Name: "edited", URL: connector.URL, Enabled: true}, &empty)
	if err != nil || updated.HasHeaders {
		t.Fatal("explicit credential removal failed")
	}
	for _, endpoint := range []string{"file:///tmp/test", "https://user:pass@example.com/mcp", "http://localhost/mcp?token=secret"} {
		if err := validateConnector(Connector{Name: "X", URL: endpoint}); err == nil {
			t.Fatalf("invalid endpoint accepted: %s", endpoint)
		}
	}
}

func TestVoiceProxyContracts(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer audio-secret" {
			t.Error("voice credential missing")
		}
		switch r.URL.Path {
		case "/v1/audio/transcriptions":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				return
			}
			defer r.MultipartForm.RemoveAll()
			if r.FormValue("model") != "stt-model" || r.FormValue("response_format") != "json" {
				t.Error("incorrect transcription fields")
			}
			file, _, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				return
			}
			defer file.Close()
			raw, _ := io.ReadAll(file)
			if string(raw) != "fixture-audio" {
				t.Error("audio not forwarded")
			}
			respond(w, 200, map[string]string{"text": "你好"})
		case "/v1/audio/speech":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["model"] != "tts-model" || body["voice"] != "my-voice" || body["input"] != "你好" || body["response_format"] != "mp3" {
				t.Errorf("bad speech payload: %+v", body)
			}
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("fixture-mp3"))
		default:
			t.Errorf("bad voice endpoint: %s", r.URL.Path)
		}
	}))
	defer upstream.Close()
	v := VoiceOptions{STT: AudioModel{upstream.URL + "/v1", "audio-secret", "stt-model"}, TTS: AudioModel{upstream.URL + "/v1", "audio-secret", "tts-model"}, Voice: "my-voice"}
	if err := v.Validate(); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(serviceForTest(t, nil), HTTPOptions{Voice: v})
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	part, _ := writer.CreateFormFile("file", "recording.webm")
	_, _ = part.Write([]byte("fixture-audio"))
	_ = writer.Close()
	req := httptest.NewRequest("POST", "/v1/dashboard/voice/transcriptions", &payload)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "你好") {
		t.Fatalf("transcription failed: %d %s", w.Code, w.Body)
	}
	req = httptest.NewRequest("POST", "/v1/dashboard/voice/speech", strings.NewReader(`{"text":"你好"}`))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 || w.Body.String() != "fixture-mp3" || w.Header().Get("Content-Type") != "audio/mpeg" {
		t.Fatal("audio reply not forwarded")
	}
	req = httptest.NewRequest("POST", "/v1/dashboard/voice/speech", strings.NewReader(`{"text":"你好","model":"override"}`))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatal("client could override model")
	}
}
