package food

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type heldAuth struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (p *heldAuth) Call(ctx context.Context, name string, args, out any) error {
	if p.calls.Add(1) == 1 {
		close(p.started)
	}
	select {
	case <-p.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return json.Unmarshal([]byte(`{"isLoggedIn":true,"deliveryAddress":"Example Street"}`), out)
}
func TestConnectionReturnsImmediatelyAndCoalescesRefresh(t *testing.T) {
	p := &heldAuth{started: make(chan struct{}), release: make(chan struct{})}
	defer close(p.release)
	a, _ := NewAdapter(p, filepath.Join(t.TempDir(), "state"))
	dify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer dify.Close()
	c := &connectionCache{workflow: &Workflow{APIKey: "key", BaseURL: dify.URL}, adapter: a}
	first := c.get()
	if first.Ready || !first.Refreshing {
		t.Fatal("cold status must be pending")
	}
	select {
	case <-p.started:
	case <-time.After(time.Second):
		t.Fatal("no background refresh")
	}
	for range 20 {
		if !c.get().Refreshing {
			t.Fatal("pending state lost")
		}
	}
	if p.calls.Load() != 1 {
		t.Fatal("duplicate refresh")
	}
	// Holding the browser lock cannot block a GET.
	start := time.Now()
	c.get()
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("GET waited for browser")
	}
}
func TestConnectionFreshCacheAndExpiry(t *testing.T) {
	c := &connectionCache{workflow: &Workflow{APIKey: "key"}, value: connectionStatus{Ready: true}, expires: time.Now().Add(time.Minute)}
	if !c.get().Ready {
		t.Fatal("fresh cache ignored")
	}
	c.expires = time.Now().Add(-time.Second)
	c.running = true
	if status := c.get(); status.Ready || !status.Refreshing {
		t.Fatal("stale readiness presented as current")
	}
}
func TestBrowserLockWaitHonorsCancellation(t *testing.T) {
	a, _ := NewAdapter(&fakeProvider{}, filepath.Join(t.TempDir(), "state"))
	if err := a.lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer a.unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := a.Auth(ctx)
	if err == nil || time.Since(start) > time.Second {
		t.Fatal("cancelled waiter did not exit")
	}
}
