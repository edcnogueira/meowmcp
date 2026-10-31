package whatsappservice

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/edcnogueira/meowmcp/internal/domain"
	"github.com/edcnogueira/meowmcp/internal/repositories/messagesrepo"
)

const (
	defaultWaitTimeout = 60 * time.Second
	MaxWaitTimeout     = 240 * time.Second
	burstWindow        = 3 * time.Second
	maxWaitResults     = 50
)

type WaitForMessagesRequest struct {
	Chat    string
	SinceID int64
	Timeout time.Duration
}

type WaitForMessagesResponse struct {
	Messages    []domain.Message
	TimedOut    bool
	SinceID     int64
	NextSinceID int64
}

func (s *Service) WaitForMessages(ctx context.Context, req *WaitForMessagesRequest) (WaitForMessagesResponse, error) {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = defaultWaitTimeout
	}
	timeout = min(timeout, MaxWaitTimeout)

	var aliases []string
	if req.Chat != "" {
		var err error
		if _, aliases, err = s.resolveChat(ctx, req.Chat); err != nil {
			return WaitForMessagesResponse{}, err
		}
	}
	since := req.SinceID
	if since <= 0 {
		var err error
		if since, err = s.defaultSince(ctx, aliases); err != nil {
			return WaitForMessagesResponse{}, err
		}
	}

	signal, stop := s.broker.subscribe(aliases)
	defer stop()

	resp := WaitForMessagesResponse{SinceID: since, NextSinceID: since, Messages: []domain.Message{}}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		msgs, err := s.MessagesSince(ctx, aliases, since, maxWaitResults)
		if err != nil {
			return WaitForMessagesResponse{}, err
		}
		if len(msgs) > 0 {
			resp.Messages = msgs
			resp.NextSinceID = msgs[len(msgs)-1].Seq
			return resp, nil
		}
		select {
		case <-signal:
		case <-deadline.C:
			resp.TimedOut = true
			return resp, nil
		case <-ctx.Done():
			return WaitForMessagesResponse{}, ctx.Err()
		}
		burst := time.NewTimer(burstWindow)
		select {
		case <-burst.C:
		case <-ctx.Done():
		}
		burst.Stop()
	}
}

func (s *Service) defaultSince(ctx context.Context, aliases []string) (int64, error) {
	if len(aliases) > 0 {
		sent, err := s.messages.ListMessages(ctx, &messagesrepo.ListMessagesRequest{ChatJIDs: aliases, OutgoingOnly: true, Limit: 1})
		if err != nil {
			return 0, fmt.Errorf("failed to read last sent message: %w", err)
		}
		if len(sent) > 0 {
			return sent[0].Seq, nil
		}
	}
	return s.messages.MaxSeq(ctx, aliases)
}

func (s *Service) MessagesSince(ctx context.Context, aliases []string, sinceID int64, limit int) ([]domain.Message, error) {
	msgs, err := s.messages.ListMessages(ctx, &messagesrepo.ListMessagesRequest{
		ChatJIDs:     aliases,
		IncomingOnly: true,
		Forward:      true,
		AfterSeq:     sinceID,
		Limit:        limit,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read new messages: %w", err)
	}
	s.decorate(ctx, msgs)
	return msgs, nil
}

func (s *Service) LatestID(ctx context.Context, aliases []string) (int64, error) {
	return s.messages.MaxSeq(ctx, aliases)
}

func (s *Service) Watch(aliases []string) (<-chan struct{}, func()) {
	return s.broker.subscribe(aliases)
}

type broker struct {
	mu   sync.Mutex
	subs map[*subscription]struct{}
}

type subscription struct {
	chats map[string]struct{}
	ch    chan struct{}
}

func newBroker() *broker {
	return &broker{subs: make(map[*subscription]struct{})}
}

func (b *broker) subscribe(chatJIDs []string) (<-chan struct{}, func()) {
	sub := &subscription{ch: make(chan struct{}, 1)}
	if len(chatJIDs) > 0 {
		sub.chats = make(map[string]struct{}, len(chatJIDs))
		for _, jid := range chatJIDs {
			sub.chats[jid] = struct{}{}
		}
	}
	b.mu.Lock()
	b.subs[sub] = struct{}{}
	b.mu.Unlock()
	return sub.ch, func() {
		b.mu.Lock()
		delete(b.subs, sub)
		b.mu.Unlock()
	}
}

func (b *broker) publish(chatJID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for sub := range b.subs {
		if sub.chats != nil {
			if _, ok := sub.chats[chatJID]; !ok {
				continue
			}
		}
		select {
		case sub.ch <- struct{}{}:
		default:
		}
	}
}
