package humantask

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeResolver struct {
	calls int
	err   error
}

type blockingResolver struct {
	started chan struct{}
}

func (r *blockingResolver) SubmitHumanInput(ctx context.Context, _ string, _ string, _ map[string]any) error {
	close(r.started)
	<-ctx.Done()
	return ctx.Err()
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
	claimed, err := reloaded.Claim(tasks[0].ID, Claim{Assignee: "principal:owner", ExpectedVersion: 1, Actor: "principal:owner"})
	if err != nil || claimed.Assignee != "principal:owner" {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	if _, err := reloaded.Decide(context.Background(), tasks[0].ID, Decision{Action: "approve", Inputs: map[string]any{}, ExpectedVersion: 2, IdempotencyKey: "wrong-reviewer", Reviewer: "principal:someone-else"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("wrong reviewer got %v, want forbidden", err)
	}
	resolved, err := reloaded.Decide(context.Background(), tasks[0].ID, Decision{Action: "approve", Inputs: map[string]any{}, ExpectedVersion: 2, IdempotencyKey: "decision-1", Reviewer: "principal:owner"})
	if err != nil || resolved.Status != "resumed" || resolver.calls != 1 {
		t.Fatalf("task=%+v calls=%d err=%v", resolved, resolver.calls, err)
	}
	if _, err := reloaded.Decide(context.Background(), tasks[0].ID, Decision{Action: "approve", ExpectedVersion: 1, IdempotencyKey: "decision-2"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("got %v, want conflict", err)
	}
	duplicate, err := reloaded.Decide(context.Background(), tasks[0].ID, Decision{Action: "approve", ExpectedVersion: 2, IdempotencyKey: "decision-1", Reviewer: "principal:owner"})
	if err != nil || duplicate.Status != "resumed" || resolver.calls != 1 {
		t.Fatalf("duplicate=%+v calls=%d err=%v", duplicate, resolver.calls, err)
	}
}

func TestChangesWakeAfterClaimAndUrgencyComesFromExpiration(t *testing.T) {
	service, err := NewService(filepath.Join(t.TempDir(), "tasks.json"), &fakeResolver{})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Unix(1_000, 0).UTC() }
	if err := service.RecordPause(context.Background(), Pause{WorkflowRunID: "r", FormID: "f", FormToken: "t", ExpirationTime: 1_600, Actions: []Action{{ID: "approve"}}}); err != nil {
		t.Fatal(err)
	}
	before, _ := service.Snapshot()
	if before.Tasks[0].Priority != "high" || before.Tasks[0].SLAStatus != "at-risk" {
		t.Fatalf("task=%+v", before.Tasks[0])
	}
	done := make(chan Snapshot, 1)
	go func() {
		snapshot, _ := service.Changes(context.Background(), before.Revision, time.Second)
		done <- snapshot
	}()
	claimed, err := service.Claim(before.Tasks[0].ID, Claim{Assignee: "reviewer-2", ExpectedVersion: 1, Actor: "reviewer-2"})
	if err != nil || claimed.Assignee != "reviewer-2" {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	select {
	case snapshot := <-done:
		if snapshot.Revision <= before.Revision || snapshot.Tasks[0].Assignee != "reviewer-2" {
			t.Fatalf("snapshot=%+v", snapshot)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("change waiter did not wake")
	}
}

func TestOwnershipIsAuthorizedAndHistoryPersistsWithoutFormValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	service, err := NewService(path, &fakeResolver{})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RecordPause(context.Background(), Pause{WorkflowRunID: "r", FormID: "f", FormToken: "secret", Actions: []Action{{ID: "approve"}}}); err != nil {
		t.Fatal(err)
	}
	tasks, _ := service.List()
	id := tasks[0].ID
	if _, err := service.Claim(id, Claim{Assignee: "principal:bob", ExpectedVersion: 1, Actor: "principal:alice"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("claim for another reviewer got %v, want forbidden", err)
	}
	claimed, err := service.Claim(id, Claim{Assignee: "principal:alice", ExpectedVersion: 1, Actor: "principal:alice"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Claim(id, Claim{Assignee: "principal:bob", ExpectedVersion: claimed.Version, Actor: "principal:bob"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("claim theft got %v, want forbidden", err)
	}
	transferred, err := service.Claim(id, Claim{Assignee: "principal:bob", ExpectedVersion: claimed.Version, Actor: "principal:alice"})
	if err != nil || transferred.Assignee != "principal:bob" {
		t.Fatalf("transfer=%+v err=%v", transferred, err)
	}

	history, err := service.History(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 || history[0].Type != "created" || history[1].Type != "claimed" || history[2].Type != "transferred" {
		t.Fatalf("history=%+v", history)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(contents, []byte("form inputs")) || !bytes.Contains(contents, []byte(`"actor": "principal:alice"`)) {
		t.Fatalf("unexpected persisted audit: %s", contents)
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

func TestReloadQuarantinesAnInterruptedSubmission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	resolver := &blockingResolver{started: make(chan struct{})}
	service, err := NewService(path, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RecordPause(context.Background(), Pause{WorkflowRunID: "r", FormID: "f", FormToken: "t", Actions: []Action{{ID: "approve"}}}); err != nil {
		t.Fatal(err)
	}
	tasks, _ := service.List()
	claimed, err := service.Claim(tasks[0].ID, Claim{Assignee: "principal:owner", ExpectedVersion: 1, Actor: "principal:owner"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = service.Decide(ctx, claimed.ID, Decision{Action: "approve", ExpectedVersion: claimed.Version, IdempotencyKey: "decision-1", Reviewer: "principal:owner"})
		close(done)
	}()
	<-resolver.started

	reloaded, err := NewService(path, &fakeResolver{})
	if err != nil {
		t.Fatal(err)
	}
	recovered, _ := reloaded.Get(claimed.ID)
	if recovered.Status != "failed" || !strings.Contains(recovered.FailureReason, "outcome is unknown") {
		t.Fatalf("recovered=%+v", recovered)
	}
	history, _ := reloaded.History(claimed.ID)
	if history[len(history)-1].Type != "submission_interrupted" {
		t.Fatalf("history=%+v", history)
	}
	cancel()
	<-done
}
