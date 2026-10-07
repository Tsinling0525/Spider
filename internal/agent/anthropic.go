package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// AnthropicBackend translates the shared chat/tool history to the Messages API.
// The runtime still owns execution and approval of every returned tool call.
type AnthropicBackend struct{ connection *CompatibleBackend }

func NewAnthropicBackend(baseURL, key, model string, client *http.Client) (*AnthropicBackend, error) {
	connection, err := NewCompatibleBackend(baseURL, key, model, client)
	if err != nil {
		return nil, err
	}
	connection.endpoint = strings.TrimRight(baseURL, "/") + "/messages"
	return &AnthropicBackend{connection}, nil
}

type anthropicBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

func (b *AnthropicBackend) Next(ctx context.Context, messages []Message, specs []ToolSpec) (Message, error) {
	type turn struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	}
	history := []turn{}
	system := []string{}
	for _, message := range messages {
		if message.Role == "system" {
			system = append(system, message.Content)
			continue
		}
		role := message.Role
		blocks := []anthropicBlock{}
		if role == "tool" {
			role = "user"
			blocks = append(blocks, anthropicBlock{Type: "tool_result", ToolUseID: message.ToolCallID, Content: message.Content})
		} else if role == "assistant" || role == "user" {
			if role == "assistant" && len(message.AnthropicContent) > 0 {
				// Thinking signatures and content blocks must round-trip unchanged.
				history = append(history, turn{Role: role, Content: message.AnthropicContent})
				continue
			}
			if message.Content != "" {
				blocks = append(blocks, anthropicBlock{Type: "text", Text: message.Content})
			}
			for _, call := range message.ToolCalls {
				var arguments map[string]any
				if json.Unmarshal([]byte(call.Function.Arguments), &arguments) != nil || arguments == nil {
					return Message{}, errors.New("invalid tool arguments in chat history")
				}
				blocks = append(blocks, anthropicBlock{Type: "tool_use", ID: call.ID, Name: call.Function.Name, Input: json.RawMessage(call.Function.Arguments)})
			}
		} else {
			return Message{}, errors.New("unsupported role in Claude history")
		}
		if len(blocks) == 0 {
			blocks = append(blocks, anthropicBlock{Type: "text", Text: " "})
		}
		history = append(history, turn{Role: role, Content: blocks})
	}
	request := map[string]any{"model": b.connection.model, "messages": history, "max_tokens": 8192}
	if len(system) > 0 {
		request["system"] = strings.Join(system, "\n\n")
	}
	if len(specs) > 0 {
		type tool struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
		}
		tools := make([]tool, 0, len(specs))
		for _, spec := range specs {
			tools = append(tools, tool{spec.Name, spec.Description, spec.Parameters})
		}
		request["tools"] = tools
		request["tool_choice"] = map[string]any{"type": "auto", "disable_parallel_tool_use": true}
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return Message{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.connection.endpoint, bytes.NewReader(raw))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", b.connection.key)
	req.Header.Set("anthropic-version", "2023-06-01")
	response, err := b.connection.client.Do(req)
	if err != nil {
		return Message{}, errors.New("Claude request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Message{}, fmt.Errorf("Claude returned HTTP %d", response.StatusCode)
	}
	raw, err = io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return Message{}, errors.New("Claude response exceeded size limit or could not be read")
	}
	var result struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		StopReason string          `json:"stop_reason"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Role != "assistant" || (result.StopReason != "end_turn" && result.StopReason != "tool_use") {
		return Message{}, errors.New("Claude response was invalid, incomplete or blocked")
	}
	var blocks []anthropicBlock
	if json.Unmarshal(result.Content, &blocks) != nil || len(blocks) == 0 {
		return Message{}, errors.New("invalid Claude content")
	}
	message := Message{Role: "assistant", AnthropicContent: result.Content}
	texts := []string{}
	for _, block := range blocks {
		switch block.Type {
		case "text":
			texts = append(texts, block.Text)
		case "tool_use":
			var input map[string]any
			if block.ID == "" || block.Name == "" || json.Unmarshal(block.Input, &input) != nil || input == nil {
				return Message{}, errors.New("invalid Claude tool call")
			}
			message.ToolCalls = append(message.ToolCalls, ToolCall{ID: block.ID, Type: "function", Function: FunctionCall{Name: block.Name, Arguments: string(block.Input)}})
		case "thinking", "redacted_thinking": // Opaque blocks are preserved above.
		default:
			return Message{}, errors.New("unsupported Claude response content")
		}
	}
	message.Content = strings.Join(texts, "\n")
	if message.Content == "" && len(message.ToolCalls) == 0 {
		return Message{}, errors.New("Claude returned no answer")
	}
	return message, nil
}
