package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CompatibleBackend uses the Chat Completions tool-calling protocol. A
// server-configured compatible provider can be used without a workflow engine.
type CompatibleBackend struct {
	endpoint      string
	key           string
	model         string
	client        *http.Client
	passReasoning bool
}

type CompatibleOptions struct{ PassReasoning bool }

func NewCompatibleBackend(baseURL, key, model string, client *http.Client, options ...CompatibleOptions) (*CompatibleBackend, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("AGENT_MODEL_BASE_URL must be an HTTP(S) API base URL without credentials, query or fragment")
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("AGENT_MODEL is required")
	}
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	// Never forward the provider key through redirects.
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	backend := &CompatibleBackend{endpoint: strings.TrimRight(baseURL, "/") + "/chat/completions", key: key, model: model, client: &copy}
	if len(options) > 0 {
		backend.passReasoning = options[0].PassReasoning
	}
	return backend, nil
}

func (b *CompatibleBackend) Next(ctx context.Context, messages []Message, specs []ToolSpec) (Message, error) {
	type tool struct {
		Type     string   `json:"type"`
		Function ToolSpec `json:"function"`
	}
	tools := make([]tool, 0, len(specs))
	for _, spec := range specs {
		tools = append(tools, tool{Type: "function", Function: spec})
	}
	// Preserve provider-specific reasoning when needed for tool continuation,
	// while keeping native Claude blocks out of Chat Completions requests.
	history := append([]Message{}, messages...)
	for i := range history {
		history[i].AnthropicContent = nil
		if !b.passReasoning {
			history[i].ReasoningContent = nil
		}
	}
	request := map[string]any{"model": b.model, "messages": history}
	if len(tools) > 0 {
		request["tools"] = tools
		request["tool_choice"] = "auto"
		request["parallel_tool_calls"] = false
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return Message{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.endpoint, bytes.NewReader(payload))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if b.key != "" {
		req.Header.Set("Authorization", "Bearer "+b.key)
	}
	response, err := b.client.Do(req)
	if err != nil {
		return Message{}, fmt.Errorf("model request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		// Do not persist upstream bodies: providers can echo credentials.
		return Message{}, fmt.Errorf("model returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return Message{}, err
	}
	if len(body) > 1<<20 {
		return Message{}, errors.New("model response exceeded size limit")
	}
	var result struct {
		Choices []struct {
			Message      Message `json:"message"`
			FinishReason string  `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &result); err != nil || len(result.Choices) != 1 {
		return Message{}, errors.New("invalid model response")
	}
	choice := result.Choices[0]
	if choice.FinishReason != "stop" && choice.FinishReason != "tool_calls" {
		return Message{}, errors.New("model response was incomplete or blocked")
	}
	return choice.Message, nil
}
