package device

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
)

type Reply struct {
	Transcript, Text, Conversation string
	Audio                          [][]byte
	TextOnly                       bool
}
type Provider interface {
	Turn(context.Context, string, string, [][]byte) (Reply, error)
}
type Dify struct {
	BaseURL, APIKey string
	Client          *http.Client
	// ASRURL optionally points to the local WAV transcription service.
	// No Dify credential is sent to this separate endpoint.
	ASRURL   string
	TextOnly bool
}

func (d *Dify) request(ctx context.Context, path, contentType string, body []byte) ([]byte, error) {
	if d.APIKey == "" {
		return nil, errors.New("voice Dify app is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(d.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+d.APIKey)
	req.Header.Set("Content-Type", contentType)
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 8<<20 {
		return nil, errors.New("provider response exceeds limit")
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("Dify %s returned HTTP %d", path, resp.StatusCode)
	}
	return b, nil
}
func (d *Dify) json(ctx context.Context, path string, payload any) ([]byte, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return d.request(ctx, path, "application/json", b)
}
func (d *Dify) Turn(ctx context.Context, user, conversation string, packets [][]byte) (Reply, error) {
	var result Reply
	wav, err := decodeOpus(ctx, packets)
	if err != nil {
		return result, err
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	// Dify validates the multipart part's MIME type, not just its filename.
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="speech.wav"`)
	header.Set("Content-Type", "audio/wav")
	f, err := w.CreatePart(header)
	if err != nil {
		return result, err
	}
	_, _ = f.Write(wav)
	_ = w.WriteField("user", user)
	_ = w.Close()
	var b []byte
	if d.ASRURL != "" {
		b, err = d.localTranscript(ctx, wav)
	} else {
		b, err = d.request(ctx, "/audio-to-text", w.FormDataContentType(), body.Bytes())
	}
	if err != nil {
		return result, err
	}
	var transcript struct {
		Text string `json:"text"`
	}
	if err = json.Unmarshal(b, &transcript); err != nil {
		return result, err
	}
	if strings.TrimSpace(transcript.Text) == "" {
		return result, errors.New("no speech recognized")
	}
	result.Transcript = transcript.Text
	b, err = d.json(ctx, "/chat-messages", map[string]any{"inputs": map[string]any{}, "query": result.Transcript, "response_mode": "blocking", "conversation_id": conversation, "user": user})
	if err != nil {
		return result, err
	}
	var chat struct {
		Answer       string `json:"answer"`
		Conversation string `json:"conversation_id"`
	}
	if err = json.Unmarshal(b, &chat); err != nil {
		return result, err
	}
	chat.Answer = displayAnswer(chat.Answer)
	if chat.Answer == "" || len(chat.Answer) > 8192 {
		return result, errors.New("invalid chat response")
	}
	result.Text = chat.Answer
	result.Conversation = chat.Conversation
	if d.TextOnly {
		result.TextOnly = true
		return result, nil
	}
	b, err = d.json(ctx, "/text-to-audio", map[string]any{"text": result.Text, "user": user})
	if err != nil {
		return result, err
	}
	result.Audio, err = encodeOpus(ctx, b)
	return result, err
}

func (d *Dify) localTranscript(ctx context.Context, wav []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", d.ASRURL, bytes.NewReader(wav))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "audio/wav")
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("local transcription: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("local transcription returned HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32769))
	if len(b) > 32768 {
		return nil, errors.New("transcription exceeds limit")
	}
	return b, err
}

// DeepSeek may prepend reasoning markup to Dify's answer field. Only show the
// final answer on the compact display (and never synthesize the reasoning).
func displayAnswer(text string) string {
	text = strings.TrimSpace(text)
	for strings.HasPrefix(text, "<think>") {
		end := strings.Index(text, "</think>")
		if end < 0 {
			return ""
		}
		text = strings.TrimSpace(text[end+len("</think>"):])
	}
	return text
}

// Diagnostic echoes recorded Opus back without claiming speech recognition or AI inference.
// It is only selected by explicit SPIDER_DEVICE_DIAGNOSTIC=1 configuration.
type Diagnostic struct{}

func (Diagnostic) Turn(ctx context.Context, user, conversation string, packets [][]byte) (Reply, error) {
	if len(packets) == 0 {
		return Reply{}, errors.New("no recorded audio")
	}
	return Reply{Transcript: fmt.Sprintf("诊断模式：收到 %d 帧录音，未进行语音识别。", len(packets)), Text: "正在回放刚才的录音。此模式不生成 AI 回答。", Audio: packets}, nil
}
