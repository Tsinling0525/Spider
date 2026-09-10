package dify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	maildomain "github.com/Tsinling0525/Spider/internal/mail"
)

const maxContextBytes = 200_000

type Client struct {
	baseURL    string
	apiKey     string
	user       string
	httpClient *http.Client
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
	messages, err := boundedMessages(thread.Messages)
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
		},
		"response_mode": "blocking",
		"user":          defaultString(c.user, "spider-mail-service"),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return maildomain.Draft{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/workflows/run", bytes.NewReader(body))
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
	var result struct {
		WorkflowRunID string `json:"workflow_run_id"`
		Data          struct {
			ID      string          `json:"id"`
			Status  string          `json:"status"`
			Outputs json.RawMessage `json:"outputs"`
			Error   string          `json:"error"`
		} `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return maildomain.Draft{}, fmt.Errorf("decode Dify response: %w", err)
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

func boundedMessages(messages []maildomain.Message) ([]byte, error) {
	copyOfMessages := append([]maildomain.Message(nil), messages...)
	for {
		encoded, err := json.Marshal(copyOfMessages)
		if err != nil {
			return nil, err
		}
		if len(encoded) <= maxContextBytes {
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
	var direct maildomain.Draft
	if isDraftObject(raw) && json.Unmarshal(raw, &direct) == nil {
		return direct, nil
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
			return direct, nil
		}
		var encoded string
		if json.Unmarshal(value, &encoded) == nil && isDraftObject([]byte(encoded)) && json.Unmarshal([]byte(encoded), &direct) == nil {
			return direct, nil
		}
	}
	return maildomain.Draft{}, errors.New("Dify outputs do not contain a structured draft")
}

func isDraftObject(raw []byte) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return false
	}
	_, hasDecision := object["should_reply"]
	_, hasBody := object["body_text"]
	return hasDecision || hasBody
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
