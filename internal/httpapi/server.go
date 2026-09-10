package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	maildomain "github.com/Tsinling0525/Spider/internal/mail"
	mailoauth "github.com/Tsinling0525/Spider/internal/oauth"
)

type Server struct {
	service *maildomain.Service
	oauth   *mailoauth.Flow
	apiKey  string
	logger  *slog.Logger
}

func NewServer(service *maildomain.Service, oauth *mailoauth.Flow, apiKey string, logger *slog.Logger) http.Handler {
	server := &Server{service: service, oauth: oauth, apiKey: apiKey, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", server.health)
	mux.HandleFunc("GET /v1/oauth/gmail/callback", server.oauthCallback)
	mux.Handle("GET /v1/oauth/gmail/start", server.authenticate(http.HandlerFunc(server.oauthStart)))
	mux.Handle("GET /v1/oauth/gmail/status", server.authenticate(http.HandlerFunc(server.oauthStatus)))
	mux.Handle("GET /v1/email/threads", server.authenticate(http.HandlerFunc(server.listThreads)))
	mux.Handle("GET /v1/email/threads/{thread_id}", server.authenticate(http.HandlerFunc(server.getThread)))
	mux.Handle("POST /v1/email/drafts", server.authenticate(http.HandlerFunc(server.generateDraft)))
	mux.Handle("POST /v1/email/send", server.authenticate(http.HandlerFunc(server.send)))
	return server.recoverPanic(server.requestLog(mux))
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "ok", "gmail_connected": s.oauth.Connected()})
}

func (s *Server) oauthStart(w http.ResponseWriter, r *http.Request) {
	url, err := s.oauth.Start(r.URL.Query().Get("send") == "true")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "oauth_start_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"authorization_url": url})
}

func (s *Server) oauthCallback(w http.ResponseWriter, r *http.Request) {
	if providerError := r.URL.Query().Get("error"); providerError != "" {
		writeError(w, http.StatusBadRequest, "oauth_denied", providerError)
		return
	}
	if err := s.oauth.Complete(r.Context(), r.URL.Query().Get("state"), r.URL.Query().Get("code")); err != nil {
		writeError(w, http.StatusBadRequest, "oauth_callback_failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("<!doctype html><title>Gmail connected</title><p>Gmail connection completed. You may close this window.</p>"))
}

func (s *Server) oauthStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"connected": s.oauth.Connected()})
}

func (s *Server) listThreads(w http.ResponseWriter, r *http.Request) {
	pageSize, _ := strconv.ParseInt(r.URL.Query().Get("page_size"), 10, 64)
	page, err := s.service.ListThreads(r.Context(), maildomain.ListOptions{
		Query: r.URL.Query().Get("query"), PageToken: r.URL.Query().Get("page_token"), PageSize: pageSize,
	})
	if err != nil {
		s.handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) getThread(w http.ResponseWriter, r *http.Request) {
	thread, err := s.service.GetThread(r.Context(), r.PathValue("thread_id"))
	if err != nil {
		s.handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, thread)
}

func (s *Server) generateDraft(w http.ResponseWriter, r *http.Request) {
	var request maildomain.DraftRequest
	if err := decodeJSON(w, r, &request); err != nil {
		return
	}
	draft, err := s.service.GenerateDraft(r.Context(), request)
	if err != nil {
		s.handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, draft)
}

func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	var request maildomain.SendRequest
	if err := decodeJSON(w, r, &request); err != nil {
		return
	}
	receipt, err := s.service.Send(r.Context(), request)
	if err != nil {
		s.handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(s.apiKey)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "a valid bearer token is required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleServiceError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	code := "upstream_error"
	if errors.Is(err, maildomain.ErrHumanConfirmationRequired) || errors.Is(err, maildomain.ErrIdempotencyKeyRequired) || strings.Contains(err.Error(), "required") {
		status, code = http.StatusBadRequest, "invalid_request"
	}
	if strings.Contains(err.Error(), "not connected") || strings.Contains(err.Error(), "not configured") {
		status, code = http.StatusServiceUnavailable, "service_unavailable"
	}
	writeError(w, status, code, err.Error())
}

func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Info("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
	})
}

func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("panic", "error", fmt.Sprint(recovered))
				writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target interface{}) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]interface{}{"error": map[string]string{"code": code, "message": message}})
}
