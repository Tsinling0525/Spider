package dashboard

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// A session lasts for one probe or call. Both JSON and SSE POST responses are
// supported; the deprecated GET /sse transport and stdio are intentionally absent.
type mcpSession struct {
	connector Connector
	client    *http.Client
	id        string
	protocol  string
	nextID    int
}

func newMCPSession(ctx context.Context, connector Connector) (*mcpSession, error) {
	return newMCPSessionWithClient(ctx, connector, connectorHTTPClient())
}

func newMCPSessionWithClient(ctx context.Context, connector Connector, client *http.Client) (*mcpSession, error) {
	s := &mcpSession{connector: connector, protocol: "2025-11-25", client: client}
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := s.rpc(ctx, "initialize", map[string]any{
		"protocolVersion": s.protocol, "capabilities": map[string]any{},
		"clientInfo": map[string]string{"name": "spider-dashboard", "version": "0.1.0"},
	}, &initialized); err != nil {
		return nil, err
	}
	switch initialized.ProtocolVersion {
	case "2025-03-26", "2025-06-18", "2025-11-25":
		s.protocol = initialized.ProtocolVersion
	default:
		s.close()
		return nil, errors.New("MCP server did not negotiate a supported 2025 protocol version")
	}
	if err := s.rpc(ctx, "notifications/initialized", nil, nil); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

func (s *mcpSession) rpc(ctx context.Context, method string, params any, result any) error {
	payload := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		payload["params"] = params
	}
	if result != nil {
		s.nextID++
		payload["id"] = s.nextID
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.connector.URL, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid MCP endpoint")
	}
	for key, value := range s.connector.Headers {
		req.Header.Set(key, value)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if method != "initialize" {
		req.Header.Set("MCP-Protocol-Version", s.protocol)
	}
	if s.id != "" {
		req.Header.Set("Mcp-Session-Id", s.id)
	}
	response, err := s.client.Do(req)
	if err != nil {
		return errors.New("MCP connection failed; check the endpoint and network")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("MCP returned HTTP %d", response.StatusCode)
	}
	if method == "initialize" {
		s.id = response.Header.Get("Mcp-Session-Id")
	}
	if result == nil {
		return nil
	}
	decode := func(raw []byte) (bool, error) {
		var envelope struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return false, errors.New("invalid MCP response")
		}
		if string(envelope.ID) != fmt.Sprint(s.nextID) {
			return false, nil
		}
		if envelope.Error != nil {
			return false, fmt.Errorf("MCP JSON-RPC error %d", envelope.Error.Code)
		}
		if len(envelope.Result) == 0 {
			return false, errors.New("MCP response is missing a result")
		}
		return true, json.Unmarshal(envelope.Result, result)
	}
	const limit = 1 << 20
	if strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		scanner := bufio.NewScanner(io.LimitReader(response.Body, limit+1))
		scanner.Buffer(make([]byte, 4096), limit)
		var data []string
		consume := func() (bool, error) {
			if len(data) == 0 {
				return false, nil
			}
			raw := strings.Join(data, "\n")
			data = nil
			if strings.TrimSpace(raw) == "" {
				return false, nil
			}
			return decode([]byte(raw))
		}
		for scanner.Scan() {
			line := strings.TrimSuffix(scanner.Text(), "\r")
			if line == "" {
				if done, err := consume(); done || err != nil {
					return err
				}
			}
			if strings.HasPrefix(line, "data:") {
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
		}
		if scanner.Err() != nil {
			return errors.New("MCP stream exceeded its limit or was interrupted")
		}
		if done, err := consume(); done || err != nil {
			return err
		}
		return errors.New("MCP stream ended without a result")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || len(raw) > limit {
		return errors.New("MCP response exceeded its limit or was interrupted")
	}
	done, err := decode(raw)
	if err != nil {
		return err
	}
	if !done {
		return errors.New("MCP response id mismatch")
	}
	return nil
}

func (s *mcpSession) close() {
	if s.id == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.connector.URL, nil)
	if err != nil {
		return
	}
	for key, value := range s.connector.Headers {
		req.Header.Set(key, value)
	}
	req.Header.Set("Mcp-Session-Id", s.id)
	req.Header.Set("MCP-Protocol-Version", s.protocol)
	if response, err := s.client.Do(req); err == nil {
		response.Body.Close()
	}
}

func probeMCP(ctx context.Context, connector Connector) ([]MCPTool, error) {
	return probeMCPWithClient(ctx, connector, connectorHTTPClient())
}
func probeMCPWithClient(ctx context.Context, connector Connector, client *http.Client) ([]MCPTool, error) {
	session, err := newMCPSessionWithClient(ctx, connector, client)
	if err != nil {
		return nil, err
	}
	defer session.close()
	tools := make([]MCPTool, 0)
	cursor := ""
	for page := 0; page < 10; page++ {
		params := map[string]string{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var result struct {
			Tools []MCPTool `json:"tools"`
			Next  string    `json:"nextCursor"`
		}
		if err := session.rpc(ctx, "tools/list", params, &result); err != nil {
			return nil, err
		}
		tools = append(tools, result.Tools...)
		if len(tools) > 128 {
			return nil, errors.New("MCP exposes more than 128 tools")
		}
		if result.Next == "" {
			seen := map[string]bool{}
			for _, tool := range tools {
				var schema map[string]any
				if tool.Name == "" || seen[tool.Name] || json.Unmarshal(tool.InputSchema, &schema) != nil || schema["type"] != "object" {
					return nil, errors.New("invalid or duplicate MCP tool definition")
				}
				seen[tool.Name] = true
			}
			return tools, nil
		}
		if cursor == result.Next {
			return nil, errors.New("MCP repeated its pagination cursor")
		}
		cursor = result.Next
	}
	return nil, errors.New("MCP tool pagination exceeded its limit")
}

func callMCP(ctx context.Context, connector Connector, tool string, arguments json.RawMessage) (string, error) {
	return callMCPWithClient(ctx, connector, tool, arguments, connectorHTTPClient())
}
func callMCPWithClient(ctx context.Context, connector Connector, tool string, arguments json.RawMessage, client *http.Client) (string, error) {
	session, err := newMCPSessionWithClient(ctx, connector, client)
	if err != nil {
		return "", err
	}
	defer session.close()
	var result json.RawMessage
	if err := session.rpc(ctx, "tools/call", map[string]any{"name": tool, "arguments": arguments}, &result); err != nil {
		return "", err
	}
	if len(result) > 64<<10 {
		return "", errors.New("MCP tool result exceeded 64 KiB")
	}
	return string(result), nil
}
