// Package dashboard owns the personal chat surface and its connectors.
package dashboard

import (
	"encoding/json"
	"time"

	"github.com/Tsinling0525/Spider/internal/agent"
)

type Connector struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	URL            string            `json:"url"`
	Description    string            `json:"description"`
	Enabled        bool              `json:"enabled"`
	Headers        map[string]string `json:"headers,omitempty"`
	HasHeaders     bool              `json:"has_headers"`
	Tools          []MCPTool         `json:"tools"`
	CheckedAt      *time.Time        `json:"checked_at,omitempty"`
	Kind           string            `json:"kind,omitempty"`
	Credentials    map[string]string `json:"credentials,omitempty"`
	HasCredentials bool              `json:"has_credentials,omitempty"`
}

type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type Pending struct {
	ID            string          `json:"id"`
	CallID        string          `json:"call_id"`
	ConnectorID   string          `json:"connector_id"`
	ConnectorName string          `json:"connector_name"`
	URL           string          `json:"url"`
	Tool          string          `json:"tool"`
	Arguments     json.RawMessage `json:"arguments"`
}

type Conversation struct {
	ID           string          `json:"id"`
	Title        string          `json:"title"`
	Messages     []agent.Message `json:"messages"`
	ConnectorIDs []string        `json:"connector_ids"`
	Pending      *Pending        `json:"pending,omitempty"`
	Status       string          `json:"status"`
	Error        string          `json:"error,omitempty"`
	MemoryError  string          `json:"memory_error,omitempty"`
	Version      int64           `json:"version"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// Credentials and immutable approval targets are persisted only on the server.
type diskState struct {
	Connectors      map[string]Connector               `json:"connectors"`
	Conversations   map[string]Conversation            `json:"conversations"`
	ApprovalTargets map[string]Connector               `json:"approval_targets"`
	ModelProviders  map[string]ModelProvider           `json:"model_providers,omitempty"`
	ActiveModels    map[ModelCapability]ModelSelection `json:"active_models,omitempty"`
	ConnectorOAuth  map[string]connectorOAuthFlow      `json:"connector_oauth,omitempty"`
	MemoryConfig    *MemoryConfig                      `json:"memory_config,omitempty"`
}

type ModelCapability string

type ModelGroup string

const (
	ChatModels  ModelGroup = "chat"
	VoiceModels ModelGroup = "voice"
)

const (
	ModelChat ModelCapability = "chat"
	ModelSTT  ModelCapability = "stt"
	ModelTTS  ModelCapability = "tts"
)

type ModelDefinition struct {
	ID         string          `json:"id"`
	Label      string          `json:"label"`
	Capability ModelCapability `json:"capability"`
	Enabled    bool            `json:"enabled"`
	Voice      string          `json:"voice,omitempty"`
}

type ModelProvider struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	BaseURL   string            `json:"base_url"`
	APIKey    string            `json:"api_key,omitempty"`
	HasAPIKey bool              `json:"has_api_key"`
	Enabled   bool              `json:"enabled"`
	Note      string            `json:"note"`
	Models    []ModelDefinition `json:"models"`
	Protocol  string            `json:"protocol,omitempty"`
	PresetID  string            `json:"preset_id,omitempty"`
	Group     ModelGroup        `json:"group,omitempty"`
}

type ModelProviderInput struct {
	Name     string            `json:"name"`
	BaseURL  string            `json:"base_url"`
	APIKey   *string           `json:"api_key,omitempty"`
	Enabled  bool              `json:"enabled"`
	Note     string            `json:"note"`
	Models   []ModelDefinition `json:"models"`
	Protocol string            `json:"protocol,omitempty"`
	PresetID *string           `json:"preset_id,omitempty"`
	Group    ModelGroup        `json:"group,omitempty"`
}

type ModelSelection struct {
	ProviderID string `json:"provider_id"`
	ModelID    string `json:"model_id"`
}

type ModelConfiguration struct {
	Providers []ModelProvider                    `json:"providers"`
	Active    map[ModelCapability]ModelSelection `json:"active"`
}

type ModelTestResult struct {
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
	LatencyMS int64  `json:"latency_ms"`
}
