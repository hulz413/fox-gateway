package orchestrator

import (
	"context"
	"strings"

	"fox-gateway/internal/domain"
	"fox-gateway/internal/store"
)

const (
	conversationResetIntent = "conversation_reset"
	resetCommandClear       = "/clear"
	resetCommandNew         = "/new"
)

type conversationContext struct {
	conversation domain.Conversation
	sessionID    string
}

func (s *Service) currentConversation(ctx context.Context, chatID string) (conversationContext, error) {
	conversation, err := s.store.GetConversation(ctx, chatID)
	if err != nil {
		if store.IsNotFound(err) {
			return conversationContext{}, nil
		}
		return conversationContext{}, err
	}
	return conversationContext{
		conversation: conversation,
		sessionID:    strings.TrimSpace(conversation.ClaudeSessionID),
	}, nil
}

func applyConversationEvent(base domain.Conversation, event domain.LarkMessageEvent, intent string) domain.Conversation {
	if base.ChatID == "" {
		base.ChatID = event.ChatID
	}
	base.LastMessageID = event.MessageID
	base.LastSenderOpenID = event.SenderOpenID
	base.LastMessageText = event.Text
	base.LastIntent = intent
	return base
}

func isConversationResetCommand(text string) bool {
	return strings.EqualFold(text, resetCommandClear) || strings.EqualFold(text, resetCommandNew)
}
