package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tsinling0525/Spider/internal/agent"
)

func channelInputForTest() channelInput {
	return channelInput{ChannelID: "my-xiaozhi", RequestID: "utterance-1", Content: "帮我找一份牛肉面", ChannelName: "书桌小智", ConnectorIDs: []string{}}
}

func waitChannel(t *testing.T, s *Service, input channelInput, status string) channelReply {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r, err := s.channelStatus(input.ChannelID, input.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		if r.Status == status {
			return r
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("channel did not reach %s", status)
	return channelReply{}
}

func TestChannelAsyncDuplicateAndPersistence(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s := serviceForTest(t, backendFunc(func(ctx context.Context, _ []agent.Message, _ []agent.ToolSpec) (agent.Message, error) {
		calls.Add(1)
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return agent.Message{}, ctx.Err()
		}
		return agent.Message{Role: "assistant", Content: "我可以帮你查菜单。"}, nil
	}))
	t.Cleanup(s.StopChannels)
	input := channelInputForTest()
	first, err := s.submitChannel(input)
	if err != nil || first.Status != "running" {
		t.Fatalf("submit: %+v %v", first, err)
	}
	<-entered
	duplicate, err := s.submitChannel(input)
	if err != nil || duplicate.ConversationID != first.ConversationID || calls.Load() != 1 {
		t.Fatalf("duplicate executed: %+v %v", duplicate, err)
	}
	changed := input
	changed.Content = "点两份"
	if _, err := s.submitChannel(changed); !errors.Is(err, errConflict) {
		t.Fatalf("changed replay: %v", err)
	}
	c, _ := s.conversation(first.ConversationID)
	if c.Source == nil || c.Source.Kind != "xiaozhi" || c.Source.Name != "书桌小智" || c.Messages[0].Content != input.Content {
		t.Fatalf("source/message missing: %+v", c)
	}
	close(release)
	finished := waitChannel(t, s, input, "idle")
	if finished.Reply != "我可以帮你查菜单。" {
		t.Fatalf("reply: %+v", finished)
	}
	s.StopChannels()
	restarted, err := NewService(s.path, s.backend)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.StopChannels)
	replayed, err := restarted.submitChannel(input)
	if err != nil || replayed.Reply != finished.Reply || calls.Load() != 1 {
		t.Fatalf("restarted replay: %+v %v", replayed, err)
	}
}

func TestChannelApprovalIsBrowserOnly(t *testing.T) {
	mcp, calls := mcpForTest(t)
	var modelCalls atomic.Int32
	s := serviceForTest(t, backendFunc(func(_ context.Context, _ []agent.Message, tools []agent.ToolSpec) (agent.Message, error) {
		if modelCalls.Add(1) == 1 {
			return agent.Message{Role: "assistant", ToolCalls: []agent.ToolCall{{ID: "food-call", Type: "function", Function: agent.FunctionCall{Name: tools[0].Name, Arguments: `{"query":"noodles"}`}}}}, nil
		}
		return agent.Message{Role: "assistant", Content: "已查到菜单。"}, nil
	}))
	t.Cleanup(s.StopChannels)
	connector := testedConnector(t, s, mcp)
	input := channelInputForTest()
	input.ConnectorIDs = []string{connector.ID}
	first, err := s.submitChannel(input)
	if err != nil {
		t.Fatal(err)
	}
	pending := waitChannel(t, s, input, "waiting_approval")
	encoded, _ := json.Marshal(pending)
	if calls.Load() != 0 || strings.Contains(string(encoded), "fixture-secret") || strings.Contains(string(encoded), connector.URL) || strings.Contains(string(encoded), "approval_id") || strings.Contains(string(encoded), `"arguments"`) {
		t.Fatalf("executed or exposed private tool: %s", encoded)
	}
	next := input
	next.RequestID = "utterance-2"
	if _, err := s.submitChannel(next); !errors.Is(err, errConflict) {
		t.Fatalf("pending turn bypassed: %v", err)
	}
	c, _ := s.conversation(first.ConversationID)
	handler := NewHandler(s, HTTPOptions{APIKey: "dashboard-key"})
	body, _ := json.Marshal(map[string]any{"approval_id": c.Pending.ID, "expected_version": c.Version, "approve": true})
	req := httptest.NewRequest("POST", "/v1/dashboard/conversations/"+c.ID+"/decisions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dashboard-key")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 || calls.Load() != 1 {
		t.Fatalf("browser confirmation: %d %s", w.Code, w.Body.String())
	}
	result := waitChannel(t, s, input, "idle")
	if result.Reply != "已查到菜单。" {
		t.Fatalf("follow-up: %+v", result)
	}
	if _, err := s.submitChannel(next); err != nil {
		t.Fatal(err)
	}
	waitChannel(t, s, next, "idle")
	old, _ := s.channelStatus(input.ChannelID, input.RequestID)
	if old.Status != "superseded" || old.Reply != "已查到菜单。" {
		t.Fatalf("old request used new turn: %+v", old)
	}
}

func TestChannelInterruptNeverReplays(t *testing.T) {
	started := make(chan struct{})
	s := serviceForTest(t, backendFunc(func(ctx context.Context, _ []agent.Message, _ []agent.ToolSpec) (agent.Message, error) {
		close(started)
		<-ctx.Done()
		return agent.Message{}, ctx.Err()
	}))
	input := channelInputForTest()
	if _, err := s.submitChannel(input); err != nil {
		t.Fatal(err)
	}
	<-started
	// Copy the durable in-flight state, then cancel the original worker.
	s.mu.Lock()
	raw, _ := json.Marshal(s.state)
	s.mu.Unlock()
	s.StopChannels()
	restarted := serviceForTest(t, backendFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Message, error) {
		t.Error("interrupted request was replayed")
		return agent.Message{}, nil
	}))
	restarted.StopChannels()
	if err := os.WriteFile(restarted.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewService(restarted.path, restarted.backend)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(loaded.StopChannels)
	result, err := loaded.submitChannel(input)
	if err != nil || result.Status != "failed" {
		t.Fatalf("interrupted replay: %+v %v", result, err)
	}
}

func TestChannelHTTPValidationAndScope(t *testing.T) {
	s := serviceForTest(t, backendFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Message, error) {
		return agent.Message{Role: "assistant", Content: "你好"}, nil
	}))
	t.Cleanup(s.StopChannels)
	h := NewHandler(s, HTTPOptions{APIKey: "secret"})
	for _, test := range []struct {
		body, key string
		code      int
	}{
		{`{"channel_id":"x","request_id":"u","content":"hello"}`, "", 401},
		{`{"channel_id":"x","request_id":"u","content":"hello","approve":true}`, "secret", 400},
		{`{"channel_id":"../x","request_id":"u","content":"hello"}`, "secret", 400},
		{`{"channel_id":"x","request_id":"u","content":"hello","connector_ids":["missing"]}`, "secret", 400},
		{`{"channel_id":"x","request_id":"u","content":"hello"}`, "secret", 202},
	} {
		req := httptest.NewRequest("POST", "/v1/dashboard/channels/xiaozhi/requests", strings.NewReader(test.body))
		req.Header.Set("Authorization", "Bearer "+test.key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != test.code {
			t.Fatalf("%s: %d %s", test.body, w.Code, w.Body.String())
		}
	}
	if _, err := s.channelStatus("other-channel", "u"); !errors.Is(err, errNotFound) {
		t.Fatalf("channel escaped scope: %v", err)
	}
	newInput := channelInput{ChannelID: "x", RequestID: "second", Content: "新话题", NewConversation: true}
	old, _ := s.channelStatus("x", "u")
	created, err := s.submitChannel(newInput)
	if err != nil || old.ConversationID == created.ConversationID {
		t.Fatalf("new topic: %+v %v", created, err)
	}
}

func TestChannelPersistenceFailureDoesNotConsumeRequest(t *testing.T) {
	var calls atomic.Int32
	s := serviceForTest(t, backendFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Message, error) {
		calls.Add(1)
		return agent.Message{Role: "assistant", Content: "已收到"}, nil
	}))
	t.Cleanup(s.StopChannels)
	path := s.path
	s.path = filepath.Join(filepath.Dir(path), "is-a-directory")
	if err := os.Mkdir(s.path, 0700); err != nil {
		t.Fatal(err)
	}
	input := channelInputForTest()
	if _, err := s.submitChannel(input); err == nil {
		t.Fatal("accepted an unpersisted request")
	}
	if len(s.state.ChannelRequests) != 0 || len(s.state.ChannelSessions) != 0 || len(s.state.Conversations) != 0 || len(s.busy) != 0 || calls.Load() != 0 {
		t.Fatal("failed transaction left state or started work")
	}
	s.path = path
	if _, err := s.submitChannel(input); err != nil {
		t.Fatal(err)
	}
	waitChannel(t, s, input, "idle")
	if calls.Load() != 1 {
		t.Fatal("retry after a rejected submission did not execute once")
	}
}
