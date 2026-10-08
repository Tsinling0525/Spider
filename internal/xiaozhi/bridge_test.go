package xiaozhi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tsinling0525/Spider/internal/agent"
	"github.com/Tsinling0525/Spider/internal/dashboard"
	"golang.org/x/net/websocket"
)

func configForTest(base string) Config {
	return Config{Endpoint: "wss://api.xiaozhi.me/mcp/?token=private-endpoint-token", DashboardURL: base, APIKey: "dashboard-secret", ChannelID: "desk", ChannelName: "书桌小智", ConnectorIDs: []string{"food"}}
}

func TestConfigRequiresSecureEndpoints(t *testing.T) {
	for _, base := range []string{"http://remote.example", "https://user:pass@remote.example", "https://remote.example/?token=secret", "file:///tmp/state", "https://remote.example/path"} {
		if _, err := New(configForTest(base)); err == nil {
			t.Fatalf("accepted %s", base)
		}
	}
	config := configForTest("http://127.0.0.1:8083")
	config.Endpoint = "ws://remote.example/mcp"
	if _, err := New(config); err == nil {
		t.Fatal("accepted plaintext endpoint")
	}
	config = configForTest("https://remote.example")
	config.APIKey = ""
	if _, err := New(config); err == nil {
		t.Fatal("accepted unauthenticated remote dashboard")
	}
}

func TestToolsBindLocalChannelAndNeverApprove(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer dashboard-secret" {
			t.Error("missing dashboard authentication")
		}
		if r.Method == "POST" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["channel_id"] != "desk" || body["channel_name"] != "书桌小智" || body["connector_ids"].([]any)[0] != "food" {
				t.Errorf("incorrect channel binding: %#v", body)
			}
		}
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"request_id":"u1","status":"running","reply":"处理中"}`))
	}))
	defer server.Close()
	b, err := New(configForTest(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	initialized := true
	for _, args := range []string{
		`{"request_id":"u1","content":"noodles","approve":true}`,
		`{"request_id":"u1","content":"noodles","connector_ids":["other"]}`,
		`{"request_id":"u1","content":"noodles","channel_id":"victim"}`,
		`{"request_id":"../u1","content":"noodles"}`,
	} {
		raw := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"spider_chat","arguments":` + args + `}}`)
		response, _ := json.Marshal(b.handle(context.Background(), raw, &initialized))
		if !strings.Contains(string(response), `"isError":true`) {
			t.Fatalf("accepted unsafe args: %s", response)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid arguments reached dashboard")
	}
	if _, err := b.call(context.Background(), "spider_confirm", json.RawMessage(`{"approve":true}`)); err == nil {
		t.Fatal("approval tool exposed")
	}
	if _, err := b.call(context.Background(), "spider_chat", json.RawMessage(`{"request_id":"u1","content":"noodles"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.call(context.Background(), "spider_status", json.RawMessage(`{"request_id":"u1"}`)); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatal("request was automatically retried")
	}
	// Official cloud clients can add routing fields outside arguments. They
	// must not cause valid calls to fail or override the local binding.
	raw := []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"spider_chat","arguments":{"request_id":"u1","content":"noodles"},"session_id":"cloud-session","channel_id":"victim","connector_ids":["other"],"approve":true,"_meta":{"progressToken":1}}}`)
	response, _ := json.Marshal(b.handle(context.Background(), raw, &initialized))
	if strings.Contains(string(response), `"error"`) || strings.Contains(string(response), `"isError":true`) || requests.Load() != 3 {
		t.Fatalf("transport metadata blocked valid bound call: %s", response)
	}
}

func TestHTTPFailuresDoNotLeakSecretsOrRetry(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "token=upstream-secret Authorization: private", 502)
	}))
	defer server.Close()
	b, _ := New(configForTest(server.URL))
	_, err := b.call(context.Background(), "spider_chat", json.RawMessage(`{"request_id":"u1","content":"hello"}`))
	if err == nil || strings.Contains(err.Error(), "secret") || requests.Load() != 1 {
		t.Fatalf("unsafe error/retry: %v", err)
	}
}

func TestVoiceConfirmationBindingCurrentSyncAndBoundedWait(t *testing.T) {
	var posts, gets atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			var input map[string]any
			_ = json.NewDecoder(r.Body).Decode(&input)
			if input["channel_id"] != "desk" || input["approve"] != nil || input["connector_id"] != nil {
				t.Error("voice authority escaped local binding")
			}
			w.WriteHeader(202)
			_, _ = w.Write([]byte(`{"status":"running"}`))
			return
		}
		gets.Add(1)
		if strings.Contains(r.URL.Path, "/conversation") && r.URL.Path != "/v1/dashboard/channels/xiaozhi/desk/conversation" {
			t.Error("current sync escaped channel")
		}
		if gets.Load() == 1 {
			_, _ = w.Write([]byte(`{"status":"running"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"idle","reply":"网页与设备使用同一会话","messages":[{"role":"user","content":"确认加入购物车"}]}`))
	}))
	defer server.Close()
	config := configForTest(server.URL)
	config.WaitTimeout = time.Second
	b, _ := New(config)
	result, err := b.call(context.Background(), "spider_chat", json.RawMessage(`{"request_id":"voice-1","content":"查菜单"}`))
	if err != nil || !strings.Contains(string(result), `"idle"`) || posts.Load() != 1 || gets.Load() != 2 {
		t.Fatalf("bounded wait: %s %v", result, err)
	}
	if _, err := b.call(context.Background(), "spider_status", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	args := json.RawMessage(`{"decision_id":"confirm-1","confirmation_id":"nonce-1","utterance":"确认加入购物车"}`)
	if _, err := b.call(context.Background(), "spider_confirm", args); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 2 {
		t.Fatal("voice decision was retried")
	}
	if _, err := b.call(context.Background(), "spider_confirm", json.RawMessage(`{"decision_id":"confirm-1","confirmation_id":"nonce-1","utterance":"确认加入购物车","approve":true}`)); err == nil || posts.Load() != 2 {
		t.Fatal("injected approval accepted")
	}
}

type testBackend struct{ calls atomic.Int32 }

func (b *testBackend) Next(context.Context, []agent.Message, []agent.ToolSpec) (agent.Message, error) {
	b.calls.Add(1)
	return agent.Message{Role: "assistant", Content: "你的需求已到 Spider。"}, nil
}

func TestWebSocketToDashboardEndToEndAndReconnect(t *testing.T) {
	backend := &testBackend{}
	s, err := dashboard.NewService(filepath.Join(t.TempDir(), "state.json"), backend)
	if err != nil {
		t.Fatal(err)
	}
	defer s.StopChannels()
	d := httptest.NewServer(dashboard.NewHandler(s, dashboard.HTTPOptions{APIKey: "dashboard-secret"}))
	defer d.Close()
	config := configForTest(d.URL)
	config.ConnectorIDs = []string{}
	b, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	completed := make(chan error, 2)
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		call := func(id int, method string, params any) (map[string]any, error) {
			_ = ws.SetDeadline(time.Now().Add(3 * time.Second))
			if err := websocket.JSON.Send(ws, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
				return nil, err
			}
			var reply struct {
				ID     int            `json:"id"`
				Result map[string]any `json:"result"`
				Error  any            `json:"error"`
			}
			if err := websocket.JSON.Receive(ws, &reply); err != nil {
				return nil, err
			}
			if reply.ID != id || reply.Error != nil {
				return nil, errors.New("incorrect JSON-RPC response")
			}
			return reply.Result, nil
		}
		if _, err := call(1, "initialize", map[string]any{"protocolVersion": "2024-11-05", "clientInfo": map[string]any{"name": "xiaozhi", "version": "test"}}); err != nil {
			completed <- err
			return
		}
		listed, err := call(2, "tools/list", map[string]any{})
		if err != nil || len(listed["tools"].([]any)) != 3 {
			completed <- errors.New("tools discovery failed")
			return
		}
		// The same logical utterance is submitted again after a new WebSocket
		// session, reproducing loss of an upstream acknowledgement.
		result, err := call(3, "tools/call", map[string]any{"name": "spider_chat", "arguments": map[string]any{"request_id": "voice-1", "content": "请帮我点外卖"}})
		if err != nil || result["isError"] == true {
			completed <- errors.New("chat submission failed")
			return
		}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			result, err = call(4, "tools/call", map[string]any{"name": "spider_status", "arguments": map[string]any{"request_id": "voice-1"}})
			if err != nil {
				completed <- err
				return
			}
			text := result["content"].([]any)[0].(map[string]any)["text"].(string)
			var status struct{ Status, Reply string }
			_ = json.Unmarshal([]byte(text), &status)
			if status.Status == "idle" && status.Reply == "你的需求已到 Spider。" {
				completed <- nil
				return
			}
			time.Sleep(time.Millisecond)
		}
		completed <- errors.New("no assistant reply")
	}))
	defer server.Close()
	b.config.Endpoint = "ws" + strings.TrimPrefix(server.URL, "http") // Local fake endpoint; production New rejects WS.
	for i := 0; i < 2; i++ {
		_ = b.connect(context.Background(), func() {})
		if err := <-completed; err != nil {
			t.Fatal(err)
		}
	}
	if backend.calls.Load() != 1 {
		t.Fatalf("utterance replayed %d times", backend.calls.Load())
	}
	request, _ := http.NewRequest("GET", d.URL+"/v1/dashboard/conversations", nil)
	request.Header.Set("Authorization", "Bearer dashboard-secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body struct {
		Conversations []dashboard.Conversation `json:"conversations"`
	}
	_ = json.NewDecoder(response.Body).Decode(&body)
	if len(body.Conversations) != 1 || body.Conversations[0].Source == nil || body.Conversations[0].Source.Name != "书桌小智" {
		t.Fatalf("dashboard missing channel session: %+v", body)
	}
}
