package whatsappservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/edcnogueira/meowmcp/internal/domain"
	"github.com/edcnogueira/meowmcp/internal/repositories/messagesrepo"
)

const (
	defaultMessageLimit = 30
	maxMessageLimit     = 300
	markReadWindow      = 100
	maxUnanswered       = 20
)

type GetMessagesRequest struct {
	Chat    string
	Limit   int
	Before  time.Time
	After   time.Time
	SinceID int64
}

type GetMessagesResponse struct {
	Chat        domain.Chat
	Messages    []domain.Message
	NextSinceID int64
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
		Forward:  req.SinceID > 0,
		AfterSeq: req.SinceID,
		Limit:    clamp(req.Limit, defaultMessageLimit, maxMessageLimit),
	})
	if err != nil {
		return GetMessagesResponse{}, fmt.Errorf("failed to get messages: %w", err)
	}
	s.decorate(ctx, msgs)
	chat, err := s.chatInfo(ctx, canonical, aliases)
	if err != nil {
		return GetMessagesResponse{}, err
	}
	next := req.SinceID
	for _, m := range msgs {
		next = max(next, m.Seq)
	}
	if req.SinceID <= 0 {
		if next, err = s.messages.MaxSeq(ctx, aliases); err != nil {
			return GetMessagesResponse{}, err
		}
	}
	return GetMessagesResponse{Chat: chat, Messages: msgs, NextSinceID: next}, nil
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
	s.decorate(ctx, msgs)
	return msgs, nil
}

type ChatStateResponse struct {
	Chat         domain.Chat
	LastIncoming *domain.Message
	LastOutgoing *domain.Message
	Unanswered   []domain.Message
	OpenQuestion *domain.Message
	LastSeq      int64
}

func (s *Service) ChatState(ctx context.Context, chat string) (ChatStateResponse, error) {
	canonical, aliases, err := s.resolveChat(ctx, chat)
	if err != nil {
		return ChatStateResponse{}, err
	}
	info, err := s.chatInfo(ctx, canonical, aliases)
	if err != nil {
		return ChatStateResponse{}, err
	}
	resp := ChatStateResponse{Chat: info}

	lastOut, err := s.messages.ListMessages(ctx, &messagesrepo.ListMessagesRequest{ChatJIDs: aliases, OutgoingOnly: true, Limit: 1})
	if err != nil {
		return ChatStateResponse{}, fmt.Errorf("failed to read last sent message: %w", err)
	}
	lastIn, err := s.messages.ListMessages(ctx, &messagesrepo.ListMessagesRequest{ChatJIDs: aliases, IncomingOnly: true, Limit: 1})
	if err != nil {
		return ChatStateResponse{}, fmt.Errorf("failed to read last received message: %w", err)
	}
	pending := &messagesrepo.ListMessagesRequest{ChatJIDs: aliases, IncomingOnly: true, Limit: maxUnanswered}
	if len(lastOut) > 0 {
		pending.After = lastOut[0].Timestamp
	}
	candidates, err := s.messages.ListMessages(ctx, pending)
	if err != nil {
		return ChatStateResponse{}, fmt.Errorf("failed to read unanswered messages: %w", err)
	}
	if resp.LastSeq, err = s.messages.MaxSeq(ctx, aliases); err != nil {
		return ChatStateResponse{}, err
	}

	resp.Unanswered = make([]domain.Message, 0, len(candidates))
	for _, m := range candidates {
		if len(lastOut) == 0 || m.Timestamp.After(lastOut[0].Timestamp) || m.Seq > lastOut[0].Seq {
			resp.Unanswered = append(resp.Unanswered, m)
		}
	}
	all := slices.Concat(lastOut, lastIn, resp.Unanswered)
	s.decorate(ctx, all)
	if len(lastOut) > 0 {
		resp.LastOutgoing = &all[0]
	}
	if len(lastIn) > 0 {
		resp.LastIncoming = &all[len(lastOut)]
	}
	resp.Unanswered = all[len(lastOut)+len(lastIn):]
	for i := len(resp.Unanswered) - 1; i >= 0; i-- {
		if isQuestion(resp.Unanswered[i]) {
			resp.OpenQuestion = &resp.Unanswered[i]
			break
		}
	}
	return resp, nil
}

func isQuestion(m domain.Message) bool {
	switch m.MediaType {
	case "buttons", "list", "poll", "interactive":
		return true
	}
	return strings.Contains(m.Text, "?") || strings.Contains(m.Transcript, "?")
}

func (s *Service) ResolveChat(ctx context.Context, chat string) (domain.Chat, []string, error) {
	canonical, aliases, err := s.resolveChat(ctx, chat)
	if err != nil {
		return domain.Chat{}, nil, err
	}
	info, err := s.chatInfo(ctx, canonical, aliases)
	if err != nil {
		return domain.Chat{}, nil, err
	}
	return info, aliases, nil
}

func (s *Service) decorate(ctx context.Context, msgs []domain.Message) {
	var jids []string
	seen := make(map[string]struct{})
	for i := range msgs {
		msgs[i].MediaStatus = mediaStatus(msgs[i])
		for _, m := range msgs[i].Mentions {
			if _, ok := seen[m.JID]; !ok {
				seen[m.JID] = struct{}{}
				jids = append(jids, m.JID)
			}
		}
	}
	if len(jids) == 0 {
		return
	}
	described := s.whatsapp.DescribeMentions(ctx, jids)
	for i := range msgs {
		for j, m := range msgs[i].Mentions {
			if d, ok := described[m.JID]; ok {
				msgs[i].Mentions[j] = d
			}
		}
	}
}

func mediaStatus(m domain.Message) domain.MediaStatus {
	if !domain.IsDownloadableMediaType(m.MediaType) {
		return ""
	}
	if m.MediaFile != "" {
		if info, err := os.Stat(m.MediaFile); err == nil && info.Size() > 0 {
			return domain.MediaStatusReady
		}
	}
	if m.HasMediaKey {
		return domain.MediaStatusDownloadable
	}
	return domain.MediaStatusMissingKey
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
