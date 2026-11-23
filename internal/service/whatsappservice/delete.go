package whatsappservice

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/jamillosantos/logctx"
	"go.uber.org/zap"

	"github.com/edcnogueira/meowmcp/internal/domain"
	"github.com/edcnogueira/meowmcp/internal/repositories/messagesrepo"
)

const deleteBatchSize = 100

type DeleteForMeRequest struct {
	Chat       string
	MessageIDs []string
	Days       []time.Time
}

type DeleteForMeResponse struct {
	Chat    domain.Chat
	Deleted int
}

func (s *Service) DeleteForMe(ctx context.Context, req *DeleteForMeRequest) (DeleteForMeResponse, error) {
	if len(req.MessageIDs) == 0 && len(req.Days) == 0 {
		return DeleteForMeResponse{}, errors.New("required: message_ids or dates")
	}
	canonical, aliases, err := s.resolveChat(ctx, req.Chat)
	if err != nil {
		return DeleteForMeResponse{}, err
	}
	chat, err := s.chatInfo(ctx, canonical, aliases)
	if err != nil {
		return DeleteForMeResponse{}, err
	}
	resp := DeleteForMeResponse{Chat: chat}

	ids := make([]string, len(req.MessageIDs))
	for i, id := range req.MessageIDs {
		ids[i] = strings.TrimSpace(id)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	msgs := make([]domain.Message, 0, len(ids))
	for _, id := range ids {
		m, err := s.messages.GetMessage(ctx, aliases, id)
		if err != nil {
			return resp, fmt.Errorf("message_id %s: %w", id, err)
		}
		msgs = append(msgs, m)
	}
	for batch := range slices.Chunk(msgs, deleteBatchSize) {
		if err := s.deleteBatch(ctx, canonical, aliases, batch); err != nil {
			return resp, err
		}
		resp.Deleted += len(batch)
	}

	for _, day := range req.Days {
		for {
			batch, err := s.messages.ListMessages(ctx, &messagesrepo.ListMessagesRequest{
				ChatJIDs: aliases,
				After:    day,
				Before:   day.AddDate(0, 0, 1),
				Limit:    deleteBatchSize,
			})
			if err != nil {
				return resp, fmt.Errorf("failed to load messages of %s: %w", day.Format(time.DateOnly), err)
			}
			if len(batch) == 0 {
				break
			}
			if err := s.deleteBatch(ctx, canonical, aliases, batch); err != nil {
				return resp, err
			}
			resp.Deleted += len(batch)
		}
	}
	return resp, nil
}

func (s *Service) deleteBatch(ctx context.Context, chatJID string, aliases []string, msgs []domain.Message) error {
	if err := s.whatsapp.DeleteForMe(ctx, chatJID, msgs); err != nil {
		return err
	}
	ids := make([]string, len(msgs))
	for i, m := range msgs {
		ids[i] = m.ID
	}
	if err := s.messages.DeleteMessages(ctx, aliases, ids); err != nil {
		return fmt.Errorf("failed to delete messages from local history: %w", err)
	}
	for _, m := range msgs {
		if m.MediaFile == "" {
			continue
		}
		if err := os.Remove(m.MediaFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
			logctx.Error(ctx, "failed to remove media file of deleted message",
				zap.String("chat_jid", m.ChatJID), zap.String("message_id", m.ID), zap.Error(err))
		}
	}
	return nil
}
