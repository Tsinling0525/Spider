package dashboard

// Catalog, remote headers and gateway tools adapted from TencentCloud/Octop
// (MIT), commit 4b17f1cfac6c8092d8554b94632af30ce7f51966.
// See dashboard/THIRD_PARTY_NOTICES.md and dashboard/LICENSE-Octop.
import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

//go:embed octop/*
var octopAdapters embed.FS

type builtinConnectorInput struct {
	Kind        string            `json:"kind"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Enabled     bool              `json:"enabled"`
	Credentials map[string]string `json:"credentials,omitempty"`
}

var builtinEndpoints = map[string]string{
	"tencent-meeting": "https://mcp.meeting.tencent.com/mcp/wemeet-open/v1",
	"qq-mail":         "https://mail.qq.com/",
	"baidu-map":       "https://api.map.baidu.com/agent_plan/v1",
	"didi":            "https://mcp.didichuxing.com/mcp-servers",
	"notion":          "https://mcp.notion.com/mcp",
}

func connectorHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func copyCredentials(in map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range in {
		out[key] = value
	}
	return out
}

func prepareBuiltin(input builtinConnectorInput, previous Connector) (Connector, error) {
	endpoint, supported := builtinEndpoints[input.Kind]
	if !supported || (previous.ID != "" && previous.Kind != input.Kind) {
		return Connector{}, fmt.Errorf("%w: unsupported connector kind", errInvalid)
	}
	c := Connector{Kind: input.Kind, Name: strings.TrimSpace(input.Name), URL: endpoint, Description: strings.TrimSpace(input.Description), Enabled: input.Enabled, Credentials: copyCredentials(previous.Credentials)}
	allowed := map[string]bool{}
	switch input.Kind {
	case "tencent-meeting":
		allowed["token"] = true
	case "baidu-map", "didi":
		allowed["api_key"] = true
	case "notion":
		allowed["access_token"] = true
	case "qq-mail":
		for _, key := range []string{"email", "password", "mail_provider", "imap_host", "imap_port", "smtp_host", "smtp_port"} {
			allowed[key] = true
		}
	}
	for key, value := range input.Credentials {
		if !allowed[key] || len(value) > 8192 || strings.ContainsAny(value, "\r\n") {
			return Connector{}, errInvalid
		}
		value = strings.TrimSpace(value)
		if value != "" {
			c.Credentials[key] = value
		}
	}
	if input.Kind == "notion" && input.Credentials["access_token"] != "" {
		// A pasted access token must never retain another account's refresh grant.
		c.Credentials = map[string]string{"access_token": c.Credentials["access_token"]}
	}
	var required string
	switch input.Kind {
	case "tencent-meeting":
		required = "token"
	case "baidu-map", "didi":
		required = "api_key"
	case "notion":
		required = "access_token"
	case "qq-mail":
		required = "password"
		address, err := mail.ParseAddress(c.Credentials["email"])
		if err != nil || address.Address != c.Credentials["email"] {
			return Connector{}, fmt.Errorf("%w: 请填写有效的邮箱地址", errInvalid)
		}
		if err := resolveBuiltinMailServers(c.Credentials); err != nil {
			return Connector{}, err
		}
	}
	if c.Credentials[required] == "" {
		return Connector{}, fmt.Errorf("%w: 请填写连接器凭据，Notion 可使用一键授权", errInvalid)
	}
	if err := validateConnector(c); err != nil {
		return Connector{}, err
	}
	return c, nil
}

func resolveBuiltinMailServers(creds map[string]string) error {
	domain := strings.ToLower(strings.SplitN(creds["email"], "@", 2)[1])
	provider := strings.ToLower(creds["mail_provider"])
	if provider == "" {
		provider = "qq"
		if domain == "gmail.com" {
			provider = "gmail"
		}
		if domain == "163.com" || domain == "126.com" || domain == "yeah.net" {
			provider = "netease"
		}
	}
	creds["mail_provider"] = provider
	var host string
	switch {
	case domain == "163.com" || domain == "126.com" || domain == "yeah.net":
		host = domain
	case provider == "netease":
		host = "163.com"
	case provider == "qq":
		host = "qq.com"
	case provider == "gmail":
		host = "gmail.com"
	case provider == "custom" && (domain == "qq.com" || domain == "foxmail.com"):
		host = "qq.com"
	case provider == "custom" && domain == "gmail.com":
		host = "gmail.com"
	case provider == "custom":
		if creds["imap_host"] == "" || creds["smtp_host"] == "" {
			return fmt.Errorf("%w: 请填写 IMAP 和 SMTP 主机", errInvalid)
		}
	default:
		return fmt.Errorf("%w: 不支持的邮箱服务商", errInvalid)
	}
	if host != "" {
		creds["imap_host"] = "imap." + host
		creds["smtp_host"] = "smtp." + host
		creds["imap_port"] = "993"
		creds["smtp_port"] = "587"
	}
	for _, key := range []string{"imap_host", "smtp_host"} {
		if strings.ContainsAny(creds[key], " /:@?#\\") {
			return fmt.Errorf("%w: 邮箱主机应为域名或 IP", errInvalid)
		}
	}
	for key, fallback := range map[string]string{"imap_port": "993", "smtp_port": "587"} {
		if creds[key] == "" {
			creds[key] = fallback
		}
		port, err := strconv.Atoi(creds[key])
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("%w: 邮箱端口无效", errInvalid)
		}
	}
	return nil
}

func (s *Service) builtinDraft(id string, input builtinConnectorInput) (Connector, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, ok := s.state.Connectors[id]
	if id != "" && !ok {
		return Connector{}, errNotFound
	}
	c, err := prepareBuiltin(input, previous)
	c.ID = id
	return c, err
}

func (s *Service) saveBuiltin(id string, input builtinConnectorInput) (Connector, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, found := s.state.Connectors[id]
	if id != "" && !found {
		return Connector{}, errNotFound
	}
	c, err := prepareBuiltin(input, previous)
	if err != nil {
		return Connector{}, err
	}
	if id == "" {
		if len(s.state.Connectors) >= 64 {
			return Connector{}, errBusy
		}
		id = newID()
	}
	c.ID = id
	before, _ := json.Marshal(previous.Credentials)
	after, _ := json.Marshal(c.Credentials)
	if previous.Kind == c.Kind && bytes.Equal(before, after) {
		c.Tools = previous.Tools
		c.CheckedAt = previous.CheckedAt
	}
	s.state.Connectors[id] = c
	if err := s.persistLocked(); err != nil {
		if found {
			s.state.Connectors[id] = previous
		} else {
			delete(s.state.Connectors, id)
		}
		return Connector{}, err
	}
	return publicConnector(c), nil
}

// Secrets in vendor URLs are constructed only for an outbound request. The
// stored/public URL and the immutable approval display never contain them.
func builtinRemoteSpec(c Connector) Connector {
	c.Headers = map[string]string{}
	switch c.Kind {
	case "tencent-meeting":
		c.Headers["X-Tencent-Meeting-Token"] = c.Credentials["token"]
		c.Headers["X-Skill-Version"] = "v1.0.1"
	case "didi":
		c.URL += "?" + url.Values{"key": {c.Credentials["api_key"]}}.Encode()
	case "notion":
		c.Headers["Authorization"] = "Bearer " + c.Credentials["access_token"]
		c.Headers["User-Agent"] = "octop-connector/0.1"
	}
	return c
}

func gatewayTools(kind string) []MCPTool {
	name := "baidu_map"
	if kind == "qq-mail" {
		name = "qq_mail"
	}
	raw, _ := octopAdapters.ReadFile("octop/" + name + "_tools.json")
	var tools []MCPTool
	_ = json.Unmarshal(raw, &tools)
	return tools
}

func probeBuiltin(ctx context.Context, c Connector, client *http.Client) ([]MCPTool, error) {
	switch c.Kind {
	case "qq-mail":
		if _, err := mailGateway(ctx, c, "probe", nil); err != nil {
			return nil, err
		}
		return gatewayTools(c.Kind), nil
	case "baidu-map":
		if _, err := baiduMapRequest(ctx, c, "get_weather", map[string]string{"region": "北京"}, client); err != nil {
			return nil, err
		}
		return gatewayTools(c.Kind), nil
	case "":
		return probeMCPWithClient(ctx, c, client)
	default:
		return probeMCPWithClient(ctx, builtinRemoteSpec(c), client)
	}
}

func callBuiltin(ctx context.Context, c Connector, tool string, args json.RawMessage, client *http.Client) (string, error) {
	switch c.Kind {
	case "qq-mail":
		return mailGateway(ctx, c, tool, args)
	case "baidu-map":
		var params map[string]string
		if json.Unmarshal(args, &params) != nil {
			return "", errInvalid
		}
		text, err := baiduMapRequest(ctx, c, tool, params, client)
		if err != nil {
			return "", err
		}
		return gatewayResult(text)
	case "":
		return callMCPWithClient(ctx, c, tool, args, client)
	default:
		return callMCPWithClient(ctx, builtinRemoteSpec(c), tool, args, client)
	}
}

func gatewayResult(text string) (string, error) {
	if len(text) > 60<<10 {
		return "", errors.New("连接器工具结果超过 60 KiB")
	}
	raw, _ := json.Marshal(map[string]any{"content": []map[string]string{{"type": "text", "text": text}}})
	return string(raw), nil
}

func baiduMapRequest(ctx context.Context, c Connector, tool string, args map[string]string, client *http.Client) (string, error) {
	params := url.Values{}
	path := ""
	require := func(key string) error {
		if strings.TrimSpace(args[key]) == "" {
			return fmt.Errorf("%w: %s is required", errInvalid, key)
		}
		return nil
	}
	switch tool {
	case "search_place":
		if err := require("query"); err != nil {
			return "", err
		}
		if err := require("region"); err != nil {
			return "", err
		}
		path = "/place"
		params.Set("user_raw_request", args["query"])
		params.Set("region", args["region"])
	case "plan_direction":
		if err := require("query"); err != nil {
			return "", err
		}
		path = "/direction"
		params.Set("user_raw_request", args["query"])
	case "get_weather":
		if err := require("region"); err != nil {
			return "", err
		}
		path = "/weather"
		params.Set("region", args["region"])
	default:
		return "", errInvalid
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, builtinEndpoints["baidu-map"]+path+"?"+params.Encode(), nil)
	req.Header.Set("Authorization", "Bearer "+c.Credentials["api_key"])
	req.Header.Set("User-Agent", "octop-connector/0.1")
	response, err := client.Do(req)
	if err != nil {
		return "", errors.New("百度地图连接失败，请检查网络")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", fmt.Errorf("百度地图返回 HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (60<<10)+1))
	if err != nil || len(raw) > 60<<10 {
		return "", errors.New("百度地图响应过大或中断")
	}
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return "", errors.New("百度地图返回无效 JSON")
	}
	status := fmt.Sprint(payload["status"])
	message, _ := payload["message"].(string)
	if status == "102" || strings.Contains(message, "token失效") || strings.Contains(strings.ToLower(message), "auth token") {
		return "", errors.New("百度地图 Token 无效，请重新配置")
	}
	if _, ok := payload["result"]; ok {
		return string(raw), nil
	}
	if _, ok := payload["results"]; ok {
		return string(raw), nil
	}
	if status == "0" || strings.EqualFold(message, "ok") {
		return string(raw), nil
	}
	return "", errors.New("百度地图接口请求失败，请检查 Token 和参数")
}

// Reuse Octop's unmodified Python standard-library IMAP/SMTP adapter. Sources
// and credentials travel over stdin; neither secrets nor user text enter argv.
func mailGateway(ctx context.Context, c Connector, tool string, args json.RawMessage) (string, error) {
	if tool != "probe" && tool != "search_emails" && tool != "read_email" && tool != "send_email" {
		return "", errInvalid
	}
	bridge, _ := octopAdapters.ReadFile("octop/mail_bridge.py")
	servers, _ := octopAdapters.ReadFile("octop/mail_servers.py")
	adapter, _ := octopAdapters.ReadFile("octop/qq_mail.py")
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	input, _ := json.Marshal(map[string]any{"server_source": string(servers), "adapter_source": string(adapter), "credentials": c.Credentials, "tool": tool, "arguments": args})
	python := os.Getenv("SPIDER_DASHBOARD_PYTHON")
	if python == "" {
		python = "python3"
	}
	if _, err := exec.LookPath(python); err != nil {
		return "", errors.New("个人邮箱需要 Python 3，请安装后重启服务，或设置 SPIDER_DASHBOARD_PYTHON")
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "-I", "-c", string(bridge))
	command.Stdin = bytes.NewReader(input)
	var output limitedMailboxOutput
	command.Stdout = &output
	if err := command.Run(); err != nil {
		return "", errors.New("邮箱连接失败，请检查 IMAP/SMTP 设置与授权码")
	}
	var result struct {
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	if json.Unmarshal(output.Bytes(), &result) != nil {
		return "", errors.New("邮箱工具响应无效")
	}
	if result.Error != "" {
		return "", errors.New(result.Error)
	}
	return gatewayResult(result.Text)
}

type limitedMailboxOutput struct{ bytes.Buffer }

func (b *limitedMailboxOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 128<<10 {
		return 0, errors.New("mail response too large")
	}
	return b.Buffer.Write(p)
}
