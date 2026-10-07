package dashboard

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const connectorOAuthCallbackPath = "/v1/dashboard/connectors/oauth/callback"
const notionIssuer = "https://mcp.notion.com"

type connectorOAuthMetadata struct {
	Issuer      string   `json:"issuer"`
	Authorize   string   `json:"authorization_endpoint"`
	Token       string   `json:"token_endpoint"`
	Register    string   `json:"registration_endpoint"`
	AuthMethods []string `json:"token_endpoint_auth_methods_supported"`
}

type connectorOAuthFlow struct {
	Status       string                 `json:"status"`
	Error        string                 `json:"error,omitempty"`
	ConnectorID  string                 `json:"connector_id,omitempty"`
	Input        builtinConnectorInput  `json:"input,omitempty"`
	PreviousHash string                 `json:"previous_hash,omitempty"`
	RedirectURI  string                 `json:"redirect_uri,omitempty"`
	Verifier     string                 `json:"verifier,omitempty"`
	ClientID     string                 `json:"client_id,omitempty"`
	ClientSecret string                 `json:"client_secret,omitempty"`
	Metadata     connectorOAuthMetadata `json:"metadata,omitempty"`
	ExpiresAt    time.Time              `json:"expires_at"`
}

func oauthRandom() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}
func connectorHash(c Connector) string {
	raw, _ := json.Marshal(c)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func validateNotionOAuthEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host != "mcp.notion.com" || u.User != nil || u.Fragment != "" {
		return errors.New("Notion OAuth 返回了无效的授权端点")
	}
	return nil
}

func oauthJSON(ctx context.Context, client *http.Client, method, endpoint string, body io.Reader, contentType string, target any) error {
	if err := validateNotionOAuthEndpoint(endpoint); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return errInvalid
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return errors.New("Notion 授权服务连接失败，请重试")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Notion 授权服务返回 HTTP %d，请重新授权", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		return errors.New("Notion 授权响应无效")
	}
	if json.Unmarshal(raw, target) != nil {
		return errors.New("Notion 授权响应无效")
	}
	return nil
}

func (s *Service) startConnectorOAuth(ctx context.Context, id string, input builtinConnectorInput, redirectURI string) (string, string, error) {
	if input.Kind != "notion" || len(input.Credentials) != 0 {
		return "", "", errInvalid
	}
	// Validate the name and immutable catalog URL before dynamic registration.
	if err := validateConnector(Connector{Name: input.Name, Description: input.Description, URL: builtinEndpoints["notion"]}); err != nil {
		return "", "", err
	}
	s.mu.Lock()
	previous, found := s.state.Connectors[id]
	s.mu.Unlock()
	if id != "" && (!found || previous.Kind != "notion") {
		return "", "", errNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var metadata connectorOAuthMetadata
	if err := oauthJSON(ctx, s.connectorClient, http.MethodGet, notionIssuer+"/.well-known/oauth-authorization-server", nil, "", &metadata); err != nil {
		return "", "", err
	}
	if metadata.Issuer != notionIssuer {
		return "", "", errors.New("Notion OAuth issuer 无效")
	}
	for _, endpoint := range []string{metadata.Authorize, metadata.Token, metadata.Register} {
		if err := validateNotionOAuthEndpoint(endpoint); err != nil {
			return "", "", err
		}
	}
	authMethod := "client_secret_post"
	for _, method := range metadata.AuthMethods {
		if method == "none" {
			authMethod = "none"
		}
	}
	registration, _ := json.Marshal(map[string]any{"client_name": "Spider Connector", "redirect_uris": []string{redirectURI}, "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "token_endpoint_auth_method": authMethod})
	var client struct {
		ID     string `json:"client_id"`
		Secret string `json:"client_secret"`
	}
	if err := oauthJSON(ctx, s.connectorClient, http.MethodPost, metadata.Register, bytes.NewReader(registration), "application/json", &client); err != nil {
		return "", "", err
	}
	if client.ID == "" || len(client.ID) > 8192 || len(client.Secret) > 8192 {
		return "", "", errors.New("Notion 客户端注册失败")
	}
	state, verifier := oauthRandom(), oauthRandom()
	challenge := sha256.Sum256([]byte(verifier))
	authorize, _ := url.Parse(metadata.Authorize)
	params := authorize.Query()
	for key, value := range map[string]string{"response_type": "code", "client_id": client.ID, "redirect_uri": redirectURI, "state": state, "code_challenge": base64.RawURLEncoding.EncodeToString(challenge[:]), "code_challenge_method": "S256"} {
		params.Set(key, value)
	}
	authorize.RawQuery = params.Encode()
	flow := connectorOAuthFlow{Status: "pending", ConnectorID: id, Input: input, RedirectURI: redirectURI, Verifier: verifier, ClientID: client.ID, ClientSecret: client.Secret, Metadata: metadata, ExpiresAt: time.Now().Add(10 * time.Minute)}
	if found {
		flow.PreviousHash = connectorHash(previous)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, item := range s.state.ConnectorOAuth {
		if time.Now().After(item.ExpiresAt) {
			delete(s.state.ConnectorOAuth, key)
		}
	}
	if len(s.state.ConnectorOAuth) >= 32 {
		return "", "", errBusy
	}
	s.state.ConnectorOAuth[state] = flow
	if err := s.persistLocked(); err != nil {
		delete(s.state.ConnectorOAuth, state)
		return "", "", err
	}
	return authorize.String(), state, nil
}

type connectorOAuthTokens struct {
	Access    string `json:"access_token"`
	Refresh   string `json:"refresh_token"`
	ExpiresIn int64  `json:"expires_in"`
	Type      string `json:"token_type"`
}

func exchangeConnectorOAuth(ctx context.Context, client *http.Client, endpoint string, values url.Values) (connectorOAuthTokens, error) {
	var tokens connectorOAuthTokens
	if err := oauthJSON(ctx, client, http.MethodPost, endpoint, strings.NewReader(values.Encode()), "application/x-www-form-urlencoded", &tokens); err != nil {
		return tokens, err
	}
	if tokens.Access == "" || len(tokens.Access) > 8192 || len(tokens.Refresh) > 8192 || strings.ContainsAny(tokens.Access+tokens.Refresh, "\r\n") || (tokens.Type != "" && !strings.EqualFold(tokens.Type, "bearer")) {
		return tokens, errors.New("Notion 授权未返回有效凭据")
	}
	return tokens, nil
}

func oauthTokenCredentials(tokens connectorOAuthTokens, clientID, clientSecret, endpoint string) map[string]string {
	c := map[string]string{"access_token": tokens.Access, "refresh_token": tokens.Refresh, "oauth_client_id": clientID, "oauth_client_secret": clientSecret, "oauth_token_url": endpoint}
	if tokens.ExpiresIn > 0 {
		c["expires_at"] = strconv.FormatInt(time.Now().Unix()+tokens.ExpiresIn, 10)
	}
	return c
}

func (s *Service) completeConnectorOAuth(ctx context.Context, state, code, denied, issuer string) (connectorOAuthFlow, error) {
	s.mu.Lock()
	flow, found := s.state.ConnectorOAuth[state]
	if !found || flow.Status != "pending" || time.Now().After(flow.ExpiresAt) {
		s.mu.Unlock()
		return flow, errors.New("授权已过期或已使用，请重新授权")
	}
	flow.Status = "exchanging"
	s.state.ConnectorOAuth[state] = flow
	if err := s.persistLocked(); err != nil {
		flow.Status = "pending"
		s.state.ConnectorOAuth[state] = flow
		s.mu.Unlock()
		return flow, err
	}
	s.mu.Unlock()
	err := func() error {
		if denied != "" {
			return errors.New("Notion 授权已取消")
		}
		if code == "" || len(code) > 8192 || (issuer != "" && issuer != notionIssuer) {
			return errors.New("Notion 授权回调无效")
		}
		values := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {flow.RedirectURI}, "client_id": {flow.ClientID}, "code_verifier": {flow.Verifier}}
		if flow.ClientSecret != "" {
			values.Set("client_secret", flow.ClientSecret)
		}
		tokens, err := exchangeConnectorOAuth(ctx, s.connectorClient, flow.Metadata.Token, values)
		if err != nil {
			return err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		previous, found := s.state.Connectors[flow.ConnectorID]
		if flow.ConnectorID != "" && (!found || connectorHash(previous) != flow.PreviousHash) {
			return errConflict
		}
		c, err := prepareBuiltin(builtinConnectorInput{Kind: "notion", Name: flow.Input.Name, Description: flow.Input.Description, Enabled: flow.Input.Enabled, Credentials: map[string]string{"access_token": tokens.Access}}, previous)
		if err != nil {
			return err
		}
		c.Credentials = oauthTokenCredentials(tokens, flow.ClientID, flow.ClientSecret, flow.Metadata.Token)
		if flow.ConnectorID == "" {
			if len(s.state.Connectors) >= 64 {
				return errBusy
			}
			flow.ConnectorID = newID()
		}
		c.ID = flow.ConnectorID
		s.state.Connectors[c.ID] = c
		if err := s.persistLocked(); err != nil {
			if found {
				s.state.Connectors[c.ID] = previous
			} else {
				delete(s.state.Connectors, c.ID)
			}
			return err
		}
		return nil
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		flow.Status = "error"
		flow.Error = err.Error()
	} else {
		flow.Status = "authorized"
	}
	flow.Verifier = ""
	flow.ClientSecret = ""
	flow.ClientID = ""
	flow.Metadata = connectorOAuthMetadata{}
	flow.Input = builtinConnectorInput{}
	flow.PreviousHash = ""
	s.state.ConnectorOAuth[state] = flow
	if persistErr := s.persistLocked(); persistErr != nil && err == nil {
		err = persistErr
	}
	return flow, err
}

func (s *Service) freshOAuthConnector(ctx context.Context, c Connector) (Connector, error) {
	if c.Kind != "notion" || c.Credentials["refresh_token"] == "" {
		return c, nil
	}
	expires, _ := strconv.ParseInt(c.Credentials["expires_at"], 10, 64)
	if expires == 0 || expires > time.Now().Unix()+30 {
		return c, nil
	}
	s.oauthMu.Lock()
	defer s.oauthMu.Unlock()
	s.mu.Lock()
	current, found := s.state.Connectors[c.ID]
	s.mu.Unlock()
	if found && current.Credentials["oauth_client_id"] == c.Credentials["oauth_client_id"] && current.Credentials["refresh_token"] != c.Credentials["refresh_token"] && current.Credentials["access_token"] != "" {
		// Keep the approved connector snapshot while picking up a rotated grant.
		c.Credentials = copyCredentials(current.Credentials)
		currentExpiry, _ := strconv.ParseInt(c.Credentials["expires_at"], 10, 64)
		if currentExpiry == 0 || currentExpiry > time.Now().Unix()+30 {
			return c, nil
		}
	}
	values := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {c.Credentials["refresh_token"]}, "client_id": {c.Credentials["oauth_client_id"]}}
	if secret := c.Credentials["oauth_client_secret"]; secret != "" {
		values.Set("client_secret", secret)
	}
	tokens, err := exchangeConnectorOAuth(ctx, s.connectorClient, c.Credentials["oauth_token_url"], values)
	if err != nil {
		return c, err
	}
	if tokens.Refresh == "" {
		tokens.Refresh = c.Credentials["refresh_token"]
	}
	oldRefresh := c.Credentials["refresh_token"]
	c.Credentials = oauthTokenCredentials(tokens, c.Credentials["oauth_client_id"], c.Credentials["oauth_client_secret"], c.Credentials["oauth_token_url"])
	s.mu.Lock()
	defer s.mu.Unlock()
	current, found = s.state.Connectors[c.ID]
	if found && current.Kind == "notion" && current.Credentials["refresh_token"] == oldRefresh && current.Credentials["oauth_client_id"] == c.Credentials["oauth_client_id"] {
		updated := current
		updated.Credentials = copyCredentials(c.Credentials)
		s.state.Connectors[c.ID] = updated
		if err := s.persistLocked(); err != nil {
			s.state.Connectors[c.ID] = current
			return c, err
		}
	}
	return c, nil
}
