package dashboard

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type HTTPOptions struct {
	APIKey         string
	AllowedOrigins []string
	Voice          VoiceOptions
}

func NewHandler(service *Service, options HTTPOptions) http.Handler {
	mux := http.NewServeMux()
	registerModelRoutes(mux, service)
	registerBuiltinConnectorRoutes(mux, service, options)
	registerMemoryRoutes(mux, service)
	mux.HandleFunc("GET /v1/dashboard/capabilities", func(w http.ResponseWriter, r *http.Request) {
		_, err := service.chatBackend()
		voice := service.voiceOptions(options.Voice)
		respond(w, 200, map[string]any{"chat": err == nil, "transcription": voice.STT.Model != "", "speech": voice.TTS.Model != "", "transport": "streamable_http"})
	})
	mux.HandleFunc("GET /v1/dashboard/connectors", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]any{"connectors": service.connectors()})
	})
	save := func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Name        string             `json:"name"`
			URL         string             `json:"url"`
			Description string             `json:"description"`
			Enabled     bool               `json:"enabled"`
			Headers     *map[string]string `json:"headers,omitempty"`
		}
		if !decode(w, r, &input) {
			return
		}
		connector, err := service.saveConnector(r.PathValue("id"), Connector{Name: input.Name, URL: input.URL, Description: input.Description, Enabled: input.Enabled}, input.Headers)
		if err != nil {
			respondError(w, err)
			return
		}
		respond(w, 200, connector)
	}
	mux.HandleFunc("POST /v1/dashboard/connectors", save)
	mux.HandleFunc("PUT /v1/dashboard/connectors/{id}", save)
	mux.HandleFunc("DELETE /v1/dashboard/connectors/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := service.deleteConnector(r.PathValue("id")); err != nil {
			respondError(w, err)
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /v1/dashboard/connectors/{id}/probe", func(w http.ResponseWriter, r *http.Request) {
		connector, err := service.probe(r.Context(), r.PathValue("id"))
		if err != nil {
			respondError(w, err)
			return
		}
		respond(w, 200, connector)
	})
	mux.HandleFunc("GET /v1/dashboard/conversations", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]any{"conversations": service.conversations()})
	})
	mux.HandleFunc("POST /v1/dashboard/conversations", func(w http.ResponseWriter, r *http.Request) {
		c, err := service.createConversation()
		if err != nil {
			respondError(w, err)
			return
		}
		respond(w, 201, c)
	})
	mux.HandleFunc("GET /v1/dashboard/conversations/{id}", func(w http.ResponseWriter, r *http.Request) {
		c, err := service.conversation(r.PathValue("id"))
		if err != nil {
			respondError(w, err)
			return
		}
		respond(w, 200, c)
	})
	mux.HandleFunc("DELETE /v1/dashboard/conversations/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := service.deleteConversation(r.PathValue("id")); err != nil {
			respondError(w, err)
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /v1/dashboard/conversations/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Content      string   `json:"content"`
			ConnectorIDs []string `json:"connector_ids"`
			Version      int64    `json:"expected_version"`
		}
		if !decode(w, r, &input) {
			return
		}
		c, err := service.send(r.Context(), r.PathValue("id"), input.Version, input.Content, input.ConnectorIDs)
		if err != nil {
			respondError(w, err)
			return
		}
		respond(w, 200, c)
	})
	mux.HandleFunc("POST /v1/dashboard/conversations/{id}/decisions", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ApprovalID string `json:"approval_id"`
			Version    int64  `json:"expected_version"`
			Approve    *bool  `json:"approve"`
		}
		if !decode(w, r, &input) {
			return
		}
		if input.Approve == nil {
			respondError(w, errInvalid)
			return
		}
		c, err := service.decide(r.Context(), r.PathValue("id"), input.Version, input.ApprovalID, *input.Approve)
		if err != nil {
			respondError(w, err)
			return
		}
		respond(w, 200, c)
	})
	mux.HandleFunc("POST /v1/dashboard/voice/transcriptions", func(w http.ResponseWriter, r *http.Request) { service.voiceOptions(options.Voice).transcribe(w, r) })
	mux.HandleFunc("POST /v1/dashboard/voice/speech", func(w http.ResponseWriter, r *http.Request) { service.voiceOptions(options.Voice).speak(w, r) })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		// OAuth callbacks authenticate with a one-time PKCE state, since the
		// vendor's browser redirect cannot carry our dashboard bearer header.
		callback := r.Method == "GET" && r.URL.Path == connectorOAuthCallbackPath
		if !callback && options.APIKey != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+options.APIKey)) != 1 {
			respond(w, 401, map[string]any{"error": map[string]string{"message": "a valid bearer token is required"}})
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			allowed := err == nil && u.Host == r.Host && (u.Scheme == "http" || u.Scheme == "https")
			for _, entry := range options.AllowedOrigins {
				allowed = allowed || origin == entry
			}
			if !allowed {
				respond(w, 403, map[string]any{"error": map[string]string{"message": "origin is not allowed"}})
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		respondError(w, errInvalid)
		return false
	}
	return true
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func respondError(w http.ResponseWriter, err error) {
	status := 502
	switch {
	case errors.Is(err, errInvalid):
		status = 400
	case errors.Is(err, errNotFound):
		status = 404
	case errors.Is(err, errConflict):
		status = 409
	case errors.Is(err, errBusy):
		status = 409
	}
	message := err.Error()
	// Filesystem paths are operational details, not public error payloads.
	if strings.Contains(message, "permission denied") || strings.Contains(message, "no such file") {
		message = "could not persist dashboard state"
		status = 500
	}
	respond(w, status, map[string]any{"error": map[string]string{"message": message}})
}
