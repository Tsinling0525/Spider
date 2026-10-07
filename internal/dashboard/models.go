package dashboard

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tsinling0525/Spider/internal/agent"
)

func validCapability(capability ModelCapability) bool {
	return capability == ModelChat || capability == ModelSTT || capability == ModelTTS
}

func groupForCapability(capability ModelCapability) ModelGroup {
	if capability == ModelChat {
		return ChatModels
	}
	return VoiceModels
}

func providerGroup(models []ModelDefinition) ModelGroup {
	if len(models) == 0 {
		return ""
	}
	chat, voice := false, false
	for _, model := range models {
		if model.Capability == ModelChat {
			chat = true
		} else {
			voice = true
		}
	}
	if chat && voice {
		return ""
	} // Legacy mixed configuration; split on boot.
	if chat {
		return ChatModels
	}
	return VoiceModels
}

// Migrate old mixed providers into independent chat and voice configurations.
// Credentials stay server-side; active voice selections follow the copied rows.
func (s *Service) migrateModelGroupsLocked() bool {
	changed := false
	legacy := []ModelProvider{}
	for _, provider := range s.state.ModelProviders {
		if provider.Group == "" {
			legacy = append(legacy, provider)
		}
	}
	for _, provider := range legacy {
		changed = true
		group := providerGroup(provider.Models)
		if len(provider.Models) == 0 {
			group = ChatModels
		}
		if group != "" {
			provider.Group = group
			s.state.ModelProviders[provider.ID] = provider
			continue
		}
		chat, voice := provider, provider
		chat.Group = ChatModels
		voice.Group = VoiceModels
		chat.Models = []ModelDefinition{}
		voice.Models = []ModelDefinition{}
		for _, model := range provider.Models {
			if model.Capability == ModelChat {
				chat.Models = append(chat.Models, model)
			} else {
				voice.Models = append(voice.Models, model)
			}
		}
		voice.ID = provider.ID + "-voice"
		if _, exists := s.state.ModelProviders[voice.ID]; exists {
			voice.ID = newID()
		}
		s.state.ModelProviders[chat.ID] = chat
		s.state.ModelProviders[voice.ID] = voice
		for _, capability := range []ModelCapability{ModelSTT, ModelTTS} {
			active := s.state.ActiveModels[capability]
			if active.ProviderID == provider.ID {
				active.ProviderID = voice.ID
				s.state.ActiveModels[capability] = active
			}
		}
	}
	return changed
}

func publicModelProvider(provider ModelProvider) ModelProvider {
	provider.HasAPIKey = provider.APIKey != ""
	provider.APIKey = ""
	return provider
}

func validateModelConnection(provider ModelProvider) error {
	if provider.Group != "" && provider.Group != ChatModels && provider.Group != VoiceModels {
		return fmt.Errorf("%w: 无效的模型配置类别", errInvalid)
	}
	if provider.Group == VoiceModels && provider.Protocol == "anthropic" {
		return fmt.Errorf("%w: Claude Messages 协议不支持语音模型", errInvalid)
	}
	if provider.Protocol != "" && provider.Protocol != "openai" && provider.Protocol != "anthropic" {
		return fmt.Errorf("%w: 不支持的模型协议", errInvalid)
	}
	switch provider.PresetID {
	case "", "openai", "claude", "deepseek", "mimo", "ollama":
	default:
		return fmt.Errorf("%w: 不支持的模型预设", errInvalid)
	}
	u, err := url.Parse(provider.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%w: API 地址必须为 HTTP(S)，且不能包含凭据、查询参数或片段", errInvalid)
	}
	if provider.Name == "" || utf8.RuneCountInString(provider.Name) > 80 || len(provider.Note) > 1024 || len(provider.APIKey) > 8192 || strings.ContainsAny(provider.APIKey, "\r\n") {
		return fmt.Errorf("%w: 供应商名称或 API key 无效", errInvalid)
	}
	return nil
}

func validateModelProvider(provider ModelProvider) error {
	if err := validateModelConnection(provider); err != nil {
		return err
	}
	if len(provider.Models) == 0 || len(provider.Models) > 64 {
		return fmt.Errorf("%w: 请配置 1–64 个模型", errInvalid)
	}
	seen := map[string]bool{}
	for _, model := range provider.Models {
		if provider.Group != "" && groupForCapability(model.Capability) != provider.Group {
			return fmt.Errorf("%w: 对话和语音模型需要分别配置", errInvalid)
		}
		if provider.Protocol == "anthropic" && model.Capability != ModelChat {
			return fmt.Errorf("%w: Claude Messages 协议只支持聊天模型", errInvalid)
		}
		key := string(model.Capability) + ":" + model.ID
		if !validCapability(model.Capability) || model.ID == "" || len(model.ID) > 256 || len(model.Label) > 256 || len(model.Voice) > 256 || strings.ContainsAny(model.ID+model.Voice, "\r\n") || seen[key] {
			return fmt.Errorf("%w: 模型 ID、能力或重复配置无效", errInvalid)
		}
		if model.Capability != ModelTTS && model.Voice != "" {
			return fmt.Errorf("%w: 音色只适用于语音输出模型", errInvalid)
		}
		seen[key] = true
	}
	return nil
}

func normalizeModelProvider(id string, input ModelProviderInput, previous ModelProvider) ModelProvider {
	provider := ModelProvider{ID: id, Name: strings.TrimSpace(input.Name), BaseURL: strings.TrimRight(strings.TrimSpace(input.BaseURL), "/"), Enabled: input.Enabled, Note: strings.TrimSpace(input.Note), Models: append([]ModelDefinition{}, input.Models...), APIKey: previous.APIKey}
	provider.Protocol = input.Protocol
	if provider.Protocol == "" {
		provider.Protocol = previous.Protocol
	}
	if provider.Protocol == "" {
		provider.Protocol = "openai"
	}
	provider.PresetID = previous.PresetID
	if input.PresetID != nil {
		provider.PresetID = *input.PresetID
	}
	if input.APIKey != nil {
		provider.APIKey = strings.TrimSpace(*input.APIKey)
	}
	provider.Group = input.Group
	if provider.Group == "" {
		provider.Group = previous.Group
	}
	if provider.Group == "" {
		provider.Group = providerGroup(provider.Models)
	}
	for i := range provider.Models {
		model := &provider.Models[i]
		model.ID = strings.TrimSpace(model.ID)
		model.Label = strings.TrimSpace(model.Label)
		model.Voice = strings.TrimSpace(model.Voice)
		if model.Label == "" {
			model.Label = model.ID
		}
	}
	return provider
}

func providerFromInput(id string, input ModelProviderInput, previous ModelProvider) (ModelProvider, error) {
	provider := normalizeModelProvider(id, input, previous)
	return provider, validateModelProvider(provider)
}

func findModel(provider ModelProvider, capability ModelCapability, id string) (ModelDefinition, bool) {
	for _, model := range provider.Models {
		if model.Capability == capability && model.ID == id {
			return model, true
		}
	}
	return ModelDefinition{}, false
}

func (s *Service) modelsLocked() ModelConfiguration {
	configuration := ModelConfiguration{Providers: []ModelProvider{}, Active: map[ModelCapability]ModelSelection{}}
	for _, provider := range s.state.ModelProviders {
		configuration.Providers = append(configuration.Providers, publicModelProvider(provider))
	}
	sort.Slice(configuration.Providers, func(i, j int) bool { return configuration.Providers[i].Name < configuration.Providers[j].Name })
	for _, capability := range []ModelCapability{ModelChat, ModelSTT, ModelTTS} {
		configuration.Active[capability] = s.state.ActiveModels[capability]
	}
	return configuration
}

func (s *Service) models() ModelConfiguration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modelsLocked()
}

func (s *Service) saveModelProvider(id string, input ModelProviderInput) (ModelProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, found := s.state.ModelProviders[id]
	if id != "" && !found {
		return ModelProvider{}, errNotFound
	}
	if id == "" {
		if len(s.state.ModelProviders) >= 64 {
			return ModelProvider{}, errBusy
		}
		id = newID()
	}
	provider, err := providerFromInput(id, input, previous)
	if err != nil {
		return ModelProvider{}, err
	}
	if found && previous.Group != "" && provider.Group != previous.Group {
		return ModelProvider{}, fmt.Errorf("%w: 请在对应的配置页编辑供应商", errInvalid)
	}
	for otherID, other := range s.state.ModelProviders {
		if otherID != id && strings.EqualFold(other.Name, provider.Name) && (other.Group == provider.Group || other.Group == "" || provider.Group == "") {
			return ModelProvider{}, fmt.Errorf("%w: 供应商名称已存在", errInvalid)
		}
	}
	for capability, active := range s.state.ActiveModels {
		if active.ProviderID != id {
			continue
		}
		model, exists := findModel(provider, capability, active.ModelID)
		if !provider.Enabled || !exists || !model.Enabled {
			return ModelProvider{}, fmt.Errorf("%w: 请先切换或关闭正在使用的模型，再停用或移除它", errConflict)
		}
	}
	s.state.ModelProviders[id] = provider
	if err := s.persistLocked(); err != nil {
		if found {
			s.state.ModelProviders[id] = previous
		} else {
			delete(s.state.ModelProviders, id)
		}
		return ModelProvider{}, err
	}
	return publicModelProvider(provider), nil
}

func (s *Service) deleteModelProvider(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	provider, found := s.state.ModelProviders[id]
	if !found {
		return errNotFound
	}
	for _, active := range s.state.ActiveModels {
		if active.ProviderID == id {
			return fmt.Errorf("%w: 请先切换或关闭该供应商的当前模型", errConflict)
		}
	}
	delete(s.state.ModelProviders, id)
	if err := s.persistLocked(); err != nil {
		s.state.ModelProviders[id] = provider
		return err
	}
	return nil
}

func (s *Service) selectModel(capability ModelCapability, selection ModelSelection) (ModelConfiguration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validCapability(capability) {
		return ModelConfiguration{}, errInvalid
	}
	if selection.ProviderID != "" || selection.ModelID != "" {
		provider, found := s.state.ModelProviders[selection.ProviderID]
		model, exists := findModel(provider, capability, selection.ModelID)
		if !found || !exists || !provider.Enabled || !model.Enabled {
			return ModelConfiguration{}, fmt.Errorf("%w: 请选择已启用且能力匹配的模型", errInvalid)
		}
	}
	previous, hadPrevious := s.state.ActiveModels[capability]
	s.state.ActiveModels[capability] = selection
	if err := s.persistLocked(); err != nil {
		if hadPrevious {
			s.state.ActiveModels[capability] = previous
		} else {
			delete(s.state.ActiveModels, capability)
		}
		return ModelConfiguration{}, err
	}
	return s.modelsLocked(), nil
}

func (s *Service) activeModelLocked(capability ModelCapability) (ModelProvider, ModelDefinition, bool) {
	selection := s.state.ActiveModels[capability]
	provider, found := s.state.ModelProviders[selection.ProviderID]
	model, exists := findModel(provider, capability, selection.ModelID)
	return provider, model, found && exists && provider.Enabled && model.Enabled
}

func (s *Service) chatBackendLocked() (agent.Backend, error) {
	provider, model, found := s.activeModelLocked(ModelChat)
	if found {
		return providerChatBackend(provider, model.ID)
	}
	if _, configured := s.state.ActiveModels[ModelChat]; !configured && s.backend != nil {
		return s.backend, nil
	}
	return nil, errors.New("请在「模型与语音」中配置并选择当前聊天模型")
}

func providerChatBackend(provider ModelProvider, model string) (agent.Backend, error) {
	if provider.Protocol == "anthropic" {
		return agent.NewAnthropicBackend(provider.BaseURL, provider.APIKey, model, nil)
	}
	return agent.NewCompatibleBackend(provider.BaseURL, provider.APIKey, model, nil, agent.CompatibleOptions{PassReasoning: provider.PresetID == "mimo" || provider.PresetID == "deepseek"})
}

func (s *Service) chatBackend() (agent.Backend, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chatBackendLocked()
}

func (s *Service) voiceOptions(fallback VoiceOptions) VoiceOptions {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := fallback
	for _, capability := range []ModelCapability{ModelSTT, ModelTTS} {
		provider, model, found := s.activeModelLocked(capability)
		if found {
			audio := AudioModel{BaseURL: provider.BaseURL, APIKey: provider.APIKey, Model: model.ID}
			if capability == ModelSTT {
				result.STT = audio
			} else {
				result.TTS = audio
				result.Voice = model.Voice
			}
		} else if _, configured := s.state.ActiveModels[capability]; configured {
			if capability == ModelSTT {
				result.STT = AudioModel{}
			} else {
				result.TTS = AudioModel{}
			}
		}
	}
	return result
}

// ImportEnvironmentModels runs once per data directory. Page edits, removals and
// explicit disabled slots survive subsequent boots even if env values remain.
func (s *Service) ImportEnvironmentModels(chatBase, chatKey, chatModel string, voice VoiceOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.state.ActiveModels) > 0 || len(s.state.ModelProviders) > 0 {
		return nil
	}
	previousProviders, previousActive := s.state.ModelProviders, s.state.ActiveModels
	s.state.ModelProviders = map[string]ModelProvider{}
	s.state.ActiveModels = map[ModelCapability]ModelSelection{ModelChat: {}, ModelSTT: {}, ModelTTS: {}}
	add := func(capability ModelCapability, name, base, key, model, voice string) error {
		if model == "" {
			return nil
		}
		id := "env-" + string(capability)
		provider := ModelProvider{ID: id, Name: name, BaseURL: strings.TrimRight(base, "/"), APIKey: key, Enabled: true, Group: groupForCapability(capability), Models: []ModelDefinition{{ID: model, Label: model, Capability: capability, Enabled: true, Voice: voice}}}
		if err := validateModelProvider(provider); err != nil {
			return err
		}
		s.state.ModelProviders[id] = provider
		s.state.ActiveModels[capability] = ModelSelection{ProviderID: id, ModelID: model}
		return nil
	}
	err := add(ModelChat, "环境配置 · 聊天", chatBase, chatKey, chatModel, "")
	if err == nil {
		err = add(ModelSTT, "环境配置 · 语音输入", voice.STT.BaseURL, voice.STT.APIKey, voice.STT.Model, "")
	}
	if err == nil {
		err = add(ModelTTS, "环境配置 · 语音输出", voice.TTS.BaseURL, voice.TTS.APIKey, voice.TTS.Model, voice.Voice)
	}
	if err == nil {
		err = s.persistLocked()
	}
	if err != nil {
		s.state.ModelProviders = previousProviders
		s.state.ActiveModels = previousActive
	}
	return err
}

func (s *Service) modelDraft(id string, input ModelProviderInput, discovery bool) (ModelProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, found := s.state.ModelProviders[id]
	if id != "" && !found {
		return ModelProvider{}, errNotFound
	}
	provider := normalizeModelProvider(id, input, previous)
	if discovery {
		return provider, validateModelConnection(provider)
	}
	return provider, validateModelProvider(provider)
}

func (s *Service) modelProvider(id string) (ModelProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	provider, found := s.state.ModelProviders[id]
	if !found {
		return provider, errNotFound
	}
	return provider, nil
}

func providerRequest(ctx context.Context, provider ModelProvider, method, path, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, provider.BaseURL+path, body)
	if err != nil {
		return nil, errInvalid
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json, audio/mpeg")
	if provider.APIKey != "" {
		if provider.Protocol == "anthropic" {
			req.Header.Set("x-api-key", provider.APIKey)
		} else {
			req.Header.Set("Authorization", "Bearer "+provider.APIKey)
		}
	}
	if provider.Protocol == "anthropic" {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	client := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return nil, errors.New("无法连接模型服务，请检查 API 地址和网络")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, fmt.Errorf("模型服务返回 HTTP %d，请检查地址、模型名称和凭据", response.StatusCode)
	}
	return response, nil
}

func fetchProviderModels(ctx context.Context, provider ModelProvider) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	seen := map[string]bool{}
	ids := []string{}
	cursor := ""
	for page := 0; page < 10; page++ {
		path := "/models"
		if provider.Protocol == "anthropic" {
			path += "?limit=100"
			if cursor != "" {
				path += "&after_id=" + url.QueryEscape(cursor)
			}
		}
		response, err := providerRequest(ctx, provider, http.MethodGet, path, "", nil)
		if err != nil {
			return nil, err
		}
		raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		response.Body.Close()
		if err != nil || len(raw) > 1<<20 {
			return nil, errors.New("模型列表超过大小上限或读取失败")
		}
		var result struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if json.Unmarshal(raw, &result) != nil || result.Data == nil {
			return nil, errors.New("供应商未返回可用的模型列表，可手动填写模型 ID")
		}
		for _, model := range result.Data {
			if model.ID != "" && len(model.ID) <= 256 && !seen[model.ID] {
				ids = append(ids, model.ID)
				seen[model.ID] = true
			}
			if len(ids) >= 1000 {
				break
			}
		}
		if provider.Protocol != "anthropic" || !result.HasMore || len(ids) >= 1000 {
			break
		}
		if result.LastID == "" || result.LastID == cursor {
			return nil, errors.New("供应商返回了无效的模型列表分页")
		}
		cursor = result.LastID
	}
	sort.Strings(ids)
	return ids, nil
}

func testProviderModel(ctx context.Context, provider ModelProvider, capability ModelCapability, id string) (ModelTestResult, error) {
	model, found := findModel(provider, capability, id)
	if !found {
		return ModelTestResult{}, fmt.Errorf("%w: 请选择已配置的模型", errInvalid)
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	start := time.Now()
	var err error
	switch capability {
	case ModelChat:
		backend, createErr := providerChatBackend(provider, id)
		if createErr != nil {
			return ModelTestResult{}, createErr
		}
		var message agent.Message
		message, err = backend.Next(ctx, []agent.Message{{Role: "user", Content: "Please reply with OK. Do not use any tools."}}, nil)
		if err == nil && (message.Role != "assistant" || strings.TrimSpace(message.Content) == "") {
			err = errors.New("模型没有返回可用的文本回复")
		}
	case ModelSTT:
		var payload bytes.Buffer
		writer := multipart.NewWriter(&payload)
		part, createErr := writer.CreateFormFile("file", "test.wav")
		if createErr != nil {
			return ModelTestResult{}, createErr
		}
		_, _ = part.Write(silentWAV())
		_ = writer.WriteField("model", id)
		_ = writer.WriteField("response_format", "json")
		_ = writer.Close()
		var response *http.Response
		response, err = providerRequest(ctx, provider, http.MethodPost, "/audio/transcriptions", writer.FormDataContentType(), &payload)
		if err == nil {
			defer response.Body.Close()
			var transcription map[string]json.RawMessage
			if json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&transcription) != nil {
				err = errors.New("转写服务返回了无效 JSON")
			} else if text, ok := transcription["text"]; !ok {
				err = errors.New("转写服务没有返回 text 字段")
			} else {
				var value string
				if json.Unmarshal(text, &value) != nil {
					err = errors.New("转写服务 text 字段无效")
				}
			}
		}
	case ModelTTS:
		voice := model.Voice
		if voice == "" {
			voice = "alloy"
		}
		raw, _ := json.Marshal(map[string]string{"model": id, "input": "你好，这是一段语音测试。", "voice": voice, "response_format": "mp3"})
		var response *http.Response
		response, err = providerRequest(ctx, provider, http.MethodPost, "/audio/speech", "application/json", bytes.NewReader(raw))
		if err == nil {
			defer response.Body.Close()
			audio, readErr := io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
			if readErr != nil || len(audio) == 0 || len(audio) > 16<<20 || strings.Contains(response.Header.Get("Content-Type"), "json") {
				err = errors.New("TTS 服务没有返回可用的音频")
			}
		}
	default:
		return ModelTestResult{}, errInvalid
	}
	if err != nil {
		return ModelTestResult{}, errors.New("模型测试失败。" + safeModelError(err, provider.APIKey))
	}
	return ModelTestResult{OK: true, Message: "模型接口调用成功", LatencyMS: time.Since(start).Milliseconds()}, nil
}

func safeModelError(err error, key string) string {
	message := err.Error()
	if key != "" {
		message = strings.ReplaceAll(message, key, "[redacted]")
	}
	return message
}

func silentWAV() []byte {
	const length = 16000 * 2
	var wav bytes.Buffer
	wav.WriteString("RIFF")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(36+length))
	wav.WriteString("WAVEfmt ")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(16))
	for _, value := range []uint16{1, 1} {
		_ = binary.Write(&wav, binary.LittleEndian, value)
	}
	for _, value := range []uint32{16000, 32000} {
		_ = binary.Write(&wav, binary.LittleEndian, value)
	}
	for _, value := range []uint16{2, 16} {
		_ = binary.Write(&wav, binary.LittleEndian, value)
	}
	wav.WriteString("data")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(length))
	wav.Write(make([]byte, length))
	return wav.Bytes()
}
