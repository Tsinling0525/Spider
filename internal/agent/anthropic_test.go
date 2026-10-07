package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnthropicNativeToolRoundTrip(t *testing.T) {
	var count int
	original := `[{"type":"thinking","thinking":"opaque reasoning","signature":"signature"},{"type":"text","text":"I'll look it up."},{"type":"tool_use","id":"call1","name":"lookup","input":{"query":"example"}}]`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "fixture-key" || r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("Authorization") != "" {
			t.Error("invalid native Claude connection")
		}
		var body struct {
			System    string `json:"system"`
			MaxTokens int    `json:"max_tokens"`
			Messages  []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
			Tools []struct {
				Name   string          `json:"name"`
				Schema json.RawMessage `json:"input_schema"`
			} `json:"tools"`
			ToolChoice struct {
				Type            string `json:"type"`
				DisableParallel bool   `json:"disable_parallel_tool_use"`
			} `json:"tool_choice"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.System != "system" || body.MaxTokens != 8192 || len(body.Tools) != 1 || body.Tools[0].Name != "lookup" || body.ToolChoice.Type != "auto" || !body.ToolChoice.DisableParallel {
			t.Error("Claude request conversion failed")
		}
		for _, turn := range body.Messages {
			if turn.Role == "system" || turn.Role == "tool" {
				t.Error("OpenAI role leaked into Claude history")
			}
		}
		if count == 1 {
			_, _ = w.Write([]byte(`{"role":"assistant","stop_reason":"tool_use","content":` + original + `}`))
			return
		}
		if len(body.Messages) != 3 || string(body.Messages[1].Content) != original || body.Messages[2].Role != "user" || !strings.Contains(string(body.Messages[2].Content), `"tool_use_id":"call1"`) || !strings.Contains(string(body.Messages[2].Content), `"content":"receipt"`) {
			t.Errorf("native content/tool receipt lost: %+v", body.Messages)
		}
		_, _ = w.Write([]byte(`{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"Done."}]}`))
	}))
	defer upstream.Close()
	backend, err := NewAnthropicBackend(upstream.URL+"/v1", "fixture-key", "claude", nil)
	if err != nil {
		t.Fatal(err)
	}
	history := []Message{{Role: "system", Content: "system"}, {Role: "user", Content: "lookup"}}
	specs := []ToolSpec{{Name: "lookup", Description: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}}
	answer, err := backend.Next(context.Background(), history, specs)
	if err != nil || len(answer.ToolCalls) != 1 || answer.ToolCalls[0].Function.Arguments != `{"query":"example"}` {
		t.Fatalf("native tool not translated: %+v %v", answer, err)
	}
	history = append(history, answer, Message{Role: "tool", ToolCallID: "call1", Content: "receipt"})
	answer, err = backend.Next(context.Background(), history, specs)
	if err != nil || answer.Content != "Done." || count != 2 {
		t.Fatalf("Claude continuation failed: %+v %v", answer, err)
	}
}

func TestAnthropicRejectsErrorsTruncationAndRedirects(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("Claude redirect followed") }))
	defer destination.Close()
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"credential", 401, "fixture-key"},
		{"redirect", 307, "fixture-key"},
		{"truncated", 200, `{"role":"assistant","stop_reason":"max_tokens","content":[{"type":"text","text":"Partial"}]}`},
		{"invalid_tool", 200, `{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":"call","name":"lookup","input":null}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", destination.URL)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer upstream.Close()
			backend, _ := NewAnthropicBackend(upstream.URL, "fixture-key", "claude", nil)
			_, err := backend.Next(context.Background(), []Message{{Role: "user", Content: "hello"}}, nil)
			if err == nil || strings.Contains(err.Error(), "fixture-key") {
				t.Fatal("invalid response accepted or leaked key")
			}
		})
	}
}

func TestCompatibleReasoningPassThroughDoesNotLeakNativeContent(t *testing.T) {
	reasoning := "provider reasoning"
	for _, pass := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard", true: "reasoning"}[pass], func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Messages []map[string]json.RawMessage `json:"messages"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid body")
					return
				}
				if _, present := body.Messages[0]["reasoning_content"]; present != pass {
					t.Error("reasoning protocol not respected")
				}
				if _, present := body.Messages[0]["anthropic_content"]; present {
					t.Error("Claude content leaked into compatible API")
				}
				_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK","reasoning_content":"response reasoning"}}]}`))
			}))
			defer upstream.Close()
			backend, _ := NewCompatibleBackend(upstream.URL, "", "model", nil, CompatibleOptions{PassReasoning: pass})
			history := []Message{{Role: "assistant", Content: "hello", ReasoningContent: &reasoning, AnthropicContent: json.RawMessage(`[{"type":"text","text":"native"}]`)}}
			result, err := backend.Next(context.Background(), history, nil)
			if err != nil || result.ReasoningContent == nil || *result.ReasoningContent != "response reasoning" || history[0].AnthropicContent == nil || history[0].ReasoningContent == nil {
				t.Fatal("reasoning lost or stored history mutated")
			}
		})
	}
}
