package dashboard

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func registerBuiltinConnectorRoutes(mux *http.ServeMux, s *Service, options HTTPOptions) {
	save := func(w http.ResponseWriter, r *http.Request) {
		var input builtinConnectorInput
		if !decode(w, r, &input) {
			return
		}
		c, err := s.saveBuiltin(r.PathValue("id"), input)
		if err != nil {
			respondError(w, err)
			return
		}
		respond(w, 200, c)
	}
	mux.HandleFunc("POST /v1/dashboard/connectors/builtin", save)
	mux.HandleFunc("PUT /v1/dashboard/connectors/builtin/{id}", save)
	mux.HandleFunc("POST /v1/dashboard/connectors/builtin/test", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ID       string                `json:"connector_id"`
			Provider builtinConnectorInput `json:"connector"`
		}
		if !decode(w, r, &input) {
			return
		}
		c, err := s.builtinDraft(input.ID, input.Provider)
		if err != nil {
			respondError(w, err)
			return
		}
		c, err = s.freshOAuthConnector(r.Context(), c)
		if err != nil {
			respondError(w, err)
			return
		}
		tools, err := probeBuiltin(r.Context(), c, s.connectorClient)
		if err != nil {
			respondError(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "tools": tools, "message": fmt.Sprintf("连接成功，发现 %d 个工具", len(tools))})
	})
	mux.HandleFunc("POST /v1/dashboard/connectors/oauth/start", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ID        string                `json:"connector_id"`
			Connector builtinConnectorInput `json:"connector"`
			Redirect  string                `json:"redirect_uri"`
		}
		if !decode(w, r, &input) {
			return
		}
		if !allowedConnectorCallback(input.Redirect, r, options) {
			respondError(w, fmt.Errorf("%w: 无效的授权回调地址", errInvalid))
			return
		}
		authorize, state, err := s.startConnectorOAuth(r.Context(), input.ID, input.Connector, input.Redirect)
		if err != nil {
			respondError(w, err)
			return
		}
		respond(w, 200, map[string]string{"authorize_url": authorize, "state": state})
	})
	mux.HandleFunc("GET /v1/dashboard/connectors/oauth/status/{state}", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		flow, found := s.state.ConnectorOAuth[r.PathValue("state")]
		s.mu.Unlock()
		if !found || time.Now().After(flow.ExpiresAt) {
			respondError(w, fmt.Errorf("%w: 授权已过期，请重试", errNotFound))
			return
		}
		respond(w, 200, map[string]string{"status": flow.Status, "error": flow.Error, "connector_id": flow.ConnectorID})
	})
	mux.HandleFunc("GET "+connectorOAuthCallbackPath, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		state := query.Get("state")
		flow, err := s.completeConnectorOAuth(r.Context(), state, query.Get("code"), query.Get("error"), query.Get("iss"))
		message := "Notion 授权成功。返回 Spider 测试连接后即可使用。"
		status := 200
		if err != nil {
			message = "Notion 授权失败或已取消，请返回 Spider 重新授权。"
			status = 400
		}
		origin := ""
		if redirect, parseErr := url.Parse(flow.RedirectURI); parseErr == nil && redirect.Host != "" {
			origin = redirect.Scheme + "://" + redirect.Host
		}
		nonce := oauthRandom()
		payload, _ := json.Marshal(map[string]string{"type": "spider-connector-oauth", "state": state})
		target, _ := json.Marshal(origin)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; style-src 'unsafe-inline'; frame-ancestors 'none'")
		w.WriteHeader(status)
		fmt.Fprintf(w, `<!doctype html><html lang="zh"><meta charset="utf-8"><title>Spider · Notion 授权</title><body style="font:16px system-ui;padding:48px;color:#222"><h1>Spider</h1><p>%s</p><a href="%s">返回 Spider</a><script nonce="%s">if(window.opener && %s){window.opener.postMessage(%s,%s);window.close();}</script></body></html>`, html.EscapeString(message), html.EscapeString(origin), nonce, target, payload, target)
	})
}

func allowedConnectorCallback(raw string, r *http.Request, options HTTPOptions) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != connectorOAuthCallbackPath && u.Path != "/spider-api"+connectorOAuthCallbackPath) {
		return false
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")) {
		return false
	}
	origin := u.Scheme + "://" + u.Host
	// The callback must be on the requesting app, or on its explicit local
	// proxy origin. Never use arbitrary user-supplied redirects for OAuth.
	if incoming := r.Header.Get("Origin"); incoming != "" && incoming != origin {
		return false
	}
	if u.Host == r.Host {
		return true
	}
	for _, entry := range options.AllowedOrigins {
		if strings.TrimSuffix(entry, "/") == origin {
			return true
		}
	}
	return false
}
