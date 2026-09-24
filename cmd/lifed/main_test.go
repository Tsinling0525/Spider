package main

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Tsinling0525/Spider/internal/life/ride"
)

func TestPublicRoutingAndSharedAuthentication(t *testing.T) {
	provider := &ride.MCP{Environment: "sandbox"}
	service, err := ride.NewService(provider, filepath.Join(t.TempDir(), "ride.json"), "sandbox:owner", false)
	if err != nil {
		t.Fatal(err)
	}
	h := publicHandler(nil, nil, service, provider, "key")
	for _, path := range []string{"/health", "/life/food", "/life/ride"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("%s lacks authentication: %d", path, w.Code)
		}
	}
	for _, path := range []string{"/health", "/life/ride"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer key")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s not routed: %d", path, w.Code)
		}
	}
}
