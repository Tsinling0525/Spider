// Package llm provides a streaming gateway to Dify Chatflow.
package llm

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

func ValidateListener(address, apiKey string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); apiKey == "" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("LLM_API_KEY is required for non-loopback listeners")
	}
	return nil
}

// NewHandler forwards chat requests to a server-configured Dify Chatflow.
// Each deployment represents one principal; client-supplied users are ignored.
func NewHandler(baseURL, apiKey, difyKey, user string) (http.Handler, error) {
	if strings.TrimSpace(difyKey) == "" {
		return nil, errors.New("LLM_DIFY_API_KEY is required")
	}
	if strings.TrimSpace(user) == "" {
		return nil, errors.New("LLM_DIFY_USER is required")
	}
	target, err := url.Parse(baseURL)
	if err != nil {
		return nil, errors.New("invalid LLM_DIFY_BASE_URL")
	}
	if (target.Scheme != "http" && target.Scheme != "https") || target.Hostname() == "" || target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		return nil, errors.New("LLM_DIFY_BASE_URL must be an HTTP(S) URL without credentials, query or fragment")
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Path = "/chat-messages"
			r.Out.URL.RawPath = ""
			r.Out.URL.RawQuery = ""
			r.SetURL(target)
			r.Out.Header.Set("Authorization", "Bearer "+difyKey)
			r.Out.Header.Del("Cookie")
		},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("{\"error\":\"dify unavailable\"}\n"))
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if apiKey != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+apiKey)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/healthz" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{\"status\":\"ok\"}\n"))
			return
		}
		if r.URL.Path != "/v1/chat-messages" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
		if err != nil {
			var tooLarge *http.MaxBytesError
			status := http.StatusBadRequest
			if errors.As(err, &tooLarge) {
				status = http.StatusRequestEntityTooLarge
			}
			http.Error(w, "invalid request body", status)
			return
		}
		var payload map[string]json.RawMessage
		if json.Unmarshal(body, &payload) != nil || payload == nil {
			http.Error(w, "expected a JSON object", http.StatusBadRequest)
			return
		}
		var query, mode string
		if json.Unmarshal(payload["query"], &query) != nil || strings.TrimSpace(query) == "" {
			http.Error(w, "query is required", http.StatusBadRequest)
			return
		}
		if raw, ok := payload["response_mode"]; ok {
			if json.Unmarshal(raw, &mode) != nil || (mode != "blocking" && mode != "streaming") {
				http.Error(w, "response_mode must be blocking or streaming", http.StatusBadRequest)
				return
			}
		} else {
			payload["response_mode"] = json.RawMessage(`"streaming"`)
		}
		if _, ok := payload["inputs"]; !ok {
			payload["inputs"] = json.RawMessage("{}")
		}
		payload["user"], _ = json.Marshal(user)
		body, _ = json.Marshal(payload)
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		r.Header.Set("Content-Type", "application/json")
		proxy.ServeHTTP(w, r)
	}), nil
}
