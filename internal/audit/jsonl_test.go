package audit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	maildomain "github.com/Tsinling0525/Spider/internal/mail"
)

func TestJSONLStorePersistsReceiptAndFinalMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	store, err := NewJSONLStore(path)
	if err != nil {
		t.Fatal(err)
	}
	receipt := maildomain.SendReceipt{ProviderMessageID: "message-1", ThreadID: "thread-1", SentAt: time.Now().UTC()}
	event := maildomain.AuditEvent{
		Type: "message.sent", At: receipt.SentAt.Format(time.RFC3339Nano), IdempotencyKey: "key-1",
		Payload: maildomain.SentAuditPayload{
			Request: maildomain.SendRequest{BodyText: "final edited text", HumanConfirmed: true},
			Receipt: receipt,
		},
	}
	if err := store.Append(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewJSONLStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := reloaded.Receipt(context.Background(), "key-1")
	if err != nil || !ok || got.ProviderMessageID != "message-1" {
		t.Fatalf("receipt was not restored: %#v, %v, %v", got, ok, err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "final edited text") {
		t.Fatal("audit does not include the final edited message")
	}
}
