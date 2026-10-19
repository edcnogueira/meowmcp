package whatsappservice

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/edcnogueira/meowmcp/internal/domain"
	"github.com/edcnogueira/meowmcp/internal/repositories/messagesrepo"
)

const (
	defaultChatLimit    = 20
	maxChatLimit        = 200
	defaultContactLimit = 20
	maxContactLimit     = 100
)

type ListChatsRequest struct {
	Query  string
	Limit  int
	Offset int
}

func (s *Service) ListChats(ctx context.Context, req *ListChatsRequest) ([]domain.Chat, error) {
	chats, err := s.messages.ListChats(ctx, &messagesrepo.ListChatsRequest{
		Query:  req.Query,
		Limit:  clamp(req.Limit, defaultChatLimit, maxChatLimit),
		Offset: max(req.Offset, 0),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list chats: %w", err)
	}
	return chats, nil
}

type SearchContactsRequest struct {
	Query string
	Limit int
}

func (s *Service) SearchContacts(ctx context.Context, req *SearchContactsRequest) ([]domain.Contact, error) {
	query := strings.ToLower(strings.TrimSpace(req.Query))
	if query == "" {
		return nil, errors.New("query is required")
	}
	limit := clamp(req.Limit, defaultContactLimit, maxContactLimit)
	digits := onlyDigits(query)

	contacts, err := s.whatsapp.Contacts(ctx)
	if err != nil && !errors.Is(err, domain.ErrNotLoggedIn) {
		return nil, fmt.Errorf("failed to load contacts: %w", err)
	}

	out := make([]domain.Contact, 0, limit)
	seen := make(map[string]struct{})
	for _, c := range contacts {
		if len(out) == limit {
			return out, nil
		}
		if matches(query, digits, c.Phone, c.Name, c.PushName, c.BusinessName) {
			out = append(out, c)
			seen[c.JID] = struct{}{}
		}
	}

	chats, err := s.messages.ListChats(ctx, &messagesrepo.ListChatsRequest{Query: req.Query, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("failed to search chats: %w", err)
	}
	for _, chat := range chats {
		if len(out) == limit {
			break
		}
		if _, ok := seen[chat.JID]; ok {
			continue
		}
		c := domain.Contact{JID: chat.JID, Name: chat.Name}
		if user, server, ok := strings.Cut(chat.JID, "@"); ok && server == "s.whatsapp.net" {
			c.Phone = "+" + user
		}
		out = append(out, c)
	}
	return out, nil
}

func (s *Service) CheckNumbers(ctx context.Context, phones []string) ([]domain.NumberCheck, error) {
	if len(phones) == 0 {
		return nil, errors.New("at least one phone number is required")
	}
	return s.whatsapp.CheckNumbers(ctx, phones)
}

func matches(query, digits string, phone string, names ...string) bool {
	if digits != "" && len(digits) >= 4 && strings.Contains(onlyDigits(phone), digits) {
		return true
	}
	for _, n := range names {
		if n != "" && strings.Contains(strings.ToLower(n), query) {
			return true
		}
	}
	return false
}

func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func clamp(v, def, maximum int) int {
	if v <= 0 {
		return def
	}
	return min(v, maximum)
}
