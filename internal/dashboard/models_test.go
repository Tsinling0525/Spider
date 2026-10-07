package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func modelInput(base string) ModelProviderInput {
	key := "model-fixture-key"
	return ModelProviderInput{Name: "Fixture", BaseURL: base, APIKey: &key, Enabled: true, Models: []ModelDefinition{
		{ID: "chat-a", Capability: ModelChat, Enabled: true},
		{ID: "chat-b", Capability: ModelChat, Enabled: true},
		{ID: "stt", Capability: ModelSTT, Enabled: true},
		{ID: "tts", Capability: ModelTTS, Enabled: true, Voice: "custom-voice"},
	}}
}

func modelJSON(t *testing.T, h http.Handler, method, path string, body any, status int) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, "/v1/dashboard"+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != status {
		t.Fatalf("%s %s: HTTP %d, want %d: %s", method, path, w.Code, status, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "model-fixture-key") || strings.Contains(w.Body.String(), `"api_key"`) {
		t.Fatal("model key exposed in API response")
	}
	return w
}

func TestModelProviderCRUDPersistenceAndActiveGuards(t *testing.T) {
	s := serviceForTest(t, nil)
	// Existing connectors must survive model configuration and its migration.
	connector, err := s.saveConnector("", Connector{Name: "Existing MCP", URL: "http://localhost:3000/mcp", Enabled: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(s, HTTPOptions{})
	input := modelInput("https://example.com/v1/")
	w := modelJSON(t, h, "POST", "/models/providers", input, 200)
	var provider ModelProvider
	if json.Unmarshal(w.Body.Bytes(), &provider) != nil || !provider.HasAPIKey || provider.BaseURL != "https://example.com/v1" {
		t.Fatal("provider not normalized or redacted")
	}
	input.APIKey = nil
	input.BaseURL = provider.BaseURL
	input.Note = "updated without replacing key"
	modelJSON(t, h, "PUT", "/models/providers/"+provider.ID, input, 200)
	private, _ := s.modelProvider(provider.ID)
	if private.APIKey != "model-fixture-key" {
		t.Fatal("editing lost saved key")
	}
	selection := ModelSelection{ProviderID: provider.ID, ModelID: "chat-a"}
	modelJSON(t, h, "PUT", "/models/active", map[string]any{"capability": "chat", "provider_id": provider.ID, "model_id": "chat-a"}, 200)
	modelJSON(t, h, "PUT", "/models/active", map[string]any{"capability": "stt", "provider_id": provider.ID, "model_id": "chat-a"}, 400)
	input.Enabled = false
	modelJSON(t, h, "PUT", "/models/providers/"+provider.ID, input, 409)
	modelJSON(t, h, "DELETE", "/models/providers/"+provider.ID, nil, 409)
	restarted, err := NewService(s.path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.models().Active[ModelChat] != selection || len(restarted.connectors()) != 1 || restarted.connectors()[0].ID != connector.ID {
		t.Fatal("restart lost models or existing connector")
	}
	if err := restarted.ImportEnvironmentModels("https://ignored.example/v1", "ignored", "ignored", VoiceOptions{}); err != nil || restarted.models().Active[ModelChat] != selection {
		t.Fatal("environment overwrote saved page settings")
	}
	h = NewHandler(restarted, HTTPOptions{})
	modelJSON(t, h, "GET", "/models", nil, 200)
	modelJSON(t, h, "PUT", "/models/active", map[string]any{"capability": "chat", "provider_id": "", "model_id": ""}, 200)
	empty := ""
	input.Enabled = true
	input.APIKey = &empty
	input.Models = input.Models[:2] // Restart migrated the legacy voice rows.
	modelJSON(t, h, "PUT", "/models/providers/"+provider.ID, input, 200)
	private, _ = restarted.modelProvider(provider.ID)
	if private.APIKey != "" {
		t.Fatal("explicit removal failed")
	}
	modelJSON(t, h, "DELETE", "/models/providers/"+provider.ID, nil, 204)
	if len(restarted.models().Providers) != 1 || restarted.models().Providers[0].Group != VoiceModels {
		t.Fatal("deleting chat also removed voice configuration")
	}
	modelJSON(t, h, "DELETE", "/models/providers/"+restarted.models().Providers[0].ID, nil, 204)
	again, err := NewService(s.path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := again.ImportEnvironmentModels("https://ignored.example/v1", "ignored", "ignored", VoiceOptions{}); err != nil || len(again.models().Providers) != 0 {
		t.Fatal("removed providers reappeared on restart")
	}
	if _, err := again.chatBackend(); err == nil {
		t.Fatal("disabled chat resurrected from environment")
	}
}

func TestLegacyMixedModelsSplitWithoutLosingCredentialsOrSelections(t *testing.T) {
	s := serviceForTest(t, nil)
	headers := map[string]string{"Authorization": "Bearer connector-fixture"}
	connector, err := s.saveConnector("", Connector{Name: "Existing", URL: "http://localhost:3000/mcp", Enabled: true}, &headers)
	if err != nil {
		t.Fatal(err)
	}
	conversation, _ := s.createConversation()
	input := modelInput("https://example.com/v1")
	preset := "openai"
	input.PresetID = &preset
	original, err := s.saveModelProvider("", input)
	if err != nil {
		t.Fatal(err)
	}
	for _, selection := range []struct {
		cap ModelCapability
		id  string
	}{{ModelChat, "chat-a"}, {ModelSTT, "stt"}, {ModelTTS, "tts"}} {
		if _, err := s.selectModel(selection.cap, ModelSelection{ProviderID: original.ID, ModelID: selection.id}); err != nil {
			t.Fatal(err)
		}
	}
	migrated, err := NewService(s.path, nil)
	if err != nil {
		t.Fatal(err)
	}
	config := migrated.models()
	if len(config.Providers) != 2 {
		t.Fatal("mixed provider was not split")
	}
	chat, _ := migrated.modelProvider(original.ID)
	voiceID := config.Active[ModelSTT].ProviderID
	voice, _ := migrated.modelProvider(voiceID)
	if chat.Group != ChatModels || voice.Group != VoiceModels || voiceID == original.ID || config.Active[ModelChat].ProviderID != original.ID || config.Active[ModelTTS].ProviderID != voiceID || config.Active[ModelTTS].ModelID != "tts" {
		t.Fatal("migration lost active model selection")
	}
	if chat.APIKey != "model-fixture-key" || voice.APIKey != chat.APIKey || voice.PresetID != "openai" || len(chat.Models) != 2 || len(voice.Models) != 2 || voice.Models[1].Voice != "custom-voice" {
		t.Fatal("migration lost credentials, models or TTS voice")
	}
	if _, err := migrated.conversation(conversation.ID); err != nil || len(migrated.connectors()) != 1 || migrated.connectors()[0].ID != connector.ID {
		t.Fatal("migration changed conversations or connectors")
	}
	again, err := NewService(s.path, nil)
	if err != nil || len(again.models().Providers) != 2 || again.models().Active[ModelSTT].ProviderID != voiceID {
		t.Fatal("migration was not persisted or was repeated")
	}
	newKey := "voice-only-key"
	voiceInput := ModelProviderInput{Name: voice.Name, BaseURL: "https://voice.example/v1", APIKey: &newKey, Enabled: true, Protocol: "openai", Group: VoiceModels, Models: voice.Models}
	if _, err := again.saveModelProvider(voiceID, voiceInput); err != nil {
		t.Fatal(err)
	}
	chat, _ = again.modelProvider(original.ID)
	if chat.APIKey != "model-fixture-key" || chat.BaseURL != "https://example.com/v1" || len(chat.Models) != 2 {
		t.Fatal("editing voice changed chat configuration")
	}
	if _, err := again.saveModelProvider("", voiceInput); !errors.Is(err, errInvalid) {
		t.Fatal("duplicate name in same category allowed")
	}
	invalid := voiceInput
	invalid.Group = ChatModels
	if _, err := again.saveModelProvider("", invalid); !errors.Is(err, errInvalid) {
		t.Fatal("chat configuration accepted audio models")
	}
	for _, capability := range []ModelCapability{ModelSTT, ModelTTS} {
		if _, err := again.selectModel(capability, ModelSelection{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := again.deleteModelProvider(voiceID); err != nil {
		t.Fatal(err)
	}
	if len(again.models().Providers) != 1 || again.models().Active[ModelChat].ProviderID != original.ID {
		t.Fatal("removing voice changed chat configuration")
	}
}

func TestModelDiscoveryProbesAndHotSwitching(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer model-fixture-key" {
			t.Error("saved credential not used")
		}
		switch r.URL.Path {
		case "/v1/models":
			respond(w, 200, map[string]any{"data": []map[string]string{{"id": "chat-b"}, {"id": "chat-a"}, {"id": "chat-a"}}})
		case "/v1/chat/completions":
			var body map[string]json.RawMessage
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid chat payload")
			}
			for _, key := range []string{"tools", "tool_choice", "parallel_tool_calls"} {
				if _, found := body[key]; found {
					t.Error("plain chat sent tool-only fields")
				}
			}
			var model string
			_ = json.Unmarshal(body["model"], &model)
			respond(w, 200, map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"role": "assistant", "content": model}}}})
		case "/v1/audio/transcriptions":
			if r.ParseMultipartForm(1<<20) != nil || r.FormValue("model") != "stt" || r.FormValue("response_format") != "json" {
				t.Error("invalid STT payload")
			}
			if r.MultipartForm != nil {
				defer r.MultipartForm.RemoveAll()
			}
			file, header, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				return
			}
			defer file.Close()
			raw, _ := io.ReadAll(file)
			if header.Filename == "test.wav" && (len(raw) != 32044 || string(raw[:4]) != "RIFF") {
				t.Error("probe WAV malformed")
			}
			respond(w, 200, map[string]string{"text": "fixture transcription"})
		case "/v1/audio/speech":
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["model"] != "tts" || body["voice"] != "custom-voice" || body["input"] == "" {
				t.Error("TTS selection/voice lost")
			}
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("fixture audio"))
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	s := serviceForTest(t, nil)
	input := modelInput(upstream.URL + "/v1")
	provider, err := s.saveModelProvider("", input)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(s, HTTPOptions{})
	// A draft can discover models before any model ID is entered, and reuse a
	// stored key without sending it back to the browser.
	input.APIKey = nil
	input.Models = nil
	w := modelJSON(t, h, "POST", "/models/fetch", map[string]any{"provider_id": provider.ID, "provider": input}, 200)
	if w.Body.String() != "{\"models\":[\"chat-a\",\"chat-b\"]}\n" {
		t.Fatalf("invalid discovery result: %s", w.Body.String())
	}
	for _, probe := range []struct {
		capability ModelCapability
		id         string
	}{{ModelChat, "chat-a"}, {ModelSTT, "stt"}, {ModelTTS, "tts"}} {
		modelJSON(t, h, "POST", "/models/test", map[string]any{"provider_id": provider.ID, "capability": probe.capability, "model_id": probe.id}, 200)
		modelJSON(t, h, "PUT", "/models/active", map[string]any{"capability": probe.capability, "provider_id": provider.ID, "model_id": probe.id}, 200)
	}
	c, _ := s.createConversation()
	for _, id := range []string{"chat-a", "chat-b"} {
		if _, err := s.selectModel(ModelChat, ModelSelection{ProviderID: provider.ID, ModelID: id}); err != nil {
			t.Fatal(err)
		}
		c, err = s.send(context.Background(), c.ID, c.Version, "hello", nil)
		if err != nil || c.Status != "idle" || c.Messages[len(c.Messages)-1].Content != id {
			t.Fatalf("live chat switch failed: %+v %v", c, err)
		}
	}
	w = modelJSON(t, h, "GET", "/capabilities", nil, 200)
	if !strings.Contains(w.Body.String(), `"transcription":true`) || !strings.Contains(w.Body.String(), `"speech":true`) {
		t.Fatal("voice capabilities not updated")
	}
	var audio bytes.Buffer
	writer := multipart.NewWriter(&audio)
	part, _ := writer.CreateFormFile("file", "recording.wav")
	_, _ = part.Write(silentWAV())
	_ = writer.Close()
	req := httptest.NewRequest("POST", "/v1/dashboard/voice/transcriptions", &audio)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "fixture transcription") {
		t.Fatal("selected STT not used by voice route")
	}
	modelJSON(t, h, "POST", "/voice/speech", map[string]string{"text": "hello"}, 200)
	if _, err := s.selectModel(ModelTTS, ModelSelection{}); err != nil {
		t.Fatal(err)
	}
	if s.voiceOptions(VoiceOptions{TTS: AudioModel{Model: "legacy"}}).TTS.Model != "" {
		t.Fatal("explicit disabled slot still used legacy voice")
	}
}

func TestModelDraftValidationAndEnvironmentImport(t *testing.T) {
	s := serviceForTest(t, nil)
	input := modelInput("https://example.com/v1")
	for _, change := range []func(*ModelProviderInput){
		func(p *ModelProviderInput) { p.BaseURL = "https://secret@example.com/v1" },
		func(p *ModelProviderInput) { p.Models = append(p.Models, p.Models[0]) },
		func(p *ModelProviderInput) { p.Models[0].Voice = "invalid-on-chat" },
		func(p *ModelProviderInput) { p.Models[0].Capability = "unknown" },
	} {
		copy := input
		copy.Models = append([]ModelDefinition{}, input.Models...)
		change(&copy)
		if _, err := s.saveModelProvider("", copy); !errors.Is(err, errInvalid) {
			t.Fatal("invalid model settings accepted")
		}
	}
	voice := VoiceOptions{STT: AudioModel{BaseURL: input.BaseURL, Model: "stt", APIKey: "model-fixture-key"}, TTS: AudioModel{BaseURL: input.BaseURL, Model: "tts"}, Voice: "voice"}
	if err := s.ImportEnvironmentModels(input.BaseURL, "model-fixture-key", "chat-a", voice); err != nil {
		t.Fatal(err)
	}
	config := s.models()
	if len(config.Providers) != 3 || config.Active[ModelSTT].ModelID != "stt" || s.voiceOptions(VoiceOptions{}).Voice != "voice" {
		t.Fatal("environment bootstrap failed")
	}
	modelJSON(t, NewHandler(s, HTTPOptions{}), "GET", "/models", nil, 200)
}

func TestModelProbesDoNotExposeUpstreamErrorsOrFollowRedirects(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusTemporaryRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("provider redirect followed")
				w.WriteHeader(200)
			}))
			defer destination.Close()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", destination.URL)
				w.WriteHeader(status)
				_, _ = w.Write([]byte("model-fixture-key"))
			}))
			defer upstream.Close()
			s := serviceForTest(t, nil)
			provider, err := s.saveModelProvider("", modelInput(upstream.URL))
			if err != nil {
				t.Fatal(err)
			}
			h := NewHandler(s, HTTPOptions{})
			modelJSON(t, h, "POST", "/models/fetch", map[string]string{"provider_id": provider.ID}, 502)
			modelJSON(t, h, "POST", "/models/test", map[string]string{"provider_id": provider.ID, "capability": "chat", "model_id": "chat-a"}, 502)
		})
	}
}

func TestClaudeProviderDiscoveryApprovalAndRestart(t *testing.T) {
	mcp, calls := mcpForTest(t)
	toolTurns := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "model-fixture-key" || r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("Authorization") != "" {
			t.Error("Claude authentication lost")
		}
		if r.URL.Path == "/v1/models" {
			if r.URL.Query().Get("after_id") == "" {
				respond(w, 200, map[string]any{"data": []any{map[string]string{"id": "claude-a"}}, "has_more": true, "last_id": "claude-a"})
			} else {
				respond(w, 200, map[string]any{"data": []any{map[string]string{"id": "claude-b"}}, "has_more": false})
			}
			return
		}
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected native endpoint %s", r.URL.Path)
		}
		var request struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("invalid Messages payload")
			return
		}
		if len(request.Tools) == 0 {
			respond(w, 200, map[string]any{"role": "assistant", "stop_reason": "end_turn", "content": []any{map[string]string{"type": "text", "text": "OK"}}})
			return
		}
		toolTurns++
		if toolTurns == 1 {
			respond(w, 200, map[string]any{"role": "assistant", "stop_reason": "tool_use", "content": []any{map[string]any{"type": "tool_use", "id": "claude-call", "name": request.Tools[0].Name, "input": map[string]string{"query": "example"}}}})
			return
		}
		if !strings.Contains(string(request.Messages[len(request.Messages)-1].Content), `"tool_use_id":"claude-call"`) {
			t.Error("tool receipt missing after restart")
		}
		respond(w, 200, map[string]any{"role": "assistant", "stop_reason": "end_turn", "content": []any{map[string]string{"type": "text", "text": "completed"}}})
	}))
	defer upstream.Close()
	s := serviceForTest(t, nil)
	input := modelInput(upstream.URL + "/v1")
	input.Protocol = "anthropic"
	preset := "claude"
	input.PresetID = &preset
	input.Models = input.Models[:1]
	provider, err := s.saveModelProvider("", input)
	if err != nil {
		t.Fatal(err)
	}
	input.Protocol = ""
	input.PresetID = nil
	input.APIKey = nil
	if _, err := s.saveModelProvider(provider.ID, input); err != nil {
		t.Fatal(err)
	}
	saved, _ := s.modelProvider(provider.ID)
	if saved.Protocol != "anthropic" || saved.PresetID != "claude" {
		t.Fatal("editing lost provider protocol or preset")
	}
	h := NewHandler(s, HTTPOptions{})
	w := modelJSON(t, h, "POST", "/models/fetch", map[string]string{"provider_id": provider.ID}, 200)
	if w.Body.String() != "{\"models\":[\"claude-a\",\"claude-b\"]}\n" {
		t.Fatal("Claude discovery pagination failed")
	}
	modelJSON(t, h, "POST", "/models/test", map[string]string{"provider_id": provider.ID, "capability": "chat", "model_id": "chat-a"}, 200)
	if _, err := s.selectModel(ModelChat, ModelSelection{ProviderID: provider.ID, ModelID: "chat-a"}); err != nil {
		t.Fatal(err)
	}
	connector := testedConnector(t, s, mcp)
	c, _ := s.createConversation()
	c, err = s.send(context.Background(), c.ID, c.Version, "lookup", []string{connector.ID})
	if err != nil || c.Pending == nil || calls.Load() != 0 {
		t.Fatal("Claude tool was not held for approval")
	}
	restarted, err := NewService(s.path, nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err = restarted.decide(context.Background(), c.ID, c.Version, c.Pending.ID, true)
	if err != nil || c.Status != "idle" || c.Messages[len(c.Messages)-1].Content != "completed" || calls.Load() != 1 {
		t.Fatalf("native tool continuation failed: %+v %v", c, err)
	}
	input.Protocol = "anthropic"
	input.Models = append(input.Models, ModelDefinition{ID: "voice", Capability: ModelTTS, Enabled: true})
	if _, err := s.saveModelProvider(provider.ID, input); !errors.Is(err, errInvalid) {
		t.Fatal("native Claude voice configuration accepted")
	}
}
