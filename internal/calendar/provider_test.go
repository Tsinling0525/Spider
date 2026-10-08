package calendar

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

type tokens struct{ token *oauth2.Token }

func (s *tokens) Load() (*oauth2.Token, error)   { return s.token, nil }
func (s *tokens) Save(token *oauth2.Token) error { s.token = token; return nil }

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestCalendarReadsBoundedPrimaryEventsAndPreservesPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		query := req.URL.Query()
		if req.Method != http.MethodGet || req.URL.Path != "/calendar/v3/calendars/primary/events" || query.Get("singleEvents") != "true" || query.Get("orderBy") != "startTime" || query.Get("pageToken") != "next" || query.Get("timeMin") != "2026-10-07T00:00:00+11:00" || req.Header.Get("Authorization") != "Bearer calendar-token" {
			t.Errorf("incorrect Calendar request: %s %v", req.URL, req.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"timeZone":"Australia/Sydney","nextPageToken":"more","items":[{"id":"all-day","summary":"Holiday","start":{"date":"2026-10-07"},"end":{"date":"2026-10-08"}},{"id":"meeting","summary":"Meeting","transparency":"transparent","attendees":[{"self":true,"responseStatus":"declined"}],"start":{"dateTime":"2026-10-07T09:00:00+11:00"},"end":{"dateTime":"2026-10-07T10:00:00+11:00"}},{"id":"deleted","status":"cancelled"}]}`))
	}))
	defer server.Close()
	destination, _ := url.Parse(server.URL)
	client := &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		copy := req.Clone(req.Context())
		copy.URL.Scheme, copy.URL.Host = destination.Scheme, destination.Host
		return http.DefaultTransport.RoundTrip(copy)
	})}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, client)
	provider := NewProvider(&oauth2.Config{}, &tokens{token: &oauth2.Token{AccessToken: "calendar-token", Expiry: time.Now().Add(time.Hour)}})
	start, _ := time.Parse(time.RFC3339, "2026-10-07T00:00:00+11:00")
	page, err := provider.ListEvents(ctx, ListOptions{TimeMin: start, TimeMax: start.Add(24 * time.Hour), PageToken: "next"})
	if err != nil || len(page.Events) != 2 || page.NextPageToken != "more" || page.Timezone != "Australia/Sydney" || !page.Events[0].AllDay || page.Events[0].End != "2026-10-08" || page.Events[1].SelfResponse != "declined" || page.Events[1].Transparency != "transparent" {
		t.Fatalf("calendar conversion failed: %+v %v", page, err)
	}
}

func TestCalendarRejectsUnboundedQueriesBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("unexpected network request")
	})})
	provider := NewProvider(&oauth2.Config{}, &tokens{token: &oauth2.Token{AccessToken: "token"}})
	now := time.Now()
	for _, opts := range []ListOptions{{}, {TimeMin: now, TimeMax: now}, {TimeMin: now, TimeMax: now.Add(32 * 24 * time.Hour)}} {
		if _, err := provider.ListEvents(ctx, opts); err == nil {
			t.Fatal("invalid interval accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid query reached provider")
	}
}
