package humantask

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type fakeResolver struct {
	calls int
	err   error
}

func (r *fakeResolver) SubmitHumanInput(context.Context, string, string, map[string]any) error {
	r.calls++
	return r.err
}

func TestPausePersistsAndDecisionIsFirstAnswerWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "human-tasks.json")
	resolver := &fakeResolver{}
	service, err := NewService(path, resolver)
	if err != nil {
		t.Fatal(err)
	}
	pause := Pause{WorkflowRunID: "run-1", FormID: "form-1", FormToken: "secret", NodeID: "review", NodeTitle: "Review", Actions: []Action{{ID: "approve", Title: "Approve"}}}
	if err := service.RecordPause(context.Background(), pause); err != nil {
		t.Fatal(err)
	}
	tasks, err := service.List()
	if err != nil || len(tasks) != 1 || tasks[0].FormToken != "" {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}

	reloaded, err := NewService(path, resolver)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := reloaded.Decide(context.Background(), tasks[0].ID, Decision{Action: "approve", Inputs: map[string]any{}, ExpectedVersion: 1, IdempotencyKey: "decision-1"})
	if err != nil || resolved.Status != "resumed" || resolver.calls != 1 {
		t.Fatalf("task=%+v calls=%d err=%v", resolved, resolver.calls, err)
	}
	if _, err := reloaded.Decide(context.Background(), tasks[0].ID, Decision{Action: "approve", ExpectedVersion: 1, IdempotencyKey: "decision-2"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("got %v, want conflict", err)
	}
	duplicate, err := reloaded.Decide(context.Background(), tasks[0].ID, Decision{Action: "approve", ExpectedVersion: 1, IdempotencyKey: "decision-1"})
	if err != nil || duplicate.Status != "resumed" || resolver.calls != 1 {
		t.Fatalf("duplicate=%+v calls=%d err=%v", duplicate, resolver.calls, err)
	}
}

func TestExpiredPauseCannotBeDecided(t *testing.T) {
	service, err := NewService(filepath.Join(t.TempDir(), "tasks.json"), &fakeResolver{})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Unix(200, 0).UTC() }
	if err := service.RecordPause(context.Background(), Pause{WorkflowRunID: "r", FormID: "f", FormToken: "t", ExpirationTime: 100, Actions: []Action{{ID: "deny"}}}); err != nil {
		t.Fatal(err)
	}
	tasks, _ := service.List()
	if tasks[0].Status != "expired" {
		t.Fatalf("got %s", tasks[0].Status)
	}
}
