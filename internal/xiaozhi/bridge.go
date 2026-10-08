// Package xiaozhi connects the official Xiaozhi MCP WebSocket endpoint to
// dashboardd. Planning, model credentials, conversation state and approvals
// remain in dashboardd; this adapter never invokes a connector itself.
package xiaozhi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

type Config struct {
	Endpoint     string
	DashboardURL string
	APIKey       string
	ChannelID    string
	ChannelName  string
	ConnectorIDs []string
	WaitTimeout  time.Duration
}

type Bridge struct {
	config Config
	client *http.Client
}

var identifier = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)

func New(config Config) (*Bridge, error) {
	if config.WaitTimeout < 0 || config.WaitTimeout > 15*time.Second {
		return nil, errors.New("response wait must be between 0 and 15 seconds")
	}
	u, err := url.Parse(config.Endpoint)
	if err != nil || u.Scheme != "wss" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("MCP_ENDPOINT must be a WSS endpoint without user info or fragment")
	}
	base, err := url.Parse(config.DashboardURL)
	if err != nil || base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		return nil, errors.New("SPIDER_DASHBOARD_BASE_URL must be an HTTP(S) origin")
	}
	ip := net.ParseIP(base.Hostname())
	loopback := base.Hostname() == "localhost" || ip != nil && ip.IsLoopback()
	if base.Scheme != "https" && !(base.Scheme == "http" && loopback) {
		return nil, errors.New("a remote dashboard requires HTTPS")
	}
	if !loopback && config.APIKey == "" {
		return nil, errors.New("a remote dashboard requires SPIDER_API_KEY")
	}
	if !identifier.MatchString(config.ChannelID) {
		return nil, errors.New("SPIDER_XIAOZHI_CHANNEL_ID must contain 1-80 letters, digits, underscores or hyphens")
	}
	if len(config.ConnectorIDs) > 128 {
		return nil, errors.New("too many selected connectors")
	}
	for _, id := range config.ConnectorIDs {
		if !identifier.MatchString(id) {
			return nil, errors.New("invalid selected connector ID")
		}
	}
	config.DashboardURL = strings.TrimRight(config.DashboardURL, "/")
	config.ConnectorIDs = append([]string{}, config.ConnectorIDs...)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &Bridge{config: config, client: &http.Client{Transport: transport, Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (b *Bridge) Run(ctx context.Context) error {
	backoff := time.Second
	for ctx.Err() == nil {
		// Never log the endpoint: its URL contains the user's private MCP token.
		connected := false
		started := time.Now()
		_ = b.connect(ctx, func() { connected = true; slog.Info("Xiaozhi MCP connected") })
		if ctx.Err() != nil {
			break
		}
		if connected && time.Since(started) >= 30*time.Second {
			backoff = time.Second
		}
		slog.Warn("Xiaozhi MCP disconnected; reconnecting", "retry_in", backoff.String())
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
		backoff = min(backoff*2, 30*time.Second)
	}
	return ctx.Err()
}

func (b *Bridge) connect(ctx context.Context, connected func()) error {
	endpoint, _ := url.Parse(b.config.Endpoint)
	config, err := websocket.NewConfig(b.config.Endpoint, "https://"+endpoint.Host)
	if err != nil {
		return errors.New("invalid MCP endpoint")
	}
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	ws, err := config.DialContext(dialCtx)
	cancel()
	if err != nil {
		return errors.New("MCP connection failed")
	}
	defer ws.Close()
	ws.MaxPayloadBytes = 32 << 10
	stop := context.AfterFunc(ctx, func() { _ = ws.Close() })
	defer stop()
	connected()
	initialized := false
	for {
		var raw []byte
		if err := websocket.Message.Receive(ws, &raw); err != nil {
			return errors.New("MCP receive failed")
		}
		response := b.handle(ctx, raw, &initialized)
		if response == nil {
			continue
		}
		_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := websocket.JSON.Send(ws, response); err != nil {
			return errors.New("MCP send failed")
		}
	}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func rpcError(id json.RawMessage, code int, message string) any {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}}
}

func strictJSON(raw []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}

func (b *Bridge) handle(ctx context.Context, raw []byte, initialized *bool) any {
	var req rpcRequest
	if json.Unmarshal(raw, &req) != nil {
		return rpcError(nil, -32700, "Invalid JSON")
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		return rpcError(req.ID, -32600, "Invalid request")
	}
	if len(req.ID) == 0 {
		// Notifications, including initialized/cancelled, never authorize tools.
		return nil
	}
	var id any
	if json.Unmarshal(req.ID, &id) != nil {
		return rpcError(nil, -32600, "Invalid request ID")
	}
	switch id.(type) {
	case string, float64:
	default:
		return rpcError(nil, -32600, "Invalid request ID")
	}
	var result any
	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if json.Unmarshal(req.Params, &params) != nil {
			return rpcError(req.ID, -32602, "Invalid initialize parameters")
		}
		version := params.ProtocolVersion
		switch version {
		case "2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25":
		default:
			version = "2024-11-05"
		}
		*initialized = true
		result = map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "spider-dashboard", "version": "1.0.0"},
			"instructions": "用户的任务交给 spider_chat。running 时用 spider_status 等待真实结果；不传 request_id 可同步设备和网页共用的当前会话。若返回 voice_confirmation，先逐字读出 summary 和 phrase，只有用户随后明确说出该确认短语或取消时才调用 spider_confirm，并原样传 utterance。不得自行确认或声称订单已成功。当前 DoorDash 只提供结账预览，不能付款。"}
	case "ping":
		result = map[string]any{}
	case "tools/list":
		if !*initialized {
			return rpcError(req.ID, -32000, "Initialize first")
		}
		result = map[string]any{"tools": tools()}
	case "tools/call":
		if !*initialized {
			return rpcError(req.ID, -32000, "Initialize first")
		}
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Meta      json.RawMessage `json:"_meta,omitempty"`
		}
		// MCP clients may attach transport metadata to the call envelope. It
		// cannot change the locally bound channel or grant tool permissions;
		// only the actual tool arguments below use strict decoding.
		if json.Unmarshal(req.Params, &params) != nil {
			return rpcError(req.ID, -32602, "Invalid tool parameters")
		}
		value, err := b.call(ctx, params.Name, params.Arguments)
		if err != nil {
			result = map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": err.Error()}}}
		} else {
			data, _ := json.Marshal(value)
			result = map[string]any{"content": []map[string]string{{"type": "text", "text": string(data)}}}
		}
	default:
		return rpcError(req.ID, -32601, "Method not found")
	}
	return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}
}

func tools() []map[string]any {
	id := map[string]any{"type": "string", "pattern": "^[a-zA-Z0-9_-]{1,80}$", "description": "为本次用户请求生成唯一编号；重试同一请求必须沿用该编号，不能换号重新提交。"}
	return []map[string]any{
		{"name": "spider_chat", "description": "把用户的完整原话交给 Spider，继续设备和网页的同一会话。短暂等待处理结果；running 时用 spider_status 查询，不能只说已转交。返回 voice_confirmation 时先读出摘要并等待用户明确确认。",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"request_id": id, "content": map[string]any{"type": "string", "minLength": 1, "maxLength": 8192}, "new_conversation": map[string]any{"type": "boolean", "default": false, "description": "仅在用户明确要求新话题时设为 true；默认延续对话。"}}, "required": []string{"request_id", "content"}, "additionalProperties": false},
			"annotations": map[string]bool{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true}},
		{"name": "spider_status", "description": "查询真实进度。省略 request_id 可同步当前共用会话及网页续聊的最新结果。running 时可继续查询；voice_confirmation 表示先读出摘要并等待用户确认，其他待确认操作才需要网页。不能声称已下单。",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"request_id": id}, "additionalProperties": false},
			"annotations": map[string]bool{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true}},
		{"name": "spider_confirm", "description": "仅在已读出当前 voice_confirmation 摘要且用户随后明确说出指定确认短语或取消时，提交用户的原话。不得替用户确认；好、嗯、继续、不确认等含糊表达不是授权。每次决定生成唯一 decision_id，同一决定重试沿用原编号。过期或变化的摘要必须重新读取并重新询问。此工具不能实际下单或付款。",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"decision_id": id, "confirmation_id": map[string]any{"type": "string", "description": "当前 voice_confirmation.id，必须原样使用"}, "utterance": map[string]any{"type": "string", "description": "用户听到摘要后实际说出的完整确认或取消原话"}}, "required": []string{"decision_id", "confirmation_id", "utterance"}, "additionalProperties": false},
			"annotations": map[string]bool{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true}},
	}
}

func (b *Bridge) call(ctx context.Context, name string, arguments json.RawMessage) (json.RawMessage, error) {
	var requestID string
	method, path := "GET", ""
	var body []byte
	switch name {
	case "spider_chat":
		var args struct {
			RequestID       string `json:"request_id"`
			Content         string `json:"content"`
			NewConversation bool   `json:"new_conversation"`
		}
		if strictJSON(arguments, &args) != nil || !identifier.MatchString(args.RequestID) || strings.TrimSpace(args.Content) == "" || len(args.Content) > 8192 {
			return nil, errors.New("请求参数无效。")
		}
		requestID = args.RequestID
		method, path = "POST", "/v1/dashboard/channels/xiaozhi/requests"
		body, _ = json.Marshal(map[string]any{"channel_id": b.config.ChannelID, "channel_name": b.config.ChannelName, "request_id": args.RequestID, "content": args.Content, "connector_ids": b.config.ConnectorIDs, "new_conversation": args.NewConversation})
	case "spider_status":
		var args struct {
			RequestID string `json:"request_id"`
		}
		if strictJSON(arguments, &args) != nil || (args.RequestID != "" && !identifier.MatchString(args.RequestID)) {
			return nil, errors.New("请求编号无效。")
		}
		requestID = args.RequestID
		path = "/v1/dashboard/channels/xiaozhi/requests/" + b.config.ChannelID + "/" + requestID
		if requestID == "" {
			path = "/v1/dashboard/channels/xiaozhi/" + b.config.ChannelID + "/conversation"
		}
	case "spider_confirm":
		var args struct {
			DecisionID     string `json:"decision_id"`
			ConfirmationID string `json:"confirmation_id"`
			Utterance      string `json:"utterance"`
		}
		if strictJSON(arguments, &args) != nil || !identifier.MatchString(args.DecisionID) || !identifier.MatchString(args.ConfirmationID) || strings.TrimSpace(args.Utterance) == "" || len(args.Utterance) > 256 {
			return nil, errors.New("语音确认参数无效。")
		}
		method, path = "POST", "/v1/dashboard/channels/xiaozhi/decisions"
		body, _ = json.Marshal(map[string]any{"channel_id": b.config.ChannelID, "decision_id": args.DecisionID, "confirmation_id": args.ConfirmationID, "utterance": args.Utterance})
	default:
		return nil, errors.New("这个工具不可用。")
	}
	data, err := b.requestJSON(ctx, method, path, body)
	if err != nil || b.config.WaitTimeout == 0 {
		return data, err
	}
	statusPath := "/v1/dashboard/channels/xiaozhi/" + b.config.ChannelID + "/conversation"
	if requestID != "" {
		statusPath = "/v1/dashboard/channels/xiaozhi/requests/" + b.config.ChannelID + "/" + requestID
	}
	waitCtx, cancel := context.WithTimeout(ctx, b.config.WaitTimeout)
	defer cancel()
	for {
		var status struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(data, &status) != nil || status.Status != "running" {
			return data, nil
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return data, nil
		case <-timer.C:
		}
		next, pollErr := b.requestJSON(waitCtx, "GET", statusPath, nil)
		if pollErr != nil {
			return data, nil
		}
		data = next
	}
}

func (b *Bridge) requestJSON(ctx context.Context, method, path string, body []byte) (json.RawMessage, error) {
	request, err := http.NewRequestWithContext(ctx, method, b.config.DashboardURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("无法建立 Spider 请求。")
	}
	request.Header.Set("Content-Type", "application/json")
	if b.config.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+b.config.APIKey)
	}
	response, err := b.client.Do(request)
	if err != nil {
		return nil, errors.New("Spider 请求结果未知。请用 spider_status 同步当前会话；重试同一任务或确认必须沿用原编号，不能重复提交。")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		switch response.StatusCode {
		case 409:
			return nil, errors.New("会话正在处理、等待确认，或确认摘要已变化/过期。请用 spider_status 同步当前会话并重新读出摘要，不要重复执行旧确认。")
		case 404:
			return nil, errors.New("未找到该请求；请检查编号和 dashboardd 版本。")
		case 401, 403:
			return nil, errors.New("Spider 认证失败，请检查桥接服务配置。")
		default:
			return nil, errors.New("Spider 未接受请求，请检查配置和网页状态；不要自动重试可能已提交的操作。")
		}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(data) > 64<<10 || !json.Valid(data) {
		return nil, errors.New("Spider 回复无效；请查询原请求并检查网页。")
	}
	return json.RawMessage(data), nil
}
