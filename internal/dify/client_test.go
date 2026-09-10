package dify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	maildomain "github.com/Tsinling0525/Spider/internal/mail"
)

func TestGenerateParsesStructuredOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatal("missing authorization header")
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"workflow_run_id": "run-1",
			"data": map[string]interface{}{
				"status": "succeeded",
				"outputs": map[string]interface{}{
					"result": `{"should_reply":true,"subject":"Re: Hello","body_text":"Hi"}`,
				},
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "secret", "test-user", server.Client())
	draft, err := client.Generate(context.Background(), maildomain.DraftRequest{ThreadID: "t1"}, maildomain.Thread{
		Messages: []maildomain.Message{{Subject: "Hello", BodyText: "Original"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if draft.WorkflowRunID != "run-1" || draft.BodyText != "Hi" || !draft.ShouldReply {
		t.Fatalf("unexpected draft: %#v", draft)
	}
}

func TestBoundedMessagesRemainValidJSON(t *testing.T) {
	body := make([]byte, maxContextBytes*2)
	for i := range body {
		body[i] = 'x'
	}
	encoded, err := boundedMessages([]maildomain.Message{{BodyText: string(body)}})
	if err != nil {
		t.Fatal(err)
	}
	var messages []maildomain.Message
	if err := json.Unmarshal(encoded, &messages); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(encoded) > maxContextBytes {
		t.Fatalf("context size %d exceeds %d", len(encoded), maxContextBytes)
	}
}
