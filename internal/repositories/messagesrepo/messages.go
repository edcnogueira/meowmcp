package messagesrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/edcnogueira/meowmcp/internal/domain"
)

const messageColumns = `chat_jid, id, sender_jid, sender_name, from_me, timestamp, text, media_type, quoted_id`

func (r *Repository) SaveMessages(ctx context.Context, msgs []domain.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	msgStmt, err := tx.PrepareContext(ctx, `
INSERT INTO messages (`+messageColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (chat_jid, id) DO UPDATE SET
	sender_name = CASE WHEN excluded.sender_name <> '' THEN excluded.sender_name ELSE messages.sender_name END,
	text = excluded.text,
	media_type = excluded.media_type,
	quoted_id = excluded.quoted_id`)
	if err != nil {
		return fmt.Errorf("failed to prepare message upsert: %w", err)
	}
	defer msgStmt.Close()

	chatStmt, err := tx.PrepareContext(ctx, `
INSERT INTO chats (jid, last_message_at) VALUES (?, ?)
ON CONFLICT (jid) DO UPDATE SET last_message_at = MAX(chats.last_message_at, excluded.last_message_at)`)
	if err != nil {
		return fmt.Errorf("failed to prepare chat touch: %w", err)
	}
	defer chatStmt.Close()

	for _, m := range msgs {
		if _, err := msgStmt.ExecContext(ctx, m.ChatJID, m.ID, m.SenderJID, m.SenderName, m.FromMe,
			unix(m.Timestamp), m.Text, m.MediaType, m.QuotedID); err != nil {
			return fmt.Errorf("failed to save message %s: %w", m.ID, err)
		}
		if _, err := chatStmt.ExecContext(ctx, m.ChatJID, unix(m.Timestamp)); err != nil {
			return fmt.Errorf("failed to touch chat %s: %w", m.ChatJID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit messages: %w", err)
	}
	return nil
}

func (r *Repository) UpdateMessageText(ctx context.Context, chatJIDs []string, id, text string) error {
	if len(chatJIDs) == 0 {
		return nil
	}
	args := append(append([]any{text}, stringsToAny(chatJIDs)...), id)
	_, err := r.db.ExecContext(ctx, `UPDATE messages SET text = ? WHERE chat_jid IN (`+placeholders(len(chatJIDs))+`) AND id = ?`, args...)
	if err != nil {
		return fmt.Errorf("failed to update message %s: %w", id, err)
	}
	return nil
}

func (r *Repository) GetMessage(ctx context.Context, chatJIDs []string, id string) (domain.Message, error) {
	if len(chatJIDs) == 0 {
		return domain.Message{}, domain.ErrMessageNotFound
	}
	row := r.db.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM messages
WHERE chat_jid IN (`+placeholders(len(chatJIDs))+`) AND id = ?`, append(stringsToAny(chatJIDs), id)...)
	m, err := scanMessage(row)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return domain.Message{}, domain.ErrMessageNotFound
	case err != nil:
		return domain.Message{}, fmt.Errorf("failed to get message %s: %w", id, err)
	}
	return m, nil
}

type ListMessagesRequest struct {
	ChatJIDs     []string
	Before       time.Time
	After        time.Time
	IncomingOnly bool
	Query        string
	Limit        int
}

func (r *Repository) ListMessages(ctx context.Context, req *ListMessagesRequest) ([]domain.Message, error) {
	var (
		conds []string
		args  []any
	)
	if len(req.ChatJIDs) > 0 {
		conds = append(conds, "chat_jid IN ("+placeholders(len(req.ChatJIDs))+")")
		args = append(args, stringsToAny(req.ChatJIDs)...)
	}
	if !req.Before.IsZero() {
		conds = append(conds, "timestamp < ?")
		args = append(args, req.Before.Unix())
	}
	if !req.After.IsZero() {
		conds = append(conds, "timestamp >= ?")
		args = append(args, req.After.Unix())
	}
	if req.IncomingOnly {
		conds = append(conds, "from_me = 0")
	}
	if q := strings.TrimSpace(req.Query); q != "" {
		conds = append(conds, `text LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(q)+"%")
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	args = append(args, req.Limit)

	rows, err := r.db.QueryContext(ctx, `SELECT `+messageColumns+` FROM messages `+where+`
ORDER BY timestamp DESC, rowid DESC LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list messages: %w", err)
	}
	defer rows.Close()

	msgs := make([]domain.Message, 0)
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate messages: %w", err)
	}
	slices.Reverse(msgs)
	return msgs, nil
}

func (r *Repository) LastSentAt(ctx context.Context, chatJIDs []string) (time.Time, error) {
	if len(chatJIDs) == 0 {
		return time.Time{}, nil
	}
	var ts sql.NullInt64
	err := r.db.QueryRowContext(ctx, `SELECT MAX(timestamp) FROM messages
WHERE from_me = 1 AND chat_jid IN (`+placeholders(len(chatJIDs))+`)`, stringsToAny(chatJIDs)...).Scan(&ts)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to get last sent message time: %w", err)
	}
	return fromUnix(ts.Int64), nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanMessage(s scanner) (domain.Message, error) {
	var (
		m  domain.Message
		ts int64
	)
	if err := s.Scan(&m.ChatJID, &m.ID, &m.SenderJID, &m.SenderName, &m.FromMe, &ts, &m.Text, &m.MediaType, &m.QuotedID); err != nil {
		return domain.Message{}, err
	}
	m.Timestamp = fromUnix(ts)
	return m, nil
}

func stringsToAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
