package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Tsinling0525/Spider/internal/humantask"
	maildomain "github.com/Tsinling0525/Spider/internal/mail"
	mailoauth "github.com/Tsinling0525/Spider/internal/oauth"
)

type Server struct {
	service    *maildomain.Service
	oauth      *mailoauth.Flow
	apiKey     string
	logger     *slog.Logger
	humanTasks *humantask.Service
	reviewerID string
}

type reviewerContextKey struct{}

type humanTaskPage struct {
	Tasks    []humantask.Task `json:"tasks"`
	Revision int64            `json:"revision"`
	Reviewer string           `json:"reviewer"`
}

func pageForReviewer(snapshot humantask.Snapshot, reviewer string) humanTaskPage {
	return humanTaskPage{Tasks: snapshot.Tasks, Revision: snapshot.Revision, Reviewer: reviewer}
}

func NewServer(service *maildomain.Service, oauth *mailoauth.Flow, apiKey string, logger *slog.Logger, taskServices ...*humantask.Service) http.Handler {
	return NewServerForPrincipal(service, oauth, apiKey, "principal:owner", logger, taskServices...)
}

func NewServerForPrincipal(service *maildomain.Service, oauth *mailoauth.Flow, apiKey, reviewerID string, logger *slog.Logger, taskServices ...*humantask.Service) http.Handler {
	var humanTasks *humantask.Service
	if len(taskServices) > 0 {
		humanTasks = taskServices[0]
	}
	reviewerID = strings.TrimSpace(reviewerID)
	if reviewerID == "" {
		reviewerID = "principal:owner"
	}
	server := &Server{service: service, oauth: oauth, apiKey: apiKey, logger: logger, humanTasks: humanTasks, reviewerID: reviewerID}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", server.health)
	mux.HandleFunc("GET /auth/google/callback", server.oauthCallback)
	mux.HandleFunc("GET /v1/oauth/gmail/callback", server.oauthCallback)
	mux.Handle("GET /v1/oauth/gmail/start", server.authenticate(http.HandlerFunc(server.oauthStart)))
	mux.Handle("GET /v1/oauth/gmail/status", server.authenticate(http.HandlerFunc(server.oauthStatus)))
	mux.Handle("GET /v1/email/threads", server.authenticate(http.HandlerFunc(server.listThreads)))
	mux.Handle("GET /v1/email/threads/{thread_id}", server.authenticate(http.HandlerFunc(server.getThread)))
	mux.Handle("POST /v1/email/drafts", server.authenticate(http.HandlerFunc(server.generateDraft)))
	mux.Handle("POST /v1/email/send", server.authenticate(http.HandlerFunc(server.send)))
	mux.Handle("GET /v1/human-tasks", server.authenticate(http.HandlerFunc(server.listHumanTasks)))
	mux.Handle("GET /v1/human-tasks/events", server.authenticate(http.HandlerFunc(server.streamHumanTasks)))
	mux.Handle("GET /v1/human-tasks/changes", server.authenticate(http.HandlerFunc(server.waitHumanTaskChanges)))
	mux.Handle("GET /v1/human-tasks/{task_id}", server.authenticate(http.HandlerFunc(server.getHumanTask)))
	mux.Handle("GET /v1/human-tasks/{task_id}/history", server.authenticate(http.HandlerFunc(server.humanTaskHistory)))
	mux.Handle("POST /v1/human-tasks/{task_id}/claims", server.authenticate(http.HandlerFunc(server.claimHumanTask)))
	mux.Handle("POST /v1/human-tasks/{task_id}/decisions", server.authenticate(http.HandlerFunc(server.decideHumanTask)))
	return server.recoverPanic(server.requestLog(mux))
}

func (s *Server) listHumanTasks(w http.ResponseWriter, r *http.Request) {
	if s.humanTasks == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "human task service is not configured")
		return
	}
	snapshot, err := s.humanTasks.Snapshot()
	if err != nil {
		s.handleHumanTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pageForReviewer(snapshot, reviewerFromContext(r.Context())))
}

func (s *Server) streamHumanTasks(w http.ResponseWriter, r *http.Request) {
	if s.humanTasks == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "human task service is not configured")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "stream_unavailable", "streaming is unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	updates, cancel := s.humanTasks.Subscribe()
	defer cancel()
	writeSnapshot := func() bool {
		snapshot, err := s.humanTasks.Snapshot()
		if err != nil {
			return false
		}
		payload, err := json.Marshal(pageForReviewer(snapshot, reviewerFromContext(r.Context())))
		if err != nil {
			return false
		}
		_, _ = fmt.Fprintf(w, "event: tasks\ndata: %s\n\n", payload)
		flusher.Flush()
		return true
	}
	if !writeSnapshot() {
		return
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-updates:
			if !writeSnapshot() {
				return
			}
		case <-heartbeat.C:
			if !writeSnapshot() {
				return
			}
		}
	}
}

func (s *Server) waitHumanTaskChanges(w http.ResponseWriter, r *http.Request) {
	if s.humanTasks == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "human task service is not configured")
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	waitSeconds, _ := strconv.Atoi(r.URL.Query().Get("wait_seconds"))
	if waitSeconds <= 0 || waitSeconds > 25 {
		waitSeconds = 25
	}
	snapshot, err := s.humanTasks.Changes(r.Context(), after, time.Duration(waitSeconds)*time.Second)
	if err != nil {
		return
	}
	writeJSON(w, http.StatusOK, pageForReviewer(snapshot, reviewerFromContext(r.Context())))
}

func (s *Server) claimHumanTask(w http.ResponseWriter, r *http.Request) {
	if s.humanTasks == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "human task service is not configured")
		return
	}
	var claim humantask.Claim
	if err := decodeJSON(w, r, &claim); err != nil {
		return
	}
	claim.Actor = reviewerFromContext(r.Context())
	task, err := s.humanTasks.Claim(r.PathValue("task_id"), claim)
	if err != nil {
		s.handleHumanTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) humanTaskHistory(w http.ResponseWriter, r *http.Request) {
	if s.humanTasks == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "human task service is not configured")
		return
	}
	events, err := s.humanTasks.History(r.PathValue("task_id"))
	if err != nil {
		s.handleHumanTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"events": events})
}

func (s *Server) getHumanTask(w http.ResponseWriter, r *http.Request) {
	if s.humanTasks == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "human task service is not configured")
		return
	}
	task, err := s.humanTasks.Get(r.PathValue("task_id"))
	if err != nil {
		s.handleHumanTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) decideHumanTask(w http.ResponseWriter, r *http.Request) {
	if s.humanTasks == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "human task service is not configured")
		return
	}
	var decision humantask.Decision
	if err := decodeJSON(w, r, &decision); err != nil {
		return
	}
	decision.Reviewer = reviewerFromContext(r.Context())
	task, err := s.humanTasks.Decide(r.Context(), r.PathValue("task_id"), decision)
	if err != nil {
		s.handleHumanTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleHumanTaskError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, humantask.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, humantask.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, humantask.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
	case strings.Contains(err.Error(), "required"):
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
	default:
		writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
	}
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
		if s.apiKey == "" {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), reviewerContextKey{}, s.reviewerID)))
			return
		}
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(s.apiKey)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "a valid bearer token is required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), reviewerContextKey{}, s.reviewerID)))
	})
}

func reviewerFromContext(ctx context.Context) string {
	value, _ := ctx.Value(reviewerContextKey{}).(string)
	return value
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
