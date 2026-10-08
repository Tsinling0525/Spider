package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tsinling0525/Spider/internal/audit"
	"github.com/Tsinling0525/Spider/internal/calendar"
	maildomain "github.com/Tsinling0525/Spider/internal/mail"
)

type backendFunc func(context.Context, []Message, []ToolSpec) (Message, error)

func (f backendFunc) Next(ctx context.Context, messages []Message, tools []ToolSpec) (Message, error) {
	return f(ctx, messages, tools)
}

type testMail struct {
	sends   atomic.Int32
	mu      sync.Mutex
	last    maildomain.SendRequest
	sendErr error
}

func (m *testMail) ListThreads(_ context.Context, opts maildomain.ListOptions) (maildomain.ThreadPage, error) {
	return maildomain.ThreadPage{Threads: []maildomain.ThreadSummary{{ID: "thread-1", Subject: "Meeting"}}}, nil
}

func (m *testMail) GetThread(_ context.Context, id string) (maildomain.Thread, error) {
	return maildomain.Thread{ID: id, Messages: []maildomain.Message{
		{ID: "incoming-1", MessageID: "<incoming@example.com>", From: maildomain.Address{Email: "colleague@example.com"}, BodyText: "Can we meet tomorrow?"},
		{ID: "own-1", MessageID: "<own@example.com>", From: maildomain.Address{Email: "owner@example.com"}},
	}}, nil
}

func (m *testMail) Send(_ context.Context, request maildomain.SendRequest) (maildomain.SendReceipt, error) {
	m.sends.Add(1)
	m.mu.Lock()
	m.last = request
	m.mu.Unlock()
	if m.sendErr != nil {
		return maildomain.SendReceipt{}, m.sendErr
	}
	return maildomain.SendReceipt{ProviderMessageID: "sent-1", ThreadID: request.ThreadID, SentAt: time.Now().UTC()}, nil
}

type testCalendar struct{ reads atomic.Int32 }

func (c *testCalendar) ListEvents(_ context.Context, opts calendar.ListOptions) (calendar.Page, error) {
	if opts.TimeMin.IsZero() || !opts.TimeMax.After(opts.TimeMin) {
		return calendar.Page{}, errors.New("invalid bounds")
	}
	c.reads.Add(1)
	return calendar.Page{Events: []calendar.Event{{ID: "busy-1", Summary: "Busy", Start: "2026-10-07T09:00:00+11:00", End: "2026-10-07T10:00:00+11:00"}}, Timezone: "Australia/Sydney"}, nil
}

func toolTurn(id, name, args string) Message {
	return Message{Role: "assistant", ToolCalls: []ToolCall{{ID: id, Type: "function", Function: FunctionCall{Name: name, Arguments: args}}}}
}

func mailScript() backendFunc {
	var index atomic.Int32
	return func(_ context.Context, messages []Message, _ []ToolSpec) (Message, error) {
		switch index.Add(1) {
		case 1:
			return toolTurn("list", "mail_list_threads", `{"query":"in:inbox","page_size":5}`), nil
		case 2:
			return toolTurn("thread", "mail_get_thread", `{"thread_id":"thread-1"}`), nil
		case 3:
			return toolTurn("calendar", "calendar_list_events", `{"time_min":"2026-10-07T00:00:00+11:00","time_max":"2026-10-08T00:00:00+11:00"}`), nil
		case 4:
			return toolTurn("reply", "request_email_reply", `{"thread_id":"thread-1","reply_to_message_id":"incoming-1","subject":"Re: Meeting","body_text":"I am available after 10am."}`), nil
		default:
			last := messages[len(messages)-1]
			var receipt maildomain.SendReceipt
			if json.Unmarshal([]byte(last.Content), &receipt) == nil && receipt.ProviderMessageID == "sent-1" {
				return Message{Role: "assistant", Content: "Reply sent after your approval."}, nil
			}
			return Message{Role: "assistant", Content: "Reply was rejected; no mail was sent."}, nil
		}
	}
}

func newMailRuntime(t *testing.T, path string, backend Backend, opts Options) (*Runtime, *testMail, *testCalendar) {
	t.Helper()
	mail := &testMail{}
	cal := &testCalendar{}
	audits, err := audit.NewJSONLStore(filepath.Join(filepath.Dir(path), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	service := maildomain.NewService(mail, nil, audits)
	runtime, err := NewRuntime(path, backend, NewMailTools(service, func(context.Context) (string, error) { return "owner@example.com", nil }, cal), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return runtime, mail, cal
}

func waitStatus(t *testing.T, runtime *Runtime, id, status string) Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run, err := runtime.Get(id, "owner")
		if err != nil {
			t.Fatal(err)
		}
		if run.Status == status {
			return run
		}
		if run.Status == "failed" && status != "failed" {
			t.Fatalf("run failed: %s", run.Error)
		}
		time.Sleep(2 * time.Millisecond)
	}
	run, _ := runtime.Get(id, "owner")
	t.Fatalf("waiting for %s: %+v", status, run)
	return Run{}
}

func TestMailAgentPlanningApprovalAndConcurrentRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.json")
	runtime, mail, cal := newMailRuntime(t, path, mailScript(), Options{})
	run, err := runtime.Start("Read mail and calendar, draft a meeting reply, send after review", "owner")
	if err != nil {
		t.Fatal(err)
	}
	pending := waitStatus(t, runtime, run.ID, "waiting_approval")
	if mail.sends.Load() != 0 || cal.reads.Load() != 1 {
		t.Fatal("tools did not respect read-only planning")
	}
	var preview maildomain.SendRequest
	if err := json.Unmarshal(pending.Pending.Preview, &preview); err != nil {
		t.Fatal(err)
	}
	if preview.To[0].Email != "colleague@example.com" || preview.HumanConfirmed || preview.InReplyTo != "<incoming@example.com>" || preview.IdempotencyKey == "" {
		t.Fatalf("invalid approval preview: %+v", preview)
	}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := runtime.Decide(run.ID, "owner", Decision{ApprovalID: pending.Pending.ID, ExpectedVersion: pending.Version, Approve: true})
			if err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, ErrConflict) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	completed := waitStatus(t, runtime, run.ID, "completed")
	if accepted.Load() != 1 || mail.sends.Load() != 1 || completed.Steps != 5 {
		t.Fatalf("approval replayed: accepted=%d sends=%d steps=%d", accepted.Load(), mail.sends.Load(), completed.Steps)
	}
	mail.mu.Lock()
	defer mail.mu.Unlock()
	if !mail.last.HumanConfirmed || mail.last.BodyText != preview.BodyText || mail.last.IdempotencyKey != preview.IdempotencyKey {
		t.Fatal("executed content differs from approved content")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("store must be private: %v %v", info, err)
	}
}

func TestPendingApprovalSurvivesRestartAndRejectionContinues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.json")
	backend := mailScript()
	runtime, originalMail, _ := newMailRuntime(t, path, backend, Options{})
	run, _ := runtime.Start("Draft a reply", "owner")
	pending := waitStatus(t, runtime, run.ID, "waiting_approval")
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted, mail, _ := newMailRuntime(t, path, backend, Options{})
	recovered, err := restarted.Get(run.ID, "owner")
	if err != nil || recovered.Pending.ID != pending.Pending.ID {
		t.Fatal("approval did not survive restart")
	}
	_, err = restarted.Decide(run.ID, "owner", Decision{ApprovalID: pending.Pending.ID, ExpectedVersion: pending.Version, Approve: false})
	if err != nil {
		t.Fatal(err)
	}
	completed := waitStatus(t, restarted, run.ID, "completed")
	if mail.sends.Load()+originalMail.sends.Load() != 0 || completed.Pending != nil {
		t.Fatal("rejection sent mail or left pending approval")
	}
}

func TestRestartDoesNotReplayUncertainSendAndReadOnlyResumeWorks(t *testing.T) {
	for _, status := range []string{"sending", "running"} {
		t.Run(status, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "runs.json")
			run := Run{ID: "recovery", Principal: "owner", Status: status, Version: 3,
				Messages: []Message{{Role: "user", Content: "Read mail"}, toolTurn("interrupted-call", "mail_list_threads", `{}`)},
			}
			if err := (fileStore{path}).save(map[string]Run{run.ID: run}); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			runtime, mail, _ := newMailRuntime(t, path, backendFunc(func(_ context.Context, messages []Message, _ []ToolSpec) (Message, error) {
				calls.Add(1)
				if messages[len(messages)-1].Role != "tool" {
					return Message{}, errors.New("interrupted call was not resolved")
				}
				return Message{Role: "assistant", Content: "Resumed."}, nil
			}), Options{})
			recovered, _ := runtime.Get(run.ID, "owner")
			if status == "sending" {
				if recovered.Status != "failed" || calls.Load() != 0 || mail.sends.Load() != 0 {
					t.Fatal("uncertain send was replayed")
				}
				if _, err := runtime.Resume(run.ID, "owner", recovered.Version); !errors.Is(err, ErrConflict) {
					t.Fatalf("uncertain writes must not resume: %v", err)
				}
			} else {
				if recovered.Status != "interrupted" {
					t.Fatal(recovered.Status)
				}
				if _, err := runtime.Resume(run.ID, "owner", recovered.Version); err != nil {
					t.Fatal(err)
				}
				waitStatus(t, runtime, run.ID, "completed")
			}
		})
	}
}

func TestCancelAbortsModelAndStaleVersionsCannotChangeRun(t *testing.T) {
	entered := make(chan struct{})
	exited := make(chan struct{})
	runtime, mail, _ := newMailRuntime(t, filepath.Join(t.TempDir(), "runs.json"), backendFunc(func(ctx context.Context, _ []Message, _ []ToolSpec) (Message, error) {
		close(entered)
		<-ctx.Done()
		close(exited)
		return Message{}, ctx.Err()
	}), Options{})
	run, _ := runtime.Start("Read mail", "owner")
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("model did not start")
	}
	if _, err := runtime.Cancel(run.ID, "owner", run.Version); !errors.Is(err, ErrConflict) {
		t.Fatal("stale version accepted")
	}
	current, _ := runtime.Get(run.ID, "owner")
	if _, err := runtime.Cancel(run.ID, "owner", current.Version); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("model context was not cancelled")
	}
	waitStatus(t, runtime, run.ID, "cancelled")
	if mail.sends.Load() != 0 {
		t.Fatal("cancel sent mail")
	}
	if _, err := runtime.Get(run.ID, "attacker"); !errors.Is(err, ErrNotFound) {
		t.Fatal("run leaked across principals")
	}
}

func TestToolErrorsFeedBackAndStepBudgetIsEnforced(t *testing.T) {
	var calls atomic.Int32
	runtime, _, _ := newMailRuntime(t, filepath.Join(t.TempDir(), "runs.json"), backendFunc(func(_ context.Context, messages []Message, _ []ToolSpec) (Message, error) {
		n := calls.Add(1)
		if n == 2 && messages[len(messages)-1].Content != `{"error":"unknown tool"}` {
			t.Error("tool failure not observed by model")
		}
		return toolTurn(string(rune('a'+n)), "missing_tool", `{}`), nil
	}), Options{MaxSteps: 2})
	run, _ := runtime.Start("Try tools", "owner")
	failed := waitStatus(t, runtime, run.ID, "failed")
	if failed.Steps != 2 || calls.Load() != 2 || failed.Error != "agent step budget exhausted" {
		t.Fatalf("budget not enforced: %+v", failed)
	}
}

func TestReplyToolRequiresObservedIncomingMessageAndRejectsInjectedFields(t *testing.T) {
	mail := &testMail{}
	tools := NewMailTools(mail, func(context.Context) (string, error) { return "owner@example.com", nil }, nil)
	reply := tools[len(tools)-1]
	args := json.RawMessage(`{"thread_id":"thread-1","reply_to_message_id":"incoming-1","subject":"Re: Meeting","body_text":"Hello"}`)
	if _, err := reply.Call(context.Background(), ToolContext{}, args); err == nil {
		t.Fatal("unobserved thread accepted")
	}
	thread, _ := mail.GetThread(context.Background(), "thread-1")
	encoded, _ := json.Marshal(observedThread{AccountEmail: "owner@example.com", Thread: thread})
	observed := ToolContext{RunID: "run-1", Messages: []Message{{Role: "tool", Name: "mail_get_thread", Content: string(encoded)}}}
	for _, invalid := range []string{
		`{"thread_id":"thread-1","reply_to_message_id":"incoming-1","subject":"Reply","body_text":"Hello","human_confirmed":true}`,
		`{"thread_id":"thread-1","reply_to_message_id":"incoming-1","subject":"Reply","body_text":"Hello","to":[{"email":"attacker@example.com"}]}`,
		`{"thread_id":"thread-1","reply_to_message_id":"own-1","subject":"Reply","body_text":"Hello"}`,
	} {
		if _, err := reply.Call(context.Background(), observed, json.RawMessage(invalid)); err == nil {
			t.Fatalf("unsafe arguments accepted: %s", invalid)
		}
	}
	if outcome, err := reply.Call(context.Background(), observed, args); err != nil || outcome.Action == nil || mail.sends.Load() != 0 {
		t.Fatalf("valid draft did not pause safely: %+v %v", outcome, err)
	}
}

func TestApprovalPersistenceFailureCannotTriggerSend(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "data")
	runtime, mail, _ := newMailRuntime(t, filepath.Join(dir, "runs.json"), mailScript(), Options{})
	run, _ := runtime.Start("Draft reply", "owner")
	pending := waitStatus(t, runtime, run.ID, "waiting_approval")
	// Replace the store directory with a regular file to simulate a disk error.
	backup := filepath.Join(root, "backup")
	if err := os.Rename(dir, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("unwritable store"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := runtime.Decide(run.ID, "owner", Decision{ApprovalID: pending.Pending.ID, ExpectedVersion: pending.Version, Approve: true})
	if err == nil || mail.sends.Load() != 0 {
		t.Fatal("send was allowed without a durable approval")
	}
	current, _ := runtime.Get(run.ID, "owner")
	if current.Status != "waiting_approval" || current.Version != pending.Version {
		t.Fatal("failed approval mutated the in-memory state")
	}
}

func TestFailedSendCannotBeApprovedOrResumedAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.json")
	runtime, mail, _ := newMailRuntime(t, path, mailScript(), Options{})
	mail.sendErr = errors.New("connection lost after request; outcome unknown")
	run, _ := runtime.Start("Draft reply", "owner")
	pending := waitStatus(t, runtime, run.ID, "waiting_approval")
	_, err := runtime.Decide(run.ID, "owner", Decision{ApprovalID: pending.Pending.ID, ExpectedVersion: pending.Version, Approve: true})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitStatus(t, runtime, run.ID, "failed")
	if mail.sends.Load() != 1 || failed.Pending == nil {
		t.Fatal("failed send did not retain its uncertain action")
	}
	if _, err := runtime.Decide(run.ID, "owner", Decision{ApprovalID: pending.Pending.ID, ExpectedVersion: failed.Version, Approve: true}); !errors.Is(err, ErrConflict) {
		t.Fatal("failed action could be approved again")
	}
	if _, err := runtime.Resume(run.ID, "owner", failed.Version); !errors.Is(err, ErrConflict) {
		t.Fatal("failed action could resume")
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted, nextMail, _ := newMailRuntime(t, path, mailScript(), Options{})
	current, _ := restarted.Get(run.ID, "owner")
	if current.Status != "failed" || nextMail.sends.Load() != 0 {
		t.Fatal("restart replayed failed send")
	}
}
