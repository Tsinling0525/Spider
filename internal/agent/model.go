// Package agent owns autonomous execution independently of any workflow vendor.
package agent

import (
	"context"
	"encoding/json"
	"time"
)

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type Message struct {
	Role             string          `json:"role"`
	Content          string          `json:"content"`
	Name             string          `json:"name,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	ToolCalls        []ToolCall      `json:"tool_calls,omitempty"`
	ReasoningContent *string         `json:"reasoning_content,omitempty"`
	AnthropicContent json.RawMessage `json:"anthropic_content,omitempty"`
}

type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Backend can be replaced without changing tools, approvals or stored runs.
type Backend interface {
	Next(context.Context, []Message, []ToolSpec) (Message, error)
}

type ToolContext struct {
	RunID    string
	Messages []Message
}

type Action struct {
	Arguments json.RawMessage `json:"arguments"`
	Preview   json.RawMessage `json:"preview"`
}

type Outcome struct {
	Content string
	Action  *Action
}

type Tool interface {
	Spec() ToolSpec
	Call(context.Context, ToolContext, json.RawMessage) (Outcome, error)
}

// Only the runtime may invoke this method, after an authenticated decision.
type ApprovedTool interface {
	Tool
	ExecuteApproved(context.Context, json.RawMessage) (string, error)
}

type Approval struct {
	ID         string          `json:"id"`
	ToolCallID string          `json:"tool_call_id"`
	ToolName   string          `json:"tool_name"`
	Arguments  json.RawMessage `json:"arguments"`
	Preview    json.RawMessage `json:"preview"`
}

type Event struct {
	Type  string    `json:"type"`
	Actor string    `json:"actor"`
	At    time.Time `json:"at"`
}

type Run struct {
	ID        string    `json:"id"`
	Goal      string    `json:"goal"`
	Principal string    `json:"principal"`
	Status    string    `json:"status"`
	Version   int64     `json:"version"`
	Steps     int       `json:"steps"`
	Messages  []Message `json:"messages"`
	Pending   *Approval `json:"pending_approval,omitempty"`
	Result    string    `json:"result,omitempty"`
	Error     string    `json:"error,omitempty"`
	Events    []Event   `json:"events"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Decision struct {
	ApprovalID      string `json:"approval_id"`
	ExpectedVersion int64  `json:"expected_version"`
	Approve         bool   `json:"approve"`
}
