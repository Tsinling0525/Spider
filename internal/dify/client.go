package dify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Tsinling0525/Spider/internal/humantask"
	maildomain "github.com/Tsinling0525/Spider/internal/mail"
)

const maxContextBytes = 200_000

type Client struct {
	baseURL    string
	apiKey     string
	user       string
	mode       string
	httpClient *http.Client
	pauseSink  PauseSink
}

type PauseSink interface {
	RecordPause(context.Context, humantask.Pause) error
}

// WithMode selects Dify's Chatflow API; Workflow remains the default.
func (c *Client) WithMode(mode string) *Client {
	c.mode = mode
	return c
}

func (c *Client) WithPauseSink(sink PauseSink) *Client {
	c.pauseSink = sink
	return c
}

func NewClient(baseURL, apiKey, user string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 45 * time.Second}
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, user: user, httpClient: httpClient,
	}
}

func (c *Client) Generate(ctx context.Context, req maildomain.DraftRequest, thread maildomain.Thread) (maildomain.Draft, error) {
	if c.apiKey == "" {
		return maildomain.Draft{}, errors.New("Dify API key is not configured")
	}
	limit := maxContextBytes
	if c.mode == "chatflow" {
		limit = 45_000
	}
	messages, err := boundedMessagesWithLimit(thread.Messages, limit)
	if err != nil {
		return maildomain.Draft{}, err
	}
	subject, participants := threadContext(thread)
	payload := map[string]interface{}{
		"inputs": map[string]interface{}{
			"thread_id": req.ThreadID, "subject": subject, "participants": participants,
			"messages_json": string(messages), "user_instruction": req.UserInstruction,
			"preferred_language": defaultString(req.PreferredLanguage, "auto"),
			"tone_profile":       defaultString(req.ToneProfile, "concise-professional"),
			"user_signature":     req.UserSignature,
			"previous_draft":     req.PreviousDraft,
		},
		"response_mode": "blocking",
		"user":          defaultString(c.user, "spider-mail-service"),
	}
	endpoint := "/workflows/run"
	if c.mode == "chatflow" {
		endpoint = "/chat-messages"
		from := strings.Join(participants, ", ")
		if len(thread.Messages) > 0 {
			from = thread.Messages[len(thread.Messages)-1].From.Email
		}
		payload["inputs"] = map[string]interface{}{
			"email_from": defaultString(from, "Unknown sender"), "email_subject": defaultString(subject, "(no subject)"), "email_body": string(messages),
		}
		query := defaultString(req.UserInstruction, "Draft a concise reply to this email.")
		if req.PreviousDraft != "" {
			query = "Revise the following user-visible draft according to the feedback.\nPrevious draft:\n" + req.PreviousDraft + "\nFeedback:\n" + query
		}
		payload["query"] = query + "\nPreferred language: " + defaultString(req.PreferredLanguage, "auto") +
			"\nTone: " + defaultString(req.ToneProfile, "concise-professional") + "\nUser signature: " + req.UserSignature
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return maildomain.Draft{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return maildomain.Draft{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(httpReq)
	if err != nil {
		return maildomain.Draft{}, fmt.Errorf("run Dify workflow: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return maildomain.Draft{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return maildomain.Draft{}, fmt.Errorf("Dify workflow returned %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if c.mode == "chatflow" {
		var result struct {
			Answer string `json:"answer"`
		}
		if err := json.Unmarshal(responseBody, &result); err != nil {
			return maildomain.Draft{}, fmt.Errorf("decode Dify chat response: %w", err)
		}
		draft, err := decodeDraft([]byte(result.Answer))
		if err != nil {
			return maildomain.Draft{}, err
		}
		return draft, nil
	}
	var result struct {
		WorkflowRunID string `json:"workflow_run_id"`
		Data          struct {
			ID      string            `json:"id"`
			Status  string            `json:"status"`
			Outputs json.RawMessage   `json:"outputs"`
			Error   string            `json:"error"`
			Reasons []json.RawMessage `json:"reasons"`
		} `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return maildomain.Draft{}, fmt.Errorf("decode Dify response: %w", err)
	}
	if result.Data.Status == "paused" {
		workflowRunID := defaultString(result.WorkflowRunID, result.Data.ID)
		if err := c.recordPauses(ctx, workflowRunID, result.Data.Reasons); err != nil {
			return maildomain.Draft{}, err
		}
		return maildomain.Draft{}, fmt.Errorf("Dify workflow %s paused for human input", workflowRunID)
	}
	if result.Data.Status != "" && result.Data.Status != "succeeded" {
		return maildomain.Draft{}, fmt.Errorf("Dify workflow %s: %s", result.Data.Status, result.Data.Error)
	}
	draft, err := decodeDraft(result.Data.Outputs)
	if err != nil {
		return maildomain.Draft{}, err
	}
	draft.WorkflowRunID = defaultString(result.WorkflowRunID, result.Data.ID)
	return draft, nil
}

func (c *Client) recordPauses(ctx context.Context, workflowRunID string, reasons []json.RawMessage) error {
	if c.pauseSink == nil {
		return errors.New("Dify workflow paused but no human task sink is configured")
	}
	recorded := 0
	for _, raw := range reasons {
		var reason struct {
			Type                  string             `json:"TYPE"`
			FormID                string             `json:"form_id"`
			FormToken             string             `json:"form_token"`
			NodeID                string             `json:"node_id"`
			NodeTitle             string             `json:"node_title"`
			FormContent           string             `json:"form_content"`
			Inputs                []humantask.Input  `json:"inputs"`
			Actions               []humantask.Action `json:"actions"`
			ResolvedDefaultValues map[string]any     `json:"resolved_default_values"`
			ExpirationTime        int64              `json:"expiration_time"`
		}
		if json.Unmarshal(raw, &reason) != nil || reason.Type != "human_input_required" {
			continue
		}
		if err := c.pauseSink.RecordPause(ctx, humantask.Pause{
			WorkflowRunID: workflowRunID, FormID: reason.FormID, FormToken: reason.FormToken,
			NodeID: reason.NodeID, NodeTitle: reason.NodeTitle, FormContent: reason.FormContent,
			Inputs: reason.Inputs, Actions: reason.Actions,
			ResolvedDefaultValues: reason.ResolvedDefaultValues, ExpirationTime: reason.ExpirationTime,
		}); err != nil {
			return fmt.Errorf("record Dify human input: %w", err)
		}
		recorded++
	}
	if recorded == 0 {
		return errors.New("Dify workflow paused without an actionable WebApp human-input form")
	}
	return nil
}

func (c *Client) SubmitHumanInput(ctx context.Context, formToken, action string, inputs map[string]any) error {
	if c.apiKey == "" {
		return errors.New("Dify API key is not configured")
	}
	payload, err := json.Marshal(map[string]any{
		"inputs": inputs, "action": action, "user": defaultString(c.user, "spider-mail-service"),
	})
	if err != nil {
		return err
	}
	endpoint := c.baseURL + "/form/human_input/" + url.PathEscape(formToken)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Dify human input returned %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func boundedMessages(messages []maildomain.Message) ([]byte, error) {
	return boundedMessagesWithLimit(messages, maxContextBytes)
}

func boundedMessagesWithLimit(messages []maildomain.Message, limit int) ([]byte, error) {
	copyOfMessages := append([]maildomain.Message(nil), messages...)
	for {
		encoded, err := json.Marshal(copyOfMessages)
		if err != nil {
			return nil, err
		}
		if len(encoded) <= limit {
			return encoded, nil
		}
		if len(copyOfMessages) > 1 {
			copyOfMessages = copyOfMessages[1:]
			continue
		}
		if len(copyOfMessages) == 0 {
			return []byte("[]"), nil
		}
		body := copyOfMessages[0].BodyText
		if len(body) <= 1_000 {
			return nil, errors.New("message context cannot be bounded")
		}
		copyOfMessages[0].BodyText = body[len(body)/2:]
	}
}

func decodeDraft(raw json.RawMessage) (maildomain.Draft, error) {
	raw = normalizeDraftJSON(raw)
	var direct maildomain.Draft
	if isDraftObject(raw) && json.Unmarshal(raw, &direct) == nil {
		return validateDraft(direct)
	}
	var outputs map[string]json.RawMessage
	if err := json.Unmarshal(raw, &outputs); err != nil {
		return maildomain.Draft{}, fmt.Errorf("decode Dify outputs: %w", err)
	}
	for _, key := range []string{"result", "output", "draft"} {
		value, ok := outputs[key]
		if !ok {
			continue
		}
		if isDraftObject(value) && json.Unmarshal(value, &direct) == nil {
			return validateDraft(direct)
		}
		var encoded string
		if json.Unmarshal(value, &encoded) != nil {
			continue
		}
		candidate := normalizeDraftJSON([]byte(encoded))
		if isDraftObject(candidate) && json.Unmarshal(candidate, &direct) == nil {
			return validateDraft(direct)
		}
	}
	return maildomain.Draft{}, errors.New("Dify outputs do not contain a structured draft")
}

// normalizeDraftJSON accepts the common model response shape where a JSON
// object is wrapped in a single Markdown fence. Text outside the fence remains
// invalid so explanatory model output cannot be mistaken for a draft.
func normalizeDraftJSON(raw []byte) []byte {
	trimmed := bytes.TrimSpace(raw)
	if !bytes.HasPrefix(trimmed, []byte("```")) {
		return trimmed
	}
	contentStart := bytes.IndexByte(trimmed, '\n')
	contentEnd := bytes.LastIndex(trimmed, []byte("```"))
	if contentStart < 0 || contentEnd <= contentStart || len(bytes.TrimSpace(trimmed[contentEnd+3:])) != 0 {
		return trimmed
	}
	return bytes.TrimSpace(trimmed[contentStart+1 : contentEnd])
}

func validateDraft(draft maildomain.Draft) (maildomain.Draft, error) {
	if draft.ShouldReply && strings.TrimSpace(draft.BodyText) == "" {
		return maildomain.Draft{}, errors.New("Dify returned a reply decision without a body")
	}
	if !draft.ShouldReply {
		draft.BodyText = ""
	}
	if draft.Warnings == nil {
		draft.Warnings = []string{}
	}
	return draft, nil
}

func isDraftObject(raw []byte) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return false
	}
	var decision bool
	var body, subject string
	return json.Unmarshal(object["should_reply"], &decision) == nil && string(object["should_reply"]) != "null" &&
		json.Unmarshal(object["body_text"], &body) == nil && string(object["body_text"]) != "null" &&
		json.Unmarshal(object["subject"], &subject) == nil && string(object["subject"]) != "null"
}

func threadContext(thread maildomain.Thread) (string, []string) {
	var subject string
	seen := make(map[string]bool)
	var participants []string
	for _, message := range thread.Messages {
		if subject == "" {
			subject = message.Subject
		}
		addresses := append([]maildomain.Address{message.From}, message.To...)
		for _, address := range addresses {
			if address.Email != "" && !seen[address.Email] {
				seen[address.Email] = true
				participants = append(participants, address.Email)
			}
		}
	}
	return subject, participants
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
