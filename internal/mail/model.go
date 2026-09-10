package mail

import "time"

type Address struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email"`
}

type Attachment struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	MIMEType string `json:"mime_type"`
	Size     int64  `json:"size"`
}

type Message struct {
	ID          string       `json:"id"`
	ThreadID    string       `json:"thread_id"`
	From        Address      `json:"from"`
	To          []Address    `json:"to"`
	CC          []Address    `json:"cc,omitempty"`
	Subject     string       `json:"subject"`
	MessageID   string       `json:"message_id,omitempty"`
	References  []string     `json:"references,omitempty"`
	BodyText    string       `json:"body_text"`
	Snippet     string       `json:"snippet,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
	SentAt      time.Time    `json:"sent_at"`
}

type ThreadSummary struct {
	ID           string    `json:"id"`
	Subject      string    `json:"subject"`
	Participants []Address `json:"participants"`
	Snippet      string    `json:"snippet"`
	MessageCount int       `json:"message_count"`
	UpdatedAt    time.Time `json:"updated_at"`
	Unread       bool      `json:"unread"`
}

type Thread struct {
	ID       string    `json:"id"`
	Messages []Message `json:"messages"`
}

type ListOptions struct {
	Query     string
	PageToken string
	PageSize  int64
}

type ThreadPage struct {
	Threads       []ThreadSummary `json:"threads"`
	NextPageToken string          `json:"next_page_token,omitempty"`
}

type DraftRequest struct {
	ThreadID          string `json:"thread_id"`
	UserInstruction   string `json:"user_instruction,omitempty"`
	PreferredLanguage string `json:"preferred_language,omitempty"`
	ToneProfile       string `json:"tone_profile,omitempty"`
	UserSignature     string `json:"user_signature,omitempty"`
}

type Draft struct {
	ID              string    `json:"id"`
	ThreadID        string    `json:"thread_id"`
	ShouldReply     bool      `json:"should_reply"`
	Subject         string    `json:"subject"`
	BodyText        string    `json:"body_text"`
	Confidence      float64   `json:"confidence"`
	Warnings        []string  `json:"warnings"`
	NeedsHumanInput bool      `json:"needs_human_input"`
	WorkflowRunID   string    `json:"workflow_run_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type SendRequest struct {
	DraftID        string    `json:"draft_id,omitempty"`
	ThreadID       string    `json:"thread_id"`
	To             []Address `json:"to"`
	CC             []Address `json:"cc,omitempty"`
	Subject        string    `json:"subject"`
	BodyText       string    `json:"body_text"`
	InReplyTo      string    `json:"in_reply_to,omitempty"`
	References     []string  `json:"references,omitempty"`
	IdempotencyKey string    `json:"idempotency_key"`
	HumanConfirmed bool      `json:"human_confirmed"`
}

type SendReceipt struct {
	ProviderMessageID string    `json:"provider_message_id"`
	ThreadID          string    `json:"thread_id"`
	SentAt            time.Time `json:"sent_at"`
}
