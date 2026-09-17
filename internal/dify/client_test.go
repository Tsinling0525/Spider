package dify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tsinling0525/Spider/internal/humantask"
	maildomain "github.com/Tsinling0525/Spider/internal/mail"
)

type recordingPauseSink struct{ pauses []humantask.Pause }

func (s *recordingPauseSink) RecordPause(_ context.Context, pause humantask.Pause) error {
	s.pauses = append(s.pauses, pause)
	return nil
}

func TestChatflowRefinesVisibleDraft(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat-messages" {
			t.Errorf("wrong endpoint: %s", r.URL.Path)
		}
		var payload struct {
			Inputs map[string]string `json:"inputs"`
			Query  string            `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(payload.Query, "Manually edited draft") || !strings.Contains(payload.Query, "Shorten it") {
			t.Errorf("missing refinement context: %s", payload.Query)
		}
		if payload.Inputs["email_from"] != "alex@example.com" || !strings.Contains(payload.Inputs["email_body"], "Original") {
			t.Errorf("missing email context: %v", payload.Inputs)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"answer": `{"should_reply":true,"subject":"Re: Hello","body_text":"Hello Alex","warnings":[],"needs_human_input":false}`})
	}))
	defer server.Close()
	client := NewClient(server.URL, "secret", "test", server.Client()).WithMode("chatflow")
	draft, err := client.Generate(context.Background(), maildomain.DraftRequest{
		ThreadID: "t1", UserInstruction: "Shorten it", PreviousDraft: "Manually edited draft",
	}, maildomain.Thread{Messages: []maildomain.Message{{From: maildomain.Address{Email: "alex@example.com"}, Subject: "Hello", BodyText: "Original"}}})
	if err != nil || draft.BodyText != "Hello Alex" {
		t.Fatalf("draft=%+v, err=%v", draft, err)
	}
}

func TestDraftContractRejectsMalformedAndClearsNoReplyBody(t *testing.T) {
	for _, raw := range []string{`{"subject":"Re: Hi","body":"legacy"}`, `{"should_reply":true,"subject":"Hi","body_text":""}`, `{"should_reply":"yes","subject":"Hi","body_text":"Hi"}`} {
		if _, err := decodeDraft([]byte(raw)); err == nil {
			t.Errorf("accepted malformed draft: %s", raw)
		}
	}
	draft, err := decodeDraft([]byte(`{"should_reply":false,"subject":"Hi","body_text":"Do not send this"}`))
	if err != nil || draft.BodyText != "" {
		t.Fatalf("no-reply body was not cleared: %+v %v", draft, err)
	}
}

func TestDecodeDraftAcceptsJSONMarkdownFence(t *testing.T) {
	raw := []byte("```json\n{\"should_reply\":true,\"subject\":\"Re: Hello\",\"body_text\":\"Hello Alex\"}\n```")
	draft, err := decodeDraft(raw)
	if err != nil || !draft.ShouldReply || draft.BodyText != "Hello Alex" {
		t.Fatalf("draft=%+v, err=%v", draft, err)
	}

	wrapped := []byte("{\"result\":\"```json\\n{\\\"should_reply\\\":true,\\\"subject\\\":\\\"Re: Hello\\\",\\\"body_text\\\":\\\"Hello Alex\\\"}\\n```\"}")
	draft, err = decodeDraft(wrapped)
	if err != nil || !draft.ShouldReply || draft.BodyText != "Hello Alex" {
		t.Fatalf("wrapped draft=%+v, err=%v", draft, err)
	}

	if _, err := decodeDraft([]byte("preface\n```json\n{\"should_reply\":true,\"subject\":\"Hi\",\"body_text\":\"Hi\"}\n```")); err == nil {
		t.Fatal("accepted fenced JSON with explanatory text")
	}
}

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

func TestGenerateRecordsPausedHumanInputAndSubmitResumesIt(t *testing.T) {
	var submitted map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/workflows/run" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"workflow_run_id": "run-paused", "data": map[string]interface{}{
					"id": "run-paused", "status": "paused", "reasons": []interface{}{map[string]interface{}{
						"TYPE": "human_input_required", "form_id": "form-1", "form_token": "token-1",
						"node_id": "review", "node_title": "Review payment", "form_content": "Confirm status",
						"inputs":  []interface{}{map[string]interface{}{"type": "paragraph", "output_variable_name": "comment"}},
						"actions": []interface{}{map[string]interface{}{"id": "approve", "title": "Approve"}}, "expiration_time": 2000000000,
					}},
				},
			})
			return
		}
		if r.URL.Path != "/form/human_input/token-1" {
			t.Fatalf("wrong path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{})
	}))
	defer server.Close()
	sink := &recordingPauseSink{}
	client := NewClient(server.URL, "secret", "mantle-user", server.Client()).WithPauseSink(sink)
	_, err := client.Generate(context.Background(), maildomain.DraftRequest{ThreadID: "t1"}, maildomain.Thread{Messages: []maildomain.Message{{BodyText: "Original"}}})
	if err == nil || len(sink.pauses) != 1 || sink.pauses[0].FormToken != "token-1" {
		t.Fatalf("pauses=%+v err=%v", sink.pauses, err)
	}
	if err := client.SubmitHumanInput(context.Background(), "token-1", "approve", map[string]any{"comment": "verified"}); err != nil {
		t.Fatal(err)
	}
	if submitted["action"] != "approve" || submitted["user"] != "mantle-user" {
		t.Fatalf("submitted=%v", submitted)
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
