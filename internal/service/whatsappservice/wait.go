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
	Since   time.Time
	Timeout time.Duration
}

type WaitForMessagesResponse struct {
	Messages  []domain.Message
	TimedOut  bool
	Since     time.Time
	NextSince time.Time
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
	since := req.Since
	if since.IsZero() {
		if len(aliases) > 0 {
			last, err := s.messages.LastSentAt(ctx, aliases)
			if err != nil {
				return WaitForMessagesResponse{}, err
			}
			since = last
		}
		if since.IsZero() {
			since = time.Now()
		}
	}

	sub := s.broker.subscribe(aliases)
	defer s.broker.unsubscribe(sub)

	stored, err := s.messages.ListMessages(ctx, &messagesrepo.ListMessagesRequest{
		ChatJIDs:     aliases,
		After:        since,
		IncomingOnly: true,
		Limit:        maxWaitResults,
	})
	if err != nil {
		return WaitForMessagesResponse{}, fmt.Errorf("failed to read stored messages: %w", err)
	}
	if len(stored) > 0 {
		return newWaitResponse(stored, since), nil
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var got []domain.Message
	select {
	case m := <-sub.ch:
		got = append(got, m)
	case <-timer.C:
		resp := newWaitResponse([]domain.Message{}, since)
		resp.TimedOut = true
		return resp, nil
	case <-ctx.Done():
		return WaitForMessagesResponse{}, ctx.Err()
	}

	burst := time.NewTimer(burstWindow)
	defer burst.Stop()
	for len(got) < maxWaitResults {
		select {
		case m := <-sub.ch:
			got = append(got, m)
		case <-burst.C:
			return newWaitResponse(got, since), nil
		case <-ctx.Done():
			return newWaitResponse(got, since), nil
		}
	}
	return newWaitResponse(got, since), nil
}

func newWaitResponse(msgs []domain.Message, since time.Time) WaitForMessagesResponse {
	next := since
	for _, m := range msgs {
		if after := m.Timestamp.Add(time.Second); after.After(next) {
			next = after
		}
	}
	return WaitForMessagesResponse{Messages: msgs, Since: since, NextSince: next}
}

type broker struct {
	mu   sync.Mutex
	subs map[*subscription]struct{}
}

type subscription struct {
	chats map[string]struct{}
	ch    chan domain.Message
}

func newBroker() *broker {
	return &broker{subs: make(map[*subscription]struct{})}
}

func (b *broker) subscribe(chatJIDs []string) *subscription {
	sub := &subscription{ch: make(chan domain.Message, maxWaitResults)}
	if len(chatJIDs) > 0 {
		sub.chats = make(map[string]struct{}, len(chatJIDs))
		for _, jid := range chatJIDs {
			sub.chats[jid] = struct{}{}
		}
	}
	b.mu.Lock()
	b.subs[sub] = struct{}{}
	b.mu.Unlock()
	return sub
}

func (b *broker) unsubscribe(sub *subscription) {
	b.mu.Lock()
	delete(b.subs, sub)
	b.mu.Unlock()
}

func (b *broker) publish(m domain.Message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for sub := range b.subs {
		if sub.chats != nil {
			if _, ok := sub.chats[m.ChatJID]; !ok {
				continue
			}
		}
		select {
		case sub.ch <- m:
		default:
		}
	}
}
