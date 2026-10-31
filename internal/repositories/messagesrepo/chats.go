package messagesrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/edcnogueira/meowmcp/internal/domain"
)

func (r *Repository) UpsertChat(ctx context.Context, chat domain.Chat) error {
	_, err := r.db.ExecContext(ctx, upsertChatSQL, chat.JID, chat.Name, chat.IsGroup, unix(chat.LastMessageAt))
	if err != nil {
		return fmt.Errorf("failed to upsert chat %s: %w", chat.JID, err)
	}
	return nil
}

const upsertChatSQL = `
INSERT INTO chats (jid, name, is_group, last_message_at) VALUES (?, ?, ?, ?)
ON CONFLICT (jid) DO UPDATE SET
	name = CASE WHEN excluded.name <> '' THEN excluded.name ELSE chats.name END,
	is_group = excluded.is_group,
	last_message_at = MAX(chats.last_message_at, excluded.last_message_at)`

func (r *Repository) RenameChat(ctx context.Context, jid, name string) error {
	if name == "" {
		return nil
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE chats SET name = ? WHERE jid = ? AND name <> ?`, name, jid, name); err != nil {
		return fmt.Errorf("failed to rename chat %s: %w", jid, err)
	}
	return nil
}

func (r *Repository) GetChat(ctx context.Context, jid string) (domain.Chat, bool, error) {
	var (
		chat   domain.Chat
		lastAt int64
	)
	err := r.db.QueryRowContext(ctx, `SELECT jid, name, is_group, last_message_at FROM chats WHERE jid = ?`, jid).
		Scan(&chat.JID, &chat.Name, &chat.IsGroup, &lastAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return domain.Chat{}, false, nil
	case err != nil:
		return domain.Chat{}, false, fmt.Errorf("failed to get chat %s: %w", jid, err)
	}
	chat.LastMessageAt = fromUnix(lastAt)
	return chat, true, nil
}

type ListChatsRequest struct {
	Query  string
	Limit  int
	Offset int
}

func (r *Repository) ListChats(ctx context.Context, req *ListChatsRequest) ([]domain.Chat, error) {
	var (
		where string
		args  []any
	)
	if q := strings.TrimSpace(req.Query); q != "" {
		where = `WHERE c.name LIKE ? ESCAPE '\' OR c.jid LIKE ? ESCAPE '\'`
		like := "%" + escapeLike(q) + "%"
		args = append(args, like, like)
	}
	args = append(args, req.Limit, req.Offset)

	rows, err := r.db.QueryContext(ctx, `
SELECT c.jid, c.name, c.is_group, c.last_message_at,
       (SELECT seq FROM messages WHERE chat_jid = c.jid ORDER BY timestamp DESC, seq DESC LIMIT 1)
FROM chats c
`+where+`
ORDER BY c.last_message_at DESC, c.jid
LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list chats: %w", err)
	}
	defer rows.Close()

	var (
		chats   = make([]domain.Chat, 0)
		lastSeq = make([]int64, 0)
	)
	for rows.Next() {
		var (
			chat   domain.Chat
			lastAt int64
			seq    sql.NullInt64
		)
		if err := rows.Scan(&chat.JID, &chat.Name, &chat.IsGroup, &lastAt, &seq); err != nil {
			return nil, fmt.Errorf("failed to scan chat: %w", err)
		}
		chat.LastMessageAt = fromUnix(lastAt)
		chats = append(chats, chat)
		lastSeq = append(lastSeq, seq.Int64)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate chats: %w", err)
	}
	_ = rows.Close()

	wanted := make([]int64, 0, len(lastSeq))
	for _, seq := range lastSeq {
		if seq != 0 {
			wanted = append(wanted, seq)
		}
	}
	last, err := r.messagesBySeq(ctx, wanted)
	if err != nil {
		return nil, fmt.Errorf("failed to load last messages: %w", err)
	}
	for i := range chats {
		if m, ok := last[lastSeq[i]]; ok {
			chats[i].LastMessage = &m
		}
	}
	return chats, nil
}

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func fromUnix(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
