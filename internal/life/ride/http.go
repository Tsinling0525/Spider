package ride

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

func fail(w http.ResponseWriter, err error) {
	var api *apiError
	if !errors.As(err, &api) {
		api = &apiError{502, "ride_upstream_error", "Uber MCP request failed; check configuration, authorization and connectivity"}
	}
	write(w, api.Status, map[string]any{"error": map[string]string{"code": api.Code, "message": api.Message}})
}

func PublicHandler(service *Service, provider *MCP, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /life/ride", func(w http.ResponseWriter, r *http.Request) {
		// Configuration is not proof of a valid OAuth session. An estimate probes it.
		write(w, 200, map[string]any{
			"provider": "uber", "configured": provider.Configured(), "environment": provider.Environment,
			"booking_enabled": service.enabled && provider.Configured(), "authentication_verified": false,
		})
	})
	mux.HandleFunc("POST /life/ride", func(w http.ResponseWriter, r *http.Request) {
		var input Input
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			fail(w, &apiError{400, "invalid_json", "expected one valid ride request JSON object"})
			return
		}
		if err := input.Validate(); err != nil {
			fail(w, &apiError{400, "invalid_request", err.Error()})
			return
		}
		if !provider.Configured() {
			fail(w, &apiError{503, "ride_not_configured", "configure LIFE_UBER_MCP_COMMAND and LIFE_UBER_ACCESS_TOKEN"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 150*time.Second)
		defer cancel()
		result, err := service.Run(ctx, input)
		if err != nil {
			fail(w, err)
			return
		}
		write(w, 200, result)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			fail(w, &apiError{401, "unauthorized", "authorization required"})
			return
		}
		mux.ServeHTTP(w, r)
	})
}
