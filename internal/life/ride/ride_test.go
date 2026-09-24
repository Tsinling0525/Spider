package ride

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func point(lat, lon float64) *Location { return &Location{&lat, &lon} }
func routeInput() Input {
	return Input{Operation: "estimate", Pickup: point(-33.8688, 151.2093), Destination: point(-33.9399, 151.1753)}
}

type fakeProvider struct {
	estimates             []Estimate
	requests, cancels     int
	requestErr, cancelErr error
	requestHook           func()
}

func (f *fakeProvider) Estimates(context.Context, Location, Location) ([]Estimate, error) {
	return f.estimates, nil
}
func (f *fakeProvider) Request(_ context.Context, _ Quote) (Ride, error) {
	f.requests++
	if f.requestHook != nil {
		f.requestHook()
	}
	return Ride{ID: "uber-ride-1", Status: "processing"}, f.requestErr
}
func (f *fakeProvider) Status(_ context.Context, id string) (Ride, error) {
	return Ride{ID: id, Status: "accepted"}, nil
}
func (f *fakeProvider) Cancel(context.Context, string) error {
	f.cancels++
	return f.cancelErr
}
func fixture(t *testing.T) (*Service, *fakeProvider) {
	t.Helper()
	f := &fakeProvider{estimates: []Estimate{{ProductID: "uberx", Name: "UberX", Currency: "AUD", Low: 35, High: 45, Duration: 1200}}}
	s, err := NewService(f, filepath.Join(t.TempDir(), "ride.json"), "sandbox:owner", true)
	if err != nil {
		t.Fatal(err)
	}
	return s, f
}
func quoteInput(t *testing.T, s *Service) Input {
	t.Helper()
	result, err := s.Run(context.Background(), routeInput())
	if err != nil {
		t.Fatal(err)
	}
	q := result.Data.([]Quote)[0]
	return Input{Operation: "request", QuoteID: q.ID, IdempotencyKey: q.ID, Confirmation: "CONFIRM RIDE"}
}

func TestRequestConcurrentAndRestartReplay(t *testing.T) {
	s, f := fixture(t)
	i := quoteInput(t, s)
	f.requestHook = func() {
		b, err := os.ReadFile(s.file)
		if err != nil {
			t.Error(err)
			return
		}
		var saved state
		if json.Unmarshal(b, &saved) != nil || saved.Requests[i.QuoteID].State != "submitting" {
			t.Error("submission must be persisted before calling Uber")
		}
	}
	var wg sync.WaitGroup
	for n := 0; n < 12; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Run(context.Background(), i); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if f.requests != 1 {
		t.Fatalf("submitted %d times", f.requests)
	}
	restarted, err := NewService(f, s.file, s.state.Scope, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.Run(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	if f.requests != 1 {
		t.Fatal("replayed upstream after restart")
	}
	info, err := os.Stat(s.file)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("ride store must be private")
	}
}

func TestUnknownOutcomeBlocksNewQuotesAcrossRestart(t *testing.T) {
	for _, attemptState := range []string{"unknown", "submitting"} {
		t.Run(attemptState, func(t *testing.T) {
			s, f := fixture(t)
			i := quoteInput(t, s)
			f.requestErr = errors.New("connection lost after submission")
			if _, err := s.Run(context.Background(), i); err == nil {
				t.Fatal("expected unknown outcome")
			}
			s.state.Requests[i.QuoteID] = attempt{State: attemptState}
			if err := s.save(); err != nil {
				t.Fatal(err)
			}
			s, err := NewService(f, s.file, s.state.Scope, true)
			if err != nil {
				t.Fatal(err)
			}
			for _, input := range []Input{i, quoteInput(t, s)} {
				if _, err := s.Run(context.Background(), input); err == nil {
					t.Fatal("unknown booking was retried")
				}
			}
			if f.requests != 1 {
				t.Fatal("unknown outcome caused a duplicate ride")
			}
		})
	}
}

func TestRequestGuards(t *testing.T) {
	for _, test := range []string{"confirmation", "idempotency", "expiry", "price", "currency", "disabled", "storage", "route"} {
		t.Run(test, func(t *testing.T) {
			s, f := fixture(t)
			i := quoteInput(t, s)
			switch test {
			case "confirmation":
				i.Confirmation = ""
			case "idempotency":
				i.IdempotencyKey = "different"
			case "expiry":
				q := s.state.Quotes[i.QuoteID]
				q.ExpiresAt = time.Now().Add(-time.Second)
				s.state.Quotes[q.ID] = q
			case "price":
				f.estimates[0].High++
			case "currency":
				f.estimates[0].Currency = "USD"
			case "disabled":
				s.enabled = false
			case "storage":
				s.file = filepath.Join(s.file, "invalid.json")
			case "route":
				i.Pickup = point(0, 0)
			}
			if _, err := s.Run(context.Background(), i); err == nil {
				t.Fatal("expected rejection")
			}
			if f.requests != 0 {
				t.Fatal("guard allowed real request")
			}
		})
	}
}

func TestCancelOwnershipConfirmationAndReplay(t *testing.T) {
	s, f := fixture(t)
	i := Input{Operation: "cancel", RideID: "uber-ride-1", Confirmation: "CONFIRM CANCEL", IdempotencyKey: "uber-ride-1"}
	if _, err := s.Run(context.Background(), i); err == nil {
		t.Fatal("canceled unknown ride")
	}
	if _, err := s.Run(context.Background(), quoteInput(t, s)); err != nil {
		t.Fatal(err)
	}
	bad := i
	bad.Confirmation = ""
	if _, err := s.Run(context.Background(), bad); err == nil {
		t.Fatal("confirmation required")
	}
	for n := 0; n < 2; n++ {
		if _, err := s.Run(context.Background(), i); err != nil {
			t.Fatal(err)
		}
		var err error
		s, err = NewService(f, s.file, s.state.Scope, true)
		if err != nil {
			t.Fatal(err)
		}
	}
	if f.cancels != 1 {
		t.Fatal("cancellation replayed")
	}
}

func TestCancelUnknownDoesNotRetry(t *testing.T) {
	s, f := fixture(t)
	if _, err := s.Run(context.Background(), quoteInput(t, s)); err != nil {
		t.Fatal(err)
	}
	f.cancelErr = errors.New("lost reply")
	i := Input{Operation: "cancel", RideID: "uber-ride-1", Confirmation: "CONFIRM CANCEL", IdempotencyKey: "uber-ride-1"}
	if _, err := s.Run(context.Background(), i); err == nil {
		t.Fatal("expected failure")
	}
	s, err := NewService(f, s.file, s.state.Scope, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Run(context.Background(), i); err == nil {
		t.Fatal("expected unknown")
	}
	if f.cancels != 1 {
		t.Fatal("cancel retried")
	}
}

func TestScopeAndCorruptState(t *testing.T) {
	s, f := fixture(t)
	quoteInput(t, s)
	if _, err := NewService(f, s.file, "production:owner", true); err == nil {
		t.Fatal("cross-environment state accepted")
	}
	if err := os.WriteFile(s.file, []byte(`{"scope":"sandbox:owner","quotes":{},"requests":{"x":{"state":"complete"}},"cancellations":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(f, s.file, "sandbox:owner", true); err == nil {
		t.Fatal("corrupt state accepted")
	}
}

func TestCanceledLockWait(t *testing.T) {
	s, _ := fixture(t)
	s.lock <- struct{}{}
	defer func() { <-s.lock }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.Run(ctx, routeInput()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestHTTPValidationAndAuth(t *testing.T) {
	s, _ := fixture(t)
	m := &MCP{Command: "unused", AccessToken: "secret", User: "owner", Environment: "sandbox"}
	h := PublicHandler(s, m, "api-key")
	for _, tc := range []struct {
		body, token string
		status      int
	}{
		{`{}`, "", 401},
		{`{"operation":"estimate","extra":1}`, "api-key", 400},
		{`{"operation":"estimate","pickup":{"latitude":0},"destination":{"latitude":0,"longitude":0}}`, "api-key", 400},
		{`{"operation":"estimate","pickup":{"latitude":91,"longitude":0},"destination":{"latitude":0,"longitude":0}}`, "api-key", 400},
		{`{} {}`, "api-key", 400},
		{`null`, "api-key", 400},
		{strings.Repeat(" ", 17<<10) + `{}`, "api-key", 400},
		{`{"operation":"estimate","pickup":{"latitude":0,"longitude":0},"destination":{"latitude":0,"longitude":1}}`, "api-key", 200},
	} {
		r := httptest.NewRequest(http.MethodPost, "/life/ride", strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("got %d want %d: %s", w.Code, tc.status, w.Body.String())
		}
	}
	m.AccessToken = ""
	r := httptest.NewRequest(http.MethodPost, "/life/ride", strings.NewReader(`{"operation":"status","ride_id":"r1"}`))
	r.Header.Set("Authorization", "Bearer api-key")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}
