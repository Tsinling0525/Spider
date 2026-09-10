package mail

import "context"

type Provider interface {
	ListThreads(context.Context, ListOptions) (ThreadPage, error)
	GetThread(context.Context, string) (Thread, error)
	Send(context.Context, SendRequest) (SendReceipt, error)
}

type Drafter interface {
	Generate(context.Context, DraftRequest, Thread) (Draft, error)
}

type AuditEvent struct {
	Type           string      `json:"type"`
	At             string      `json:"at"`
	ThreadID       string      `json:"thread_id,omitempty"`
	DraftID        string      `json:"draft_id,omitempty"`
	WorkflowRunID  string      `json:"workflow_run_id,omitempty"`
	IdempotencyKey string      `json:"idempotency_key,omitempty"`
	Payload        interface{} `json:"payload,omitempty"`
}

type AuditStore interface {
	Append(context.Context, AuditEvent) error
	Receipt(context.Context, string) (SendReceipt, bool, error)
}

type SentAuditPayload struct {
	Request SendRequest `json:"request"`
	Receipt SendReceipt `json:"receipt"`
}
