package agent

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	mailoauth "github.com/Tsinling0525/Spider/internal/oauth"
)

// NewHandler binds a deployment to one authenticated principal. Request bodies
// cannot choose identity, backend credentials, tool permissions or recipients.
func NewHandler(runtime *Runtime, flow *mailoauth.Flow, apiKey, principal string, fallback http.Handler) http.Handler {
	mux := http.NewServeMux()
	auth := func(handler http.HandlerFunc) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if apiKey != "" && subtle.ConstantTimeCompare([]byte(req.Header.Get("Authorization")), []byte("Bearer "+apiKey)) != 1 {
				w.Header().Set("WWW-Authenticate", "Bearer")
				apiError(w, http.StatusUnauthorized, "unauthorized", "a valid bearer token is required")
				return
			}
			handler(w, req)
		})
	}
	mux.Handle("POST /v1/agent/runs", auth(func(w http.ResponseWriter, req *http.Request) {
		var input struct {
			Goal string `json:"goal"`
		}
		if !readJSON(w, req, &input) {
			return
		}
		run, err := runtime.Start(input.Goal, principal)
		replyRun(w, http.StatusAccepted, run, err)
	}))
	mux.Handle("GET /v1/agent/runs", auth(func(w http.ResponseWriter, _ *http.Request) {
		apiJSON(w, http.StatusOK, map[string]any{"runs": runtime.List(principal)})
	}))
	mux.Handle("GET /v1/agent/runs/{run_id}", auth(func(w http.ResponseWriter, req *http.Request) {
		run, err := runtime.Get(req.PathValue("run_id"), principal)
		replyRun(w, http.StatusOK, run, err)
	}))
	mux.Handle("POST /v1/agent/runs/{run_id}/decisions", auth(func(w http.ResponseWriter, req *http.Request) {
		var input struct {
			ApprovalID      string `json:"approval_id"`
			ExpectedVersion int64  `json:"expected_version"`
			Approve         *bool  `json:"approve"`
		}
		if !readJSON(w, req, &input) {
			return
		}
		if input.Approve == nil || input.ApprovalID == "" || input.ExpectedVersion <= 0 {
			replyRun(w, 0, Run{}, ErrInvalid)
			return
		}
		run, err := runtime.Decide(req.PathValue("run_id"), principal, Decision{ApprovalID: input.ApprovalID, ExpectedVersion: input.ExpectedVersion, Approve: *input.Approve})
		replyRun(w, http.StatusAccepted, run, err)
	}))
	for _, action := range []string{"cancel", "resume"} {
		mux.Handle("POST /v1/agent/runs/{run_id}/"+action, auth(func(w http.ResponseWriter, req *http.Request) {
			var input struct {
				ExpectedVersion int64 `json:"expected_version"`
			}
			if !readJSON(w, req, &input) {
				return
			}
			var run Run
			var err error
			if action == "cancel" {
				run, err = runtime.Cancel(req.PathValue("run_id"), principal, input.ExpectedVersion)
			} else {
				run, err = runtime.Resume(req.PathValue("run_id"), principal, input.ExpectedVersion)
			}
			replyRun(w, http.StatusAccepted, run, err)
		}))
	}
	if flow != nil {
		mux.Handle("GET /v1/oauth/google/start", auth(func(w http.ResponseWriter, req *http.Request) {
			var url string
			var err error
			if req.URL.Query().Get("calendar") == "true" {
				url, err = flow.StartWithCalendar(req.URL.Query().Get("send") == "true")
			} else {
				url, err = flow.Start(req.URL.Query().Get("send") == "true")
			}
			if err != nil {
				apiError(w, http.StatusInternalServerError, "oauth_start_failed", "could not start Google authorization")
				return
			}
			apiJSON(w, http.StatusOK, map[string]string{"authorization_url": url})
		}))
	}
	if fallback != nil {
		mux.Handle("/", fallback)
	}
	return mux
}

func replyRun(w http.ResponseWriter, status int, run Run, err error) {
	if err == nil {
		apiJSON(w, status, run)
		return
	}
	switch {
	case errors.Is(err, ErrInvalid):
		apiError(w, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, ErrNotFound):
		apiError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, ErrConflict):
		apiError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, ErrBusy):
		apiError(w, http.StatusServiceUnavailable, "capacity_reached", err.Error())
	default:
		apiError(w, http.StatusInternalServerError, "storage_error", "could not persist agent state")
	}
}

func readJSON(w http.ResponseWriter, req *http.Request, target any) bool {
	req.Body = http.MaxBytesReader(w, req.Body, 16<<10)
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_json", "expected a JSON object with declared fields")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		apiError(w, http.StatusBadRequest, "invalid_json", "unexpected trailing JSON")
		return false
	}
	return true
}

func apiJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func apiError(w http.ResponseWriter, status int, code, message string) {
	apiJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
