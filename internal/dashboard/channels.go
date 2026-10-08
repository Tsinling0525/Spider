package dashboard

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tsinling0525/Spider/internal/agent"
)

// Chat submission never approves a tool. Voice decisions use a separate route
// with a locally enabled profile and an immutable, expiring confirmation.
type channelInput struct {
	ChannelID       string   `json:"channel_id"`
	ChannelName     string   `json:"channel_name"`
	RequestID       string   `json:"request_id"`
	Content         string   `json:"content"`
	ConnectorIDs    []string `json:"connector_ids"`
	NewConversation bool     `json:"new_conversation"`
}

type channelRequest struct {
	Input          channelInput `json:"input"`
	ConversationID string       `json:"conversation_id"`
	MessageIndex   int          `json:"message_index"`
	CreatedAt      time.Time    `json:"created_at"`
}

// Return only assistant text and a tool label to the cloud. Raw connector
// receipts, credential-bearing URLs, arguments and approval IDs stay local.
type channelReply struct {
	RequestID      string             `json:"request_id"`
	ConversationID string             `json:"conversation_id"`
	Status         string             `json:"status"`
	Reply          string             `json:"reply"`
	PendingTool    string             `json:"pending_tool,omitempty"`
	Error          string             `json:"error,omitempty"`
	Voice          *VoiceConfirmation `json:"voice_confirmation,omitempty"`
	Messages       []channelMessage   `json:"messages,omitempty"`
	DecisionID     string             `json:"decision_id,omitempty"`
}

var channelIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)

func channelKey(channelID, requestID string) string { return channelID + "/" + requestID }

func (s *Service) submitChannel(input channelInput) (channelReply, error) {
	input.Content = strings.TrimSpace(input.Content)
	input.ChannelName = strings.TrimSpace(input.ChannelName)
	if input.ChannelName == "" {
		input.ChannelName = "小智"
	}
	if !channelIDPattern.MatchString(input.ChannelID) || !channelIDPattern.MatchString(input.RequestID) ||
		!utf8.ValidString(input.Content) || !utf8.ValidString(input.ChannelName) || utf8.RuneCountInString(input.ChannelName) > 64 ||
		input.Content == "" || len(input.Content) > 8192 || len(input.ConnectorIDs) > 128 {
		return channelReply{}, errInvalid
	}
	input.ConnectorIDs = slices.Clone(input.ConnectorIDs)
	key := channelKey(input.ChannelID, input.RequestID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, ok := s.state.ChannelRequests[key]; ok {
		if previous.Input.Content != input.Content || !slices.Equal(previous.Input.ConnectorIDs, input.ConnectorIDs) || previous.Input.NewConversation != input.NewConversation {
			return channelReply{}, fmt.Errorf("%w: request_id was already used for a different request", errConflict)
		}
		return s.channelReplyLocked(previous), nil
	}
	if s.channelsStopped || s.memoryPaused || len(s.state.ChannelRequests) >= 10000 {
		return channelReply{}, errBusy
	}
	backend, err := s.chatBackendLocked()
	if err != nil {
		return channelReply{}, err
	}
	oldSession := s.state.ChannelSessions[input.ChannelID]
	c, exists := s.state.Conversations[oldSession]
	created := !exists || input.NewConversation
	if created {
		if len(s.state.Conversations) >= 1000 {
			return channelReply{}, errBusy
		}
		now := time.Now().UTC()
		c = Conversation{ID: newID(), Title: "新对话", Messages: []agent.Message{}, ConnectorIDs: []string{}, Status: "idle", Version: 1, CreatedAt: now, UpdatedAt: now,
			Source: &ConversationSource{Kind: "xiaozhi", ID: input.ChannelID, Name: input.ChannelName}}
		s.state.Conversations[c.ID] = c
	}
	record := channelRequest{Input: input, ConversationID: c.ID, MessageIndex: len(c.Messages), CreatedAt: time.Now().UTC()}
	s.state.ChannelRequests[key] = record
	s.state.ChannelSessions[input.ChannelID] = c.ID
	// startTurnLocked atomically persists the request, source, session and user
	// message before any model work. A reconnect can never start this turn twice.
	started, specs, bound, err := s.startTurnLocked(c.ID, c.Version, input.Content, input.ConnectorIDs)
	if err != nil {
		delete(s.state.ChannelRequests, key)
		if oldSession == "" {
			delete(s.state.ChannelSessions, input.ChannelID)
		} else {
			s.state.ChannelSessions[input.ChannelID] = oldSession
		}
		if created {
			delete(s.state.Conversations, c.ID)
		}
		return channelReply{}, err
	}
	s.channelWG.Add(1)
	go func() {
		defer s.channelWG.Done()
		_, _ = s.generate(s.channelCtx, started, specs, bound, backend)
	}()
	return s.channelReplyLocked(record), nil
}

func (s *Service) channelReplyLocked(record channelRequest) channelReply {
	result := channelReply{RequestID: record.Input.RequestID, ConversationID: record.ConversationID}
	c, ok := s.state.Conversations[record.ConversationID]
	if !ok {
		result.Status = "deleted"
		result.Reply = "这条 Spider 会话已删除，请发起新的请求。"
		return result
	}
	// A previous request must not claim a later utterance's answer as its own.
	end := len(c.Messages)
	for i := record.MessageIndex + 1; i < end; i++ {
		if c.Messages[i].Role == "user" {
			confirmation := false
			for _, decision := range s.state.ChannelDecisions {
				if decision.ConversationID == c.ID && decision.Input.ChannelID == record.Input.ChannelID && decision.MessageIndex == i {
					confirmation = true
					break
				}
			}
			if confirmation {
				continue
			}
			end = i
			break
		}
	}
	result.Status = c.Status
	if end != len(c.Messages) {
		result.Status = "superseded"
	}
	for i := end - 1; i > record.MessageIndex; i-- {
		m := c.Messages[i]
		if m.Role == "assistant" && len(m.ToolCalls) == 0 && m.Content != "" {
			result.Reply = string([]rune(m.Content)[:min(4000, utf8.RuneCountInString(m.Content))])
			break
		}
	}
	if result.Status == "running" {
		result.Reply = "Spider 正在处理，请稍后查询结果。"
	}
	if result.Status == "waiting_approval" {
		current := s.replyForConversationLocked(c)
		result.Reply, result.PendingTool, result.Voice = current.Reply, current.PendingTool, current.Voice
	}
	if result.Status == "failed" {
		result.Error = c.Error
		result.Reply = c.Error
	}
	return result
}

func (s *Service) channelStatus(channelID, requestID string) (channelReply, error) {
	if !channelIDPattern.MatchString(channelID) || !channelIDPattern.MatchString(requestID) {
		return channelReply{}, errInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.state.ChannelRequests[channelKey(channelID, requestID)]
	if !ok {
		return channelReply{}, errNotFound
	}
	return s.channelReplyLocked(record), nil
}

func (s *Service) StopChannels() {
	s.mu.Lock()
	s.channelsStopped = true
	s.channelCancel()
	s.mu.Unlock()
	s.channelWG.Wait()
}

func registerChannelRoutes(mux *http.ServeMux, s *Service) {
	mux.HandleFunc("GET /v1/dashboard/channels/xiaozhi/{channel}/conversation", func(w http.ResponseWriter, r *http.Request) {
		result, err := s.channelConversation(r.PathValue("channel"))
		if err != nil {
			respondError(w, err)
			return
		}
		respond(w, http.StatusOK, result)
	})
	mux.HandleFunc("POST /v1/dashboard/channels/xiaozhi/decisions", func(w http.ResponseWriter, r *http.Request) {
		var input voiceDecisionInput
		if !decode(w, r, &input) {
			return
		}
		result, err := s.confirmChannel(input)
		if err != nil {
			respondError(w, err)
			return
		}
		respond(w, http.StatusAccepted, result)
	})
	mux.HandleFunc("POST /v1/dashboard/channels/xiaozhi/requests", func(w http.ResponseWriter, r *http.Request) {
		var input channelInput
		if !decode(w, r, &input) {
			return
		}
		result, err := s.submitChannel(input)
		if err != nil {
			respondError(w, err)
			return
		}
		respond(w, http.StatusAccepted, result)
	})
	mux.HandleFunc("GET /v1/dashboard/channels/xiaozhi/requests/{channel}/{request}", func(w http.ResponseWriter, r *http.Request) {
		result, err := s.channelStatus(r.PathValue("channel"), r.PathValue("request"))
		if err != nil {
			respondError(w, err)
			return
		}
		respond(w, http.StatusOK, result)
	})
}
