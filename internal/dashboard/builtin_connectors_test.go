package dashboard

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tsinling0525/Spider/internal/agent"
)

func TestCopiedMailboxAdapterFixtures(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required for mailbox connector fixtures")
	}
	command := exec.Command(python, "-I", "mail_adapter_test.py")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("mail adapter fixtures failed: %v\n%s", err, output)
	}
}

type connectorRoundTrip func(*http.Request) (*http.Response, error)

func (f connectorRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func connectorResponse(status int, payload any) *http.Response {
	raw, _ := json.Marshal(payload)
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw)))}
}
func builtinInput(kind string) builtinConnectorInput {
	creds := map[string]string{"api_key": "fixture-secret"}
	if kind == "tencent-meeting" {
		creds = map[string]string{"token": "fixture-secret"}
	}
	if kind == "notion" {
		creds = map[string]string{"access_token": "fixture-secret"}
	}
	if kind == "qq-mail" {
		creds = map[string]string{"email": "you@126.com", "password": "fixture-secret", "mail_provider": "netease"}
	}
	return builtinConnectorInput{Kind: kind, Name: kind, Description: "原始说明", Enabled: true, Credentials: creds}
}

func TestBuiltinCredentialsAndIndependentCustomConnector(t *testing.T) {
	s := serviceForTest(t, nil)
	headers := map[string]string{"Authorization": "Bearer custom-fixture"}
	custom, err := s.saveConnector("", Connector{Name: "DoorDash", URL: "http://127.0.0.1:3917/mcp", Enabled: true}, &headers)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"tencent-meeting", "qq-mail", "baidu-map", "didi", "notion"} {
		input := builtinInput(kind)
		c, err := s.saveBuiltin("", input)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(c)
		if strings.Contains(string(raw), "fixture-secret") || c.HasHeaders || !c.HasCredentials || strings.Contains(c.URL, "?") {
			t.Fatal("public connector leaked credentials")
		}
		if kind == "qq-mail" && (c.Credentials["imap_host"] != "imap.126.com" || c.Credentials["smtp_host"] != "smtp.126.com") {
			t.Fatal("NetEase domain mapping lost")
		}
		input.Credentials = nil
		input.Enabled = false
		if _, err := s.saveBuiltin(c.ID, input); err != nil {
			t.Fatal(err)
		}
		stored := s.state.Connectors[c.ID]
		if len(stored.Credentials) == 0 || stored.Enabled {
			t.Fatal("credential preservation/toggle failed")
		}
		if _, err := s.saveConnector(c.ID, Connector{Name: "conversion", URL: "https://example.com/mcp"}, nil); !errors.Is(err, errInvalid) {
			t.Fatal("custom editor changed a built-in")
		}
		input.Credentials = map[string]string{"unknown": "bad"}
		if _, err := s.saveBuiltin(c.ID, input); !errors.Is(err, errInvalid) {
			t.Fatal("unknown credential field accepted")
		}
	}
	restarted, err := NewService(s.path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(restarted.connectors()) != 6 || restarted.state.Connectors[custom.ID].Headers["Authorization"] != "Bearer custom-fixture" {
		t.Fatal("restart damaged existing connector")
	}
	for _, c := range restarted.state.Connectors {
		if c.Kind != "" && len(c.Credentials) == 0 {
			t.Fatal("restart dropped built-in credentials")
		}
	}
}

func TestRemoteBuiltinWireFormatAndToolDiscovery(t *testing.T) {
	for _, kind := range []string{"tencent-meeting", "didi", "notion"} {
		t.Run(kind, func(t *testing.T) {
			c, err := prepareBuiltin(builtinInput(kind), Connector{})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			client := &http.Client{Transport: connectorRoundTrip(func(r *http.Request) (*http.Response, error) {
				switch kind {
				case "tencent-meeting":
					if r.URL.String() != builtinEndpoints[kind] || r.Header.Get("X-Tencent-Meeting-Token") != "fixture-secret" || r.Header.Get("X-Skill-Version") != "v1.0.1" {
						t.Fatal("meeting spec differs from Octop")
					}
				case "didi":
					if r.URL.Query().Get("key") != "fixture-secret" || r.URL.Host != "mcp.didichuxing.com" {
						t.Fatal("Didi query credential missing")
					}
				case "notion":
					if r.Header.Get("Authorization") != "Bearer fixture-secret" {
						t.Fatal("Notion bearer missing")
					}
				}
				var p struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
				}
				_ = json.NewDecoder(r.Body).Decode(&p)
				var result any = map[string]any{}
				switch p.Method {
				case "initialize":
					result = map[string]any{"protocolVersion": "2025-11-25"}
				case "notifications/initialized":
					return connectorResponse(202, nil), nil
				case "tools/list":
					result = map[string]any{"tools": []MCPTool{{Name: "lookup", Description: "查询", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
				case "tools/call":
					calls++
					result = map[string]any{"content": []map[string]string{{"type": "text", "text": "ok"}}}
				default:
					t.Fatal("unexpected MCP method")
				}
				return connectorResponse(200, map[string]any{"jsonrpc": "2.0", "id": p.ID, "result": result}), nil
			})}
			tools, err := probeBuiltin(context.Background(), c, client)
			if err != nil || len(tools) != 1 || calls != 0 {
				t.Fatalf("probe failed: %v", err)
			}
			if _, err := callBuiltin(context.Background(), c, "lookup", json.RawMessage(`{}`), client); err != nil || calls != 1 {
				t.Fatalf("call failed: %v", err)
			}
			if strings.Contains(c.URL, "fixture-secret") {
				t.Fatal("stored URL was mutated")
			}
		})
	}
}

func TestBaiduGatewayWaitsForApprovalAndUsesOriginalTools(t *testing.T) {
	calls := 0
	s := serviceForTest(t, backendFunc(func(_ context.Context, messages []agent.Message, tools []agent.ToolSpec) (agent.Message, error) {
		if messages[len(messages)-1].Role == "tool" {
			return agent.Message{Role: "assistant", Content: "查到了天气"}, nil
		}
		if len(tools) != 3 {
			t.Fatal("Baidu tool catalog differs")
		}
		return agent.Message{Role: "assistant", ToolCalls: []agent.ToolCall{{ID: "weather", Type: "function", Function: agent.FunctionCall{Name: tools[2].Name, Arguments: `{"region":"上海"}`}}}}, nil
	}))
	s.connectorClient = &http.Client{Transport: connectorRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "Bearer fixture-secret" || r.URL.Path != "/agent_plan/v1/weather" {
			t.Fatal("Baidu adapter request differs")
		}
		if calls == 1 && r.URL.Query().Get("region") != "北京" {
			t.Fatal("probe changed")
		}
		if calls == 2 && r.URL.Query().Get("region") != "上海" {
			t.Fatal("approved arguments changed")
		}
		return connectorResponse(200, map[string]any{"status": 0, "result": map[string]string{"weather": "晴"}}), nil
	})}
	c, err := s.saveBuiltin("", builtinInput("baidu-map"))
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.probe(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	conversation, _ := s.createConversation()
	conversation, err = s.send(context.Background(), conversation.ID, conversation.Version, "查上海天气", []string{c.ID})
	if err != nil || conversation.Pending == nil || calls != 1 {
		t.Fatal("gateway called a tool before approval")
	}
	conversation, err = s.decide(context.Background(), conversation.ID, conversation.Version, conversation.Pending.ID, true)
	if err != nil || calls != 2 || conversation.Status != "idle" {
		t.Fatalf("approved gateway call failed: %v", err)
	}
	params := map[string]string{"query": "天安门", "region": "北京"}
	for tool, path := range map[string]string{"search_place": "/place", "plan_direction": "/direction"} {
		client := &http.Client{Transport: connectorRoundTrip(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/agent_plan/v1"+path || r.URL.Query().Get("user_raw_request") != "天安门" {
				t.Fatal("map argument mapping changed")
			}
			return connectorResponse(200, map[string]any{"status": 0}), nil
		})}
		if _, err := baiduMapRequest(context.Background(), s.state.Connectors[c.ID], tool, params, client); err != nil {
			t.Fatal(err)
		}
	}
	client := &http.Client{Transport: connectorRoundTrip(func(*http.Request) (*http.Response, error) {
		return connectorResponse(200, map[string]any{"status": 102, "message": "fixture-secret token失效"}), nil
	})}
	if _, err := baiduMapRequest(context.Background(), s.state.Connectors[c.ID], "get_weather", params, client); err == nil || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatal("Baidu authentication error leaked upstream text")
	}
}

func TestNotionPKCECallbackRestartRefreshAndReplay(t *testing.T) {
	s := serviceForTest(t, nil)
	exchanges, refreshes := 0, 0
	verifier := ""
	client := &http.Client{Transport: connectorRoundTrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			return connectorResponse(200, connectorOAuthMetadata{Issuer: notionIssuer, Authorize: notionIssuer + "/authorize", Token: notionIssuer + "/token", Register: notionIssuer + "/register", AuthMethods: []string{"none"}}), nil
		case "/register":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["token_endpoint_auth_method"] != "none" {
				t.Fatal("DCR auth method changed")
			}
			return connectorResponse(201, map[string]string{"client_id": "client-fixture"}), nil
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("client_id") != "client-fixture" {
				t.Fatal("client identity missing")
			}
			if r.Form.Get("grant_type") == "authorization_code" {
				exchanges++
				verifier = r.Form.Get("code_verifier")
				if r.Form.Get("code") != "code-fixture" {
					t.Fatal("authorization code missing")
				}
				return connectorResponse(200, map[string]any{"access_token": "access-fixture", "refresh_token": "refresh-fixture", "expires_in": 1, "token_type": "Bearer"}), nil
			}
			refreshes++
			if r.Form.Get("refresh_token") != "refresh-fixture" {
				t.Fatal("refresh grant missing")
			}
			return connectorResponse(200, map[string]any{"access_token": "rotated-access", "refresh_token": "rotated-refresh", "expires_in": 3600}), nil
		default:
			t.Fatalf("unexpected OAuth endpoint %s", r.URL.Path)
			return nil, nil
		}
	})}
	s.connectorClient = client
	input := builtinInput("notion")
	input.Credentials = nil
	redirect := "http://127.0.0.1:5174/spider-api" + connectorOAuthCallbackPath
	authorize, state, err := s.startConnectorOAuth(context.Background(), "", input, redirect)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(authorize)
	if parsed.Query().Get("state") != state || parsed.Query().Get("code_challenge_method") != "S256" {
		t.Fatal("PKCE authorization missing")
	}
	restarted, err := NewService(s.path, nil)
	if err != nil {
		t.Fatal(err)
	}
	restarted.connectorClient = client
	handler := NewHandler(restarted, HTTPOptions{APIKey: "dashboard-fixture"})
	r := httptest.NewRequest("GET", connectorOAuthCallbackPath+"?"+url.Values{"state": {state}, "code": {"code-fixture"}, "iss": {notionIssuer}}.Encode(), nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || exchanges != 1 {
		t.Fatalf("callback without bearer failed: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "access-fixture") || strings.Contains(w.Body.String(), "refresh-fixture") || strings.Contains(w.Body.String(), "code-fixture") {
		t.Fatal("callback HTML leaked credentials")
	}
	challenge := sha256.Sum256([]byte(verifier))
	if base64.RawURLEncoding.EncodeToString(challenge[:]) != parsed.Query().Get("code_challenge") {
		t.Fatal("PKCE verifier not preserved across restart")
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 400 || exchanges != 1 {
		t.Fatal("callback replay exchanged a code twice")
	}
	list := restarted.connectors()
	if len(list) != 1 || list[0].CheckedAt != nil {
		t.Fatal("OAuth did not save one untested connector")
	}
	c := restarted.state.Connectors[list[0].ID]
	c.Credentials["expires_at"] = strconv.FormatInt(time.Now().Unix()-1, 10)
	restarted.state.Connectors[c.ID] = c
	fresh, err := restarted.freshOAuthConnector(context.Background(), c)
	if err != nil || refreshes != 1 || fresh.Credentials["access_token"] != "rotated-access" {
		t.Fatal("OAuth refresh failed")
	}
	if _, err := restarted.freshOAuthConnector(context.Background(), c); err != nil || refreshes != 1 {
		t.Fatal("rotating refresh token was reused")
	}
	loaded, err := NewService(s.path, nil)
	if err != nil || loaded.state.Connectors[c.ID].Credentials["refresh_token"] != "rotated-refresh" {
		t.Fatal("refreshed credentials were not persisted")
	}
	if _, err := loaded.saveBuiltin(c.ID, builtinConnectorInput{Kind: "notion", Name: "Notion", Enabled: true, Credentials: map[string]string{"access_token": "manual-token"}}); err != nil {
		t.Fatal(err)
	}
	if loaded.state.Connectors[c.ID].Credentials["refresh_token"] != "" {
		t.Fatal("manual token retained an old OAuth account")
	}
}

func TestBuiltinAPIAuthenticationAndCallbackValidation(t *testing.T) {
	s := serviceForTest(t, nil)
	handler := NewHandler(s, HTTPOptions{APIKey: "dashboard-fixture", AllowedOrigins: []string{"http://127.0.0.1:5174"}})
	raw, _ := json.Marshal(builtinInput("didi"))
	request := func(auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/dashboard/connectors/builtin", strings.NewReader(string(raw)))
		if auth {
			r.Header.Set("Authorization", "Bearer dashboard-fixture")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if request(false).Code != 401 {
		t.Fatal("built-in credentials saved without API authentication")
	}
	w := request(true)
	if w.Code != 200 || strings.Contains(w.Body.String(), "fixture-secret") {
		t.Fatal("built-in save did not redact credentials")
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:8083/v1/dashboard/connectors/oauth/start", nil)
	r.Header.Set("Origin", "http://127.0.0.1:5174")
	options := HTTPOptions{AllowedOrigins: []string{"http://127.0.0.1:5174"}}
	if !allowedConnectorCallback("http://127.0.0.1:5174/spider-api"+connectorOAuthCallbackPath, r, options) {
		t.Fatal("dev proxy callback rejected")
	}
	for _, redirect := range []string{"https://evil.example" + connectorOAuthCallbackPath, "http://127.0.0.1:5174/other", "http://127.0.0.1:5174/spider-api" + connectorOAuthCallbackPath + "?x=1"} {
		if allowedConnectorCallback(redirect, r, options) {
			t.Fatal("unsafe callback accepted")
		}
	}
	if validateNotionOAuthEndpoint("https://mcp.notion.com.evil.example/token") == nil || validateNotionOAuthEndpoint("http://mcp.notion.com/token") == nil {
		t.Fatal("unsafe vendor endpoint accepted")
	}
}
