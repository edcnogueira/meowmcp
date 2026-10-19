package whatsappservice

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jamillosantos/logctx"
	"go.uber.org/zap"

	"github.com/edcnogueira/meowmcp/internal/domain"
)

type SendMessageRequest struct {
	To        string
	Text      string
	ReplyToID string
}

type SendMessageResponse struct {
	Sent domain.SentMessage
	Chat domain.Chat
}

func (s *Service) SendMessage(ctx context.Context, req *SendMessageRequest) (SendMessageResponse, error) {
	if err := validateSendMessageRequest(req); err != nil {
		return SendMessageResponse{}, err
	}
	chatJID, err := s.whatsapp.ResolveRecipient(ctx, req.To)
	if err != nil {
		return SendMessageResponse{}, err
	}
	aliases, err := s.whatsapp.ChatAliases(ctx, chatJID)
	if err != nil {
		return SendMessageResponse{}, err
	}

	var quoted *domain.Message
	if req.ReplyToID != "" {
		m, err := s.messages.GetMessage(ctx, aliases, req.ReplyToID)
		if err != nil {
			return SendMessageResponse{}, fmt.Errorf("reply_to_id %s: %w", req.ReplyToID, err)
		}
		quoted = &m
	}

	sent, err := s.whatsapp.SendText(ctx, chatJID, req.Text, quoted)
	if err != nil {
		return SendMessageResponse{}, err
	}

	st := s.whatsapp.Status()
	msg := domain.Message{
		ID:         sent.ID,
		ChatJID:    sent.ChatJID,
		SenderJID:  st.JID,
		SenderName: st.PushName,
		FromMe:     true,
		Timestamp:  sent.Timestamp,
		Text:       req.Text,
		QuotedID:   req.ReplyToID,
	}
	if err := s.messages.UpsertChat(ctx, domain.Chat{
		JID:           sent.ChatJID,
		IsGroup:       strings.HasSuffix(sent.ChatJID, "@g.us"),
		LastMessageAt: sent.Timestamp,
	}); err != nil {
		logctx.Error(ctx, "failed to record chat of sent message", zap.String("chat_jid", sent.ChatJID), zap.Error(err))
	}
	if err := s.messages.SaveMessages(ctx, []domain.Message{msg}); err != nil {
		logctx.Error(ctx, "failed to record sent message", zap.String("message_id", sent.ID), zap.Error(err))
	}

	chat, err := s.chatInfo(ctx, sent.ChatJID, aliases)
	if err != nil {
		return SendMessageResponse{}, err
	}
	return SendMessageResponse{Sent: sent, Chat: chat}, nil
}

func validateSendMessageRequest(req *SendMessageRequest) error {
	var missing []string
	if strings.TrimSpace(req.To) == "" {
		missing = append(missing, "to")
	}
	if strings.TrimSpace(req.Text) == "" {
		missing = append(missing, "text")
	}
	if len(missing) > 0 {
		return errors.New("required: " + strings.Join(missing, ", "))
	}
	return nil
}
