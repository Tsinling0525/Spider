package dashboard

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type AudioModel struct {
	BaseURL string
	APIKey  string
	Model   string
}
type VoiceOptions struct {
	STT   AudioModel
	TTS   AudioModel
	Voice string
}

func (v VoiceOptions) Validate() error {
	for _, model := range []AudioModel{v.STT, v.TTS} {
		if model.Model == "" {
			continue
		}
		u, err := url.Parse(model.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("voice base URL must be HTTP(S) without credentials, query or fragment")
		}
	}
	return nil
}

func (v VoiceOptions) transcribe(w http.ResponseWriter, r *http.Request) {
	if v.STT.Model == "" {
		respondError(w, errors.New("请在「模型与语音」中配置并选择语音输入模型"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
	if err := r.ParseMultipartForm(12 << 20); err != nil {
		respondError(w, errInvalid)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		respondError(w, errInvalid)
		return
	}
	defer file.Close()
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	name := "recording.webm"
	for _, suffix := range []string{".wav", ".mp3", ".m4a", ".ogg", ".mp4"} {
		if strings.HasSuffix(strings.ToLower(header.Filename), suffix) {
			name = "recording" + suffix
		}
	}
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		respondError(w, err)
		return
	}
	if _, err := io.Copy(part, file); err != nil {
		respondError(w, errInvalid)
		return
	}
	_ = writer.WriteField("model", v.STT.Model)
	_ = writer.WriteField("response_format", "json")
	_ = writer.Close()
	response, err := audioRequest(r, v.STT, "/audio/transcriptions", writer.FormDataContentType(), &payload)
	if err != nil {
		respondError(w, err)
		return
	}
	defer response.Body.Close()
	var result struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&result); err != nil {
		respondError(w, errors.New("invalid transcription response"))
		return
	}
	respond(w, 200, result)
}

func (v VoiceOptions) speak(w http.ResponseWriter, r *http.Request) {
	if v.TTS.Model == "" {
		respondError(w, errors.New("请在「模型与语音」中配置并选择语音输出模型"))
		return
	}
	var input struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Text) == "" || len(input.Text) > 12000 {
		respondError(w, errInvalid)
		return
	}
	voice := v.Voice
	if voice == "" {
		voice = "alloy"
	}
	raw, _ := json.Marshal(map[string]string{"model": v.TTS.Model, "input": input.Text, "voice": voice, "response_format": "mp3"})
	response, err := audioRequest(r, v.TTS, "/audio/speech", "application/json", bytes.NewReader(raw))
	if err != nil {
		respondError(w, err)
		return
	}
	defer response.Body.Close()
	audio, err := io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
	if err != nil || len(audio) > 16<<20 {
		respondError(w, errors.New("speech response exceeded its limit or was interrupted"))
		return
	}
	w.Header().Set("Content-Type", "audio/mpeg")
	w.WriteHeader(200)
	_, _ = w.Write(audio)
}

func audioRequest(original *http.Request, model AudioModel, path, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(original.Context(), http.MethodPost, strings.TrimRight(model.BaseURL, "/")+path, body)
	if err != nil {
		return nil, errors.New("invalid voice endpoint")
	}
	req.Header.Set("Content-Type", contentType)
	if model.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+model.APIKey)
	}
	client := &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return nil, errors.New("voice model connection failed")
	}
	if response.StatusCode != 200 {
		response.Body.Close()
		return nil, errors.New("voice model returned an error; check server-side credentials and model configuration")
	}
	return response, nil
}
