package whatsappservice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/edcnogueira/meowmcp/internal/domain"
	"github.com/edcnogueira/meowmcp/internal/repositories/messagesrepo"
)

const (
	defaultMessageLimit = 30
	maxMessageLimit     = 300
	markReadWindow      = 100
)

type GetMessagesRequest struct {
	Chat   string
	Limit  int
	Before time.Time
	After  time.Time
}

type GetMessagesResponse struct {
	Chat     domain.Chat
	Messages []domain.Message
}

func (s *Service) GetMessages(ctx context.Context, req *GetMessagesRequest) (GetMessagesResponse, error) {
	canonical, aliases, err := s.resolveChat(ctx, req.Chat)
	if err != nil {
		return GetMessagesResponse{}, err
	}
	msgs, err := s.messages.ListMessages(ctx, &messagesrepo.ListMessagesRequest{
		ChatJIDs: aliases,
		Before:   req.Before,
		After:    req.After,
		Limit:    clamp(req.Limit, defaultMessageLimit, maxMessageLimit),
	})
	if err != nil {
		return GetMessagesResponse{}, fmt.Errorf("failed to get messages: %w", err)
	}
	chat, err := s.chatInfo(ctx, canonical, aliases)
	if err != nil {
		return GetMessagesResponse{}, err
	}
	return GetMessagesResponse{Chat: chat, Messages: msgs}, nil
}

type SearchMessagesRequest struct {
	Query string
	Chat  string
	Limit int
}

func (s *Service) SearchMessages(ctx context.Context, req *SearchMessagesRequest) ([]domain.Message, error) {
	if strings.TrimSpace(req.Query) == "" {
		return nil, errors.New("query is required")
	}
	var aliases []string
	if req.Chat != "" {
		var err error
		if _, aliases, err = s.resolveChat(ctx, req.Chat); err != nil {
			return nil, err
		}
	}
	msgs, err := s.messages.ListMessages(ctx, &messagesrepo.ListMessagesRequest{
		ChatJIDs: aliases,
		Query:    req.Query,
		Limit:    clamp(req.Limit, defaultMessageLimit, maxMessageLimit),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to search messages: %w", err)
	}
	return msgs, nil
}

func (s *Service) MarkRead(ctx context.Context, chat string) (int, error) {
	canonical, aliases, err := s.resolveChat(ctx, chat)
	if err != nil {
		return 0, err
	}
	msgs, err := s.messages.ListMessages(ctx, &messagesrepo.ListMessagesRequest{
		ChatJIDs:     aliases,
		IncomingOnly: true,
		Limit:        markReadWindow,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to load messages to mark as read: %w", err)
	}
	if len(msgs) == 0 {
		return 0, nil
	}
	if err := s.whatsapp.MarkRead(ctx, canonical, msgs); err != nil {
		return 0, err
	}
	return len(msgs), nil
}

func (s *Service) chatInfo(ctx context.Context, canonical string, aliases []string) (domain.Chat, error) {
	for _, jid := range aliases {
		chat, ok, err := s.messages.GetChat(ctx, jid)
		if err != nil {
			return domain.Chat{}, fmt.Errorf("failed to get chat: %w", err)
		}
		if ok {
			return chat, nil
		}
	}
	return domain.Chat{JID: canonical, IsGroup: strings.HasSuffix(canonical, "@g.us")}, nil
}
