package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/Tsinling0525/Spider/internal/calendar"
	maildomain "github.com/Tsinling0525/Spider/internal/mail"
)

type MailCapability interface {
	ListThreads(context.Context, maildomain.ListOptions) (maildomain.ThreadPage, error)
	GetThread(context.Context, string) (maildomain.Thread, error)
	Send(context.Context, maildomain.SendRequest) (maildomain.SendReceipt, error)
}

type mailTools struct {
	mail    MailCapability
	account func(context.Context) (string, error)
	cal     calendar.Reader
}

type readTool struct {
	spec ToolSpec
	call func(context.Context, ToolContext, json.RawMessage) (Outcome, error)
}

func (t readTool) Spec() ToolSpec { return t.spec }
func (t readTool) Call(ctx context.Context, run ToolContext, args json.RawMessage) (Outcome, error) {
	return t.call(ctx, run, args)
}

type replyTool struct{ *mailTools }

func NewMailTools(mail MailCapability, account func(context.Context) (string, error), cal calendar.Reader) []Tool {
	m := &mailTools{mail: mail, account: account, cal: cal}
	return []Tool{
		readTool{spec("mail_list_threads", "Search Gmail thread summaries. A search result is not evidence that a reply is needed; inspect conversations. Follow next_page_token when needed.", `{"type":"object","properties":{"query":{"type":"string"},"page_token":{"type":"string"},"page_size":{"type":"integer","minimum":1,"maximum":20}},"additionalProperties":false}`), m.list},
		readTool{spec("mail_get_thread", "Read a conversation and the connected account address. Required before preparing a reply; use a message's id as reply_to_message_id.", `{"type":"object","properties":{"thread_id":{"type":"string"}},"required":["thread_id"],"additionalProperties":false}`), m.thread},
		readTool{spec("calendar_list_events", "Read primary calendar events for scheduling. RFC3339 bounds with timezone are required; maximum interval 31 days. All-day end dates are exclusive. Follow next_page_token before claiming availability.", `{"type":"object","properties":{"time_min":{"type":"string"},"time_max":{"type":"string"},"page_token":{"type":"string"}},"required":["time_min","time_max"],"additionalProperties":false}`), m.events},
		replyTool{m},
	}
}

func spec(name, description, parameters string) ToolSpec {
	return ToolSpec{Name: name, Description: description, Parameters: json.RawMessage(parameters)}
}

func (m *mailTools) list(ctx context.Context, _ ToolContext, raw json.RawMessage) (Outcome, error) {
	var args struct {
		Query     string `json:"query"`
		PageToken string `json:"page_token"`
		PageSize  int64  `json:"page_size"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return Outcome{}, err
	}
	if args.PageSize <= 0 || args.PageSize > 20 {
		args.PageSize = 10
	}
	page, err := m.mail.ListThreads(ctx, maildomain.ListOptions{Query: args.Query, PageToken: args.PageToken, PageSize: args.PageSize})
	return jsonOutcome(page, err)
}

type observedThread struct {
	AccountEmail string            `json:"account_email"`
	Thread       maildomain.Thread `json:"thread"`
}

func (m *mailTools) thread(ctx context.Context, _ ToolContext, raw json.RawMessage) (Outcome, error) {
	var args struct {
		ThreadID string `json:"thread_id"`
	}
	if err := decodeArgs(raw, &args); err != nil || args.ThreadID == "" {
		return Outcome{}, errors.New("thread_id is required")
	}
	thread, err := m.mail.GetThread(ctx, args.ThreadID)
	if err != nil {
		return Outcome{}, err
	}
	account, err := m.account(ctx)
	if err != nil || strings.TrimSpace(account) == "" {
		return Outcome{}, errors.New("cannot identify the connected Gmail account")
	}
	return jsonOutcome(observedThread{AccountEmail: account, Thread: thread}, nil)
}

func (m *mailTools) events(ctx context.Context, _ ToolContext, raw json.RawMessage) (Outcome, error) {
	if m.cal == nil {
		return Outcome{}, errors.New("calendar capability is not configured")
	}
	var opts calendar.ListOptions
	if err := decodeArgs(raw, &opts); err != nil {
		return Outcome{}, err
	}
	page, err := m.cal.ListEvents(ctx, opts)
	return jsonOutcome(page, err)
}

func (t replyTool) Spec() ToolSpec {
	return spec("request_email_reply", "Prepare a plain-text reply for human review. Pauses execution and never sends automatically. Recipient and threading headers come from the inspected message, not model-supplied addresses. No CC or attachments.", `{"type":"object","properties":{"thread_id":{"type":"string"},"reply_to_message_id":{"type":"string"},"subject":{"type":"string"},"body_text":{"type":"string"}},"required":["thread_id","reply_to_message_id","subject","body_text"],"additionalProperties":false}`)
}

func (t replyTool) Call(_ context.Context, run ToolContext, raw json.RawMessage) (Outcome, error) {
	var args struct {
		ThreadID  string `json:"thread_id"`
		MessageID string `json:"reply_to_message_id"`
		Subject   string `json:"subject"`
		BodyText  string `json:"body_text"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return Outcome{}, err
	}
	if strings.TrimSpace(args.Subject) == "" || strings.TrimSpace(args.BodyText) == "" || len(args.BodyText) > 32000 || len(args.Subject) > 998 || strings.ContainsAny(args.Subject, "\r\n") {
		return Outcome{}, errors.New("a valid subject and plain-text body are required (maximum 32000 bytes)")
	}
	var observed observedThread
	found := false
	for i := len(run.Messages) - 1; i >= 0; i-- {
		message := run.Messages[i]
		if message.Role == "tool" && message.Name == "mail_get_thread" {
			var candidate observedThread
			if json.Unmarshal([]byte(message.Content), &candidate) == nil && candidate.Thread.ID == args.ThreadID {
				observed, found = candidate, true
				break
			}
		}
	}
	if !found {
		return Outcome{}, errors.New("inspect this thread with mail_get_thread before preparing a reply")
	}
	for _, message := range observed.Thread.Messages {
		if message.ID != args.MessageID {
			continue
		}
		if message.From.Email == "" || message.MessageID == "" || strings.EqualFold(message.From.Email, observed.AccountEmail) {
			return Outcome{}, errors.New("select an incoming message with a valid sender and Message-ID")
		}
		request := maildomain.SendRequest{
			DraftID: newID(), ThreadID: args.ThreadID, To: []maildomain.Address{message.From},
			Subject: args.Subject, BodyText: args.BodyText, InReplyTo: message.MessageID,
			References:     append(append([]string(nil), message.References...), message.MessageID),
			IdempotencyKey: "agent:" + run.RunID + ":" + newID(),
		}
		encoded, err := json.Marshal(request)
		if err != nil {
			return Outcome{}, err
		}
		return Outcome{Action: &Action{Arguments: encoded, Preview: encoded}}, nil
	}
	return Outcome{}, errors.New("reply_to_message_id must identify a message in the inspected thread")
}

func (t replyTool) ExecuteApproved(ctx context.Context, raw json.RawMessage) (string, error) {
	var request maildomain.SendRequest
	if err := decodeArgs(raw, &request); err != nil {
		return "", err
	}
	// This flag is set only here, after the runtime persists a human decision.
	request.HumanConfirmed = true
	receipt, err := t.mail.Send(ctx, request)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(receipt)
	return string(encoded), err
}

func decodeArgs(raw json.RawMessage, target any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("tool arguments must be a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("unexpected trailing tool arguments")
	}
	return nil
}

func jsonOutcome(value any, err error) (Outcome, error) {
	if err != nil {
		return Outcome{}, err
	}
	encoded, err := json.Marshal(value)
	return Outcome{Content: string(encoded)}, err
}
