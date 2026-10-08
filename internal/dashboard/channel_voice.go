package dashboard

import (
	"encoding/json"
	"fmt"
	"github.com/Tsinling0525/Spider/internal/agent"
	"strings"
	"time"
)

const voicePrompt = `This is a shared Xiaozhi voice and Dashboard conversation.
The server can automatically execute locally allowlisted DoorDash queries (auth check,
search, menu, cart, tracking, and checkout with confirm=false). Ask concise follow-up
questions for missing food preferences, quantities, required options and delivery address.
Adding items or changing the address waits for an explicit voice or browser confirmation.
The current local cart connector supports one plain item per selection, quantity=1,
without special instructions or required customizations. Explain unsupported options.
Use the configured DoorDash service, never substitute another delivery platform.
This deployment ONLY supports checkout previews: purchase submission is disabled.
Always call doordash_checkout with confirm=false. Read verified merchant, items,
quantities, AUD total including fees, and address from the actual preview before describing
an order. Never invent prices, claim a purchase, or suggest confirm=true can pay.
After a rejected tool, do not request it again without a new explicit user instruction.
Keep replies short enough to speak; a completed tool is not necessarily a placed order.`

type voiceQueryBudget struct{}

func (s *Service) voiceSpecs(c Conversation, specs []agent.ToolSpec, bound map[string]boundTool) []agent.ToolSpec {
	if !s.voiceConversation(c) {
		return specs
	}
	result := append([]agent.ToolSpec{}, specs...)
	for i, spec := range result {
		tool := bound[spec.Name]
		if !s.voiceConnectors[tool.connector.ID] {
			continue
		}
		if tool.tool.Name == "doordash_checkout" {
			result[i].Description = "DoorDash / doordash_checkout: 仅生成结账预览，绝不会下单或付款。confirm 必须为 false。"
			result[i].Parameters = json.RawMessage(`{"type":"object","properties":{"confirm":{"type":"boolean","const":false}},"required":["confirm"],"additionalProperties":false}`)
		}
		if tool.tool.Name == "doordash_add_to_cart" {
			result[i].Description = "DoorDash / doordash_add_to_cart: 向购物车加入一份无需必选选项的原味菜品，等待用户明确确认。quantity 必须为 1，不能添加特殊备注；不下单、不付款。"
			result[i].Parameters = json.RawMessage(`{"type":"object","properties":{"restaurantId":{"type":"string"},"itemName":{"type":"string"},"quantity":{"type":"integer","const":1}},"required":["restaurantId","itemName"],"additionalProperties":false}`)
		}
	}
	return result
}

// SetVoiceChannels is startup-only, before serving requests. Neither the model nor
// a cloud request can enable a profile or choose an automatically trusted connector.
func (s *Service) SetVoiceChannels(channels, connectors []string) error {
	channelSet, connectorSet := map[string]bool{}, map[string]bool{}
	for _, id := range channels {
		if !channelIDPattern.MatchString(id) {
			return errInvalid
		}
		channelSet[id] = true
	}
	for _, id := range connectors {
		if !channelIDPattern.MatchString(id) {
			return errInvalid
		}
		connectorSet[id] = true
	}
	s.voiceChannels, s.voiceConnectors = channelSet, connectorSet
	return nil
}

func (s *Service) voiceConversation(c Conversation) bool {
	return c.Source != nil && c.Source.Kind == "xiaozhi" && s.voiceChannels[c.Source.ID]
}

func (s *Service) voiceTool(c Conversation) bool {
	return s.voiceConversation(c) && c.Pending != nil && s.voiceConnectors[c.Pending.ConnectorID]
}

func onlyFields(args map[string]json.RawMessage, fields ...string) bool {
	for key := range args {
		found := false
		for _, field := range fields {
			if field == key {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func textArgument(args map[string]json.RawMessage, key string) string {
	var text string
	if json.Unmarshal(args[key], &text) != nil || len(text) > 512 {
		return ""
	}
	return strings.TrimSpace(text)
}

func (s *Service) automaticVoiceQuery(c Conversation) bool {
	if !s.voiceTool(c) {
		return false
	}
	var args map[string]json.RawMessage
	if json.Unmarshal(c.Pending.Arguments, &args) != nil || args == nil {
		return false
	}
	switch c.Pending.Tool {
	case "doordash_auth_check":
		return len(args) == 0
	case "doordash_search":
		return onlyFields(args, "query", "cuisine") && textArgument(args, "query") != "" && (args["cuisine"] == nil || textArgument(args, "cuisine") != "")
	case "doordash_menu":
		return onlyFields(args, "restaurantId") && textArgument(args, "restaurantId") != ""
	case "doordash_cart":
		return onlyFields(args, "restaurantId") && (args["restaurantId"] == nil || textArgument(args, "restaurantId") != "")
	case "doordash_track_order":
		return onlyFields(args, "orderId") && (args["orderId"] == nil || textArgument(args, "orderId") != "")
	case "doordash_checkout":
		var confirm bool
		return onlyFields(args, "confirm") && args["confirm"] != nil && string(args["confirm"]) != "null" && json.Unmarshal(args["confirm"], &confirm) == nil && !confirm
	}
	return false
}

func (s *Service) attachVoiceConfirmation(c *Conversation) {
	if !s.voiceConversation(*c) || c.Pending == nil {
		return
	}
	c.Pending.Voice = &VoiceConfirmation{ID: newID(), Summary: "这个操作尚不支持语音批准，需要在网页查看或在设备上取消。", ExpiresAt: time.Now().UTC().Add(5 * time.Minute), Version: c.Version}
	var args map[string]json.RawMessage
	if json.Unmarshal(c.Pending.Arguments, &args) != nil {
		return
	}
	summary, phrase := "", ""
	if !s.voiceTool(*c) {
		c.Pending.Voice = &VoiceConfirmation{ID: newID(), Summary: "这个工具不支持语音批准，需要在网页查看。", ExpiresAt: time.Now().UTC().Add(5 * time.Minute), Version: c.Version}
		return
	}
	switch c.Pending.Tool {
	case "doordash_set_address":
		address := textArgument(args, "address")
		if !onlyFields(args, "address") || address == "" {
			return
		}
		summary, phrase = "把 DoorDash 配送地址改为："+address+"。", "确认设置地址"
	case "doordash_add_to_cart":
		item, restaurant := textArgument(args, "itemName"), textArgument(args, "restaurantId")
		if !onlyFields(args, "restaurantId", "itemName", "quantity", "specialInstructions") || item == "" || restaurant == "" {
			return
		}
		quantity := 1
		if args["quantity"] != nil && (json.Unmarshal(args["quantity"], &quantity) != nil || quantity != 1) {
			return
		}
		summary = fmt.Sprintf("向 DoorDash 商家 %s 的购物车加入 %d 份%s。", restaurant, quantity, item)
		if args["specialInstructions"] != nil {
			note := textArgument(args, "specialInstructions")
			var rawNote string
			if json.Unmarshal(args["specialInstructions"], &rawNote) != nil || note != "" {
				return
			}
		}
		summary += "这只会修改购物车，不会下单或付款。"
		phrase = "确认加入购物车"
	default:
		// Logout, unknown tools and purchase=true are never voice-approved.
		summary = "这个操作不支持语音批准，需要在网页查看。"
		if c.Pending.Tool == "doordash_checkout" {
			summary = "当前 DoorDash 连接器只支持结账预览，不能实际下单或付款。请取消这个操作，再查询结账预览。"
		}
	}
	c.Pending.Voice = &VoiceConfirmation{ID: newID(), Summary: summary, Phrase: phrase, ExpiresAt: time.Now().UTC().Add(5 * time.Minute), Version: c.Version}
}

type voiceDecisionInput struct {
	ChannelID      string `json:"channel_id"`
	DecisionID     string `json:"decision_id"`
	ConfirmationID string `json:"confirmation_id"`
	Utterance      string `json:"utterance"`
}

type voiceDecision struct {
	Input          voiceDecisionInput `json:"input"`
	ConversationID string             `json:"conversation_id"`
	CreatedAt      time.Time          `json:"created_at"`
	MessageIndex   int                `json:"message_index"`
}

func confirmationWords(text string) string {
	return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(text), "。！!，,.？?"))
}

func (s *Service) confirmChannel(input voiceDecisionInput) (channelReply, error) {
	if !channelIDPattern.MatchString(input.ChannelID) || !channelIDPattern.MatchString(input.DecisionID) || !channelIDPattern.MatchString(input.ConfirmationID) || len(input.Utterance) > 256 {
		return channelReply{}, errInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := channelKey(input.ChannelID, input.DecisionID)
	if previous, ok := s.state.ChannelDecisions[key]; ok {
		if previous.Input != input {
			return channelReply{}, errConflict
		}
		result := s.currentChannelReplyLocked(input.ChannelID, previous.ConversationID)
		result.DecisionID = input.DecisionID
		return result, nil
	}
	if s.channelsStopped || len(s.state.ChannelDecisions) >= 10000 {
		return channelReply{}, errBusy
	}
	c, ok := s.state.Conversations[s.state.ChannelSessions[input.ChannelID]]
	if !ok {
		return channelReply{}, errNotFound
	}
	if !s.voiceConversation(c) || c.Source.ID != input.ChannelID || c.Pending == nil || c.Pending.Voice == nil {
		return channelReply{}, errConflict
	}
	challenge := c.Pending.Voice
	if challenge.ID != input.ConfirmationID || challenge.Version != c.Version || !time.Now().Before(challenge.ExpiresAt) {
		return channelReply{}, errConflict
	}
	approve := challenge.Phrase != "" && confirmationWords(input.Utterance) == challenge.Phrase && s.voiceTool(c)
	if !approve {
		switch confirmationWords(input.Utterance) {
		case "取消", "拒绝", "不要执行", "取消操作":
		default:
			return channelReply{}, fmt.Errorf("%w: repeat the specific confirmation phrase or cancel", errInvalid)
		}
	}
	// Changes to the connector invalidate spoken summaries, even if an old
	// browser approval still has an immutable endpoint snapshot.
	current, found := s.state.Connectors[c.Pending.ConnectorID]
	target := s.state.ApprovalTargets[c.ID]
	a, _ := json.Marshal(current)
	b, _ := json.Marshal(target)
	if approve && (!found || !current.Enabled || string(a) != string(b)) {
		return channelReply{}, errConflict
	}
	s.state.ChannelDecisions[key] = voiceDecision{Input: input, ConversationID: c.ID, CreatedAt: time.Now().UTC(), MessageIndex: len(c.Messages) + 1}
	claim, err := s.claimDecisionLocked(c.ID, c.Version, c.Pending.ID, approve, input.Utterance)
	if err != nil {
		delete(s.state.ChannelDecisions, key)
		return channelReply{}, err
	}
	s.channelWG.Add(1)
	go func() { defer s.channelWG.Done(); _, _ = s.finishDecision(s.channelCtx, claim) }()
	result := s.currentChannelReplyLocked(input.ChannelID, c.ID)
	result.DecisionID = input.DecisionID
	return result, nil
}

type channelMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (s *Service) currentChannelReplyLocked(channelID, conversationID string) channelReply {
	c, ok := s.state.Conversations[conversationID]
	if !ok {
		return channelReply{Status: "deleted", Reply: "这条 Spider 会话已删除。"}
	}
	result := s.replyForConversationLocked(c)
	result.ConversationID = c.ID
	for _, record := range s.state.ChannelRequests {
		if record.Input.ChannelID == channelID && record.ConversationID == c.ID && (result.RequestID == "" || record.CreatedAt.After(s.state.ChannelRequests[channelKey(channelID, result.RequestID)].CreatedAt)) {
			result.RequestID = record.Input.RequestID
		}
	}
	for _, message := range c.Messages {
		if (message.Role == "user" || message.Role == "assistant") && message.Content != "" {
			text := []rune(message.Content)
			result.Messages = append(result.Messages, channelMessage{Role: message.Role, Content: string(text[:min(len(text), 1000)])})
		}
	}
	if len(result.Messages) > 6 {
		result.Messages = result.Messages[len(result.Messages)-6:]
	}
	return result
}

func (s *Service) channelConversation(channelID string) (channelReply, error) {
	if !channelIDPattern.MatchString(channelID) {
		return channelReply{}, errInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.state.ChannelSessions[channelID]
	if id == "" {
		return channelReply{}, errNotFound
	}
	c, ok := s.state.Conversations[id]
	if ok && c.Pending != nil && c.Pending.Voice != nil && !time.Now().Before(c.Pending.Voice.ExpiresAt) && s.voiceConversation(c) {
		old := c
		pending := *c.Pending
		c.Pending = &pending
		c.Version++
		s.attachVoiceConfirmation(&c)
		s.state.Conversations[id] = c
		if err := s.persistLocked(); err != nil {
			s.state.Conversations[id] = old
			return channelReply{}, err
		}
	}
	return s.currentChannelReplyLocked(channelID, id), nil
}

func (s *Service) replyForConversationLocked(c Conversation) channelReply {
	result := channelReply{ConversationID: c.ID, Status: c.Status}
	for i := len(c.Messages) - 1; i >= 0; i-- {
		message := c.Messages[i]
		if message.Role == "assistant" && len(message.ToolCalls) == 0 && message.Content != "" {
			text := []rune(message.Content)
			result.Reply = string(text[:min(len(text), 4000)])
			break
		}
	}
	if c.Status == "running" {
		result.Reply = "Spider 正在处理，请稍后查询结果。"
	}
	if c.Status == "waiting_approval" {
		result.Reply = "这个操作需要在 Spider 网页确认。"
		if c.Pending != nil {
			result.PendingTool = c.Pending.ConnectorName + " / " + c.Pending.Tool
			if s.voiceConversation(c) && c.Pending.Voice != nil && time.Now().Before(c.Pending.Voice.ExpiresAt) && c.Pending.Voice.Version == c.Version {
				result.Voice = c.Pending.Voice
				result.Reply = c.Pending.Voice.Summary + "可以说“取消”。"
				if c.Pending.Voice.Phrase != "" {
					result.Reply = c.Pending.Voice.Summary + "如果同意，请说“" + c.Pending.Voice.Phrase + "”；也可以说“取消”。"
				}
			} else if s.voiceTool(c) && c.Pending.Tool == "doordash_checkout" {
				result.Reply = "当前 DoorDash 连接器只支持结账预览，不能实际下单或付款。请取消这个操作，改为查询结账预览。"
			}
		}
	}
	if c.Status == "failed" {
		result.Error = c.Error
		result.Reply = c.Error
	}
	return result
}
