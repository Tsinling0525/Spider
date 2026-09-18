package food

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, err error) {
	write(w, status, map[string]any{"error": map[string]string{"code": "food_error", "message": err.Error()}})
}
func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		fail(w, 400, errors.New("invalid food request JSON"))
		return false
	}
	if d.Decode(&struct{}{}) != io.EOF {
		fail(w, 400, errors.New("only one JSON object is allowed"))
		return false
	}
	return true
}
func auth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			fail(w, 401, errors.New("authorization required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
func PublicHandler(workflow *Workflow, adapter *Adapter, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]string{"service": "lifed", "status": "ok"})
	})
	status := &connectionCache{workflow: workflow, adapter: adapter}
	mux.HandleFunc("GET /life/food", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, status.get())
	})
	mux.HandleFunc("POST /life/food", func(w http.ResponseWriter, r *http.Request) {
		var input Input
		if !decode(w, r, &input) {
			return
		}
		if err := input.Validate(); err != nil {
			fail(w, 400, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 240*time.Second)
		defer cancel()
		result, err := workflow.Run(ctx, input)
		if err != nil {
			fail(w, 502, err)
			return
		}
		write(w, 200, result)
	})
	return auth(token, mux)
}
func AdapterHandler(adapter *Adapter, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /food/search", func(w http.ResponseWriter, r *http.Request) {
		var i Input
		if !decode(w, r, &i) {
			return
		}
		i.Operation = "search"
		if err := i.Validate(); err != nil {
			fail(w, 400, err)
			return
		}
		v, err := adapter.Search(r.Context(), i)
		if err != nil {
			fail(w, 502, err)
			return
		}
		write(w, 200, v)
	})
	mux.HandleFunc("POST /food/quote", func(w http.ResponseWriter, r *http.Request) {
		var i Input
		if !decode(w, r, &i) {
			return
		}
		i.Operation = "quote"
		if err := i.Validate(); err != nil {
			fail(w, 400, err)
			return
		}
		v, err := adapter.Quote(r.Context(), i)
		if err != nil {
			fail(w, 409, err)
			return
		}
		write(w, 200, v)
	})
	mux.HandleFunc("POST /food/place-order", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			QuoteID      string `json:"quote_id"`
			Confirmation string `json:"confirmation"`
		}
		if !decode(w, r, &input) {
			return
		}
		if input.QuoteID == "" || input.QuoteID != r.Header.Get("Idempotency-Key") || input.Confirmation != "CONFIRM PURCHASE" {
			fail(w, 400, errors.New("quote-bound idempotency and explicit approval are required"))
			return
		}
		v, err := adapter.Place(r.Context(), input.QuoteID, input.Confirmation)
		if err != nil {
			fail(w, 409, err)
			return
		}
		write(w, 200, v)
	})
	mux.HandleFunc("GET /food/orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := adapter.Status(r.Context(), r.PathValue("id"))
		if err != nil {
			fail(w, 502, err)
			return
		}
		write(w, 200, v)
	})
	// This listener is reachable by Dify containers and must never run unauthenticated.
	if token == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fail(w, 503, errors.New("adapter authentication is not configured"))
		})
	}
	return auth(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(ctx))
	}))
}
