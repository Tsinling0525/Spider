package mail

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeProvider struct {
	thread    Thread
	receipt   SendReceipt
	sendCalls int
}

func (f *fakeProvider) ListThreads(context.Context, ListOptions) (ThreadPage, error) {
	return ThreadPage{}, nil
}
func (f *fakeProvider) GetThread(context.Context, string) (Thread, error) { return f.thread, nil }
func (f *fakeProvider) Send(context.Context, SendRequest) (SendReceipt, error) {
	f.sendCalls++
	return f.receipt, nil
}

type fakeDrafter struct{ draft Draft }

func (f fakeDrafter) Generate(context.Context, DraftRequest, Thread) (Draft, error) {
	return f.draft, nil
}

type memoryAudit struct {
	mu       sync.Mutex
	receipts map[string]SendReceipt
}

func (m *memoryAudit) Append(_ context.Context, event AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if event.Type == "message.sent" {
		m.receipts[event.IdempotencyKey] = event.Payload.(SentAuditPayload).Receipt
	}
	return nil
}
func (m *memoryAudit) Receipt(_ context.Context, key string) (SendReceipt, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt, ok := m.receipts[key]
	return receipt, ok, nil
}

func TestSendRequiresHumanConfirmation(t *testing.T) {
	provider := &fakeProvider{}
	service := NewService(provider, fakeDrafter{}, &memoryAudit{receipts: make(map[string]SendReceipt)})
	_, err := service.Send(context.Background(), SendRequest{IdempotencyKey: "key"})
	if !errors.Is(err, ErrHumanConfirmationRequired) {
		t.Fatalf("expected human confirmation error, got %v", err)
	}
}

func TestSendIsIdempotent(t *testing.T) {
	receipt := SendReceipt{ProviderMessageID: "m1", ThreadID: "t1", SentAt: time.Now()}
	provider := &fakeProvider{receipt: receipt}
	service := NewService(provider, fakeDrafter{}, &memoryAudit{receipts: make(map[string]SendReceipt)})
	request := SendRequest{
		ThreadID: "t1", To: []Address{{Email: "a@example.com"}}, BodyText: "hello",
		IdempotencyKey: "same-key", HumanConfirmed: true,
	}
	for range 2 {
		got, err := service.Send(context.Background(), request)
		if err != nil || got.ProviderMessageID != receipt.ProviderMessageID {
			t.Fatalf("unexpected send result: %#v, %v", got, err)
		}
	}
	if provider.sendCalls != 1 {
		t.Fatalf("provider called %d times, want 1", provider.sendCalls)
	}
}

func TestGenerateDraftAddsIdentityAndAuditFields(t *testing.T) {
	provider := &fakeProvider{thread: Thread{ID: "t1"}}
	audit := &memoryAudit{receipts: make(map[string]SendReceipt)}
	service := NewService(provider, fakeDrafter{draft: Draft{ShouldReply: true, BodyText: "reply"}}, audit)
	draft, err := service.GenerateDraft(context.Background(), DraftRequest{ThreadID: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	if draft.ID == "" || draft.ThreadID != "t1" || draft.CreatedAt.IsZero() || draft.Warnings == nil {
		t.Fatalf("draft fields were not completed: %#v", draft)
	}
}
