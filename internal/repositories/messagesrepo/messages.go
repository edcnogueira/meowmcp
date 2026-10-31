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

const messageColumns = `seq, chat_jid, id, sender_jid, sender_name, agent_name, from_me, timestamp, text, media_type, view_once,
quoted_id, mentions, transcript, transcript_unclear, media_ref IS NOT NULL, media_file`

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
INSERT INTO messages (chat_jid, id, sender_jid, sender_name, agent_name, from_me, timestamp, text, media_type, view_once, quoted_id, mentions, media_ref, seq)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, (SELECT IFNULL(MAX(seq), 0) + 1 FROM messages))
ON CONFLICT (chat_jid, id) DO UPDATE SET
	sender_name = CASE WHEN excluded.sender_name <> '' THEN excluded.sender_name ELSE messages.sender_name END,
	agent_name = excluded.agent_name,
	text = excluded.text,
	media_type = excluded.media_type,
	quoted_id = excluded.quoted_id,
	mentions = excluded.mentions,
	view_once = MAX(messages.view_once, excluded.view_once),
	media_ref = COALESCE(excluded.media_ref, messages.media_ref)`)
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
		if _, err := msgStmt.ExecContext(ctx, m.ChatJID, m.ID, m.SenderJID, m.SenderName, m.AgentName, m.FromMe,
			unix(m.Timestamp), m.Text, m.MediaType, m.ViewOnce, m.QuotedID, joinMentions(m.Mentions), nullBytes(m.MediaRef)); err != nil {
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

func (r *Repository) GetMedia(ctx context.Context, chatJIDs []string, id string) (domain.Message, error) {
	if len(chatJIDs) == 0 {
		return domain.Message{}, domain.ErrMessageNotFound
	}
	row := r.db.QueryRowContext(ctx, `SELECT `+messageColumns+`, media_ref FROM messages
WHERE chat_jid IN (`+placeholders(len(chatJIDs))+`) AND id = ?`, append(stringsToAny(chatJIDs), id)...)
	var ref []byte
	m, err := scanMessage(row, &ref)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return domain.Message{}, domain.ErrMessageNotFound
	case err != nil:
		return domain.Message{}, fmt.Errorf("failed to get media of message %s: %w", id, err)
	}
	m.MediaRef = ref
	return m, nil
}

func (r *Repository) SetTranscript(ctx context.Context, chatJIDs []string, id, transcript string, unclear bool) error {
	if len(chatJIDs) == 0 {
		return nil
	}
	args := append(append([]any{transcript, unclear}, stringsToAny(chatJIDs)...), id)
	_, err := r.db.ExecContext(ctx, `UPDATE messages SET transcript = ?, transcript_unclear = ?
WHERE chat_jid IN (`+placeholders(len(chatJIDs))+`) AND id = ?`, args...)
	if err != nil {
		return fmt.Errorf("failed to store transcript of message %s: %w", id, err)
	}
	return nil
}

func (r *Repository) SetMediaFile(ctx context.Context, chatJIDs []string, id, path string) error {
	if len(chatJIDs) == 0 {
		return nil
	}
	args := append(append([]any{path}, stringsToAny(chatJIDs)...), id)
	_, err := r.db.ExecContext(ctx, `UPDATE messages SET media_file = ? WHERE chat_jid IN (`+placeholders(len(chatJIDs))+`) AND id = ?`, args...)
	if err != nil {
		return fmt.Errorf("failed to store media file of message %s: %w", id, err)
	}
	return nil
}

type ListMessagesRequest struct {
	ChatJIDs     []string
	Before       time.Time
	After        time.Time
	IncomingOnly bool
	OutgoingOnly bool
	Query        string
	Forward      bool
	AfterSeq     int64
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
	switch {
	case req.IncomingOnly:
		conds = append(conds, "from_me = 0")
	case req.OutgoingOnly:
		conds = append(conds, "from_me = 1")
	}
	if q := strings.TrimSpace(req.Query); q != "" {
		conds = append(conds, `(text LIKE ? ESCAPE '\' OR transcript LIKE ? ESCAPE '\')`)
		like := "%" + escapeLike(q) + "%"
		args = append(args, like, like)
	}
	order := "timestamp DESC, seq DESC"
	if req.Forward {
		conds = append(conds, "seq > ?")
		args = append(args, req.AfterSeq)
		order = "seq ASC"
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	args = append(args, req.Limit)

	msgs, err := r.queryMessages(ctx, `SELECT `+messageColumns+` FROM messages `+where+` ORDER BY `+order+` LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list messages: %w", err)
	}
	if !req.Forward {
		slices.Reverse(msgs)
	}
	return msgs, nil
}

func (r *Repository) MaxSeq(ctx context.Context, chatJIDs []string) (int64, error) {
	query, args := `SELECT IFNULL(MAX(seq), 0) FROM messages`, []any(nil)
	if len(chatJIDs) > 0 {
		query += ` WHERE chat_jid IN (` + placeholders(len(chatJIDs)) + `)`
		args = stringsToAny(chatJIDs)
	}
	var seq int64
	if err := r.db.QueryRowContext(ctx, query, args...).Scan(&seq); err != nil {
		return 0, fmt.Errorf("failed to get latest message position: %w", err)
	}
	return seq, nil
}

func (r *Repository) messagesBySeq(ctx context.Context, seqs []int64) (map[int64]domain.Message, error) {
	if len(seqs) == 0 {
		return nil, nil
	}
	args := make([]any, len(seqs))
	for i, s := range seqs {
		args[i] = s
	}
	msgs, err := r.queryMessages(ctx, `SELECT `+messageColumns+` FROM messages WHERE seq IN (`+placeholders(len(seqs))+`)`, args...)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]domain.Message, len(msgs))
	for _, m := range msgs {
		out[m.Seq] = m
	}
	return out, nil
}

func (r *Repository) queryMessages(ctx context.Context, query string, args ...any) ([]domain.Message, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
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
	return msgs, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanMessage(s scanner, extra ...any) (domain.Message, error) {
	var (
		m        domain.Message
		ts       int64
		mentions string
	)
	dest := append([]any{&m.Seq, &m.ChatJID, &m.ID, &m.SenderJID, &m.SenderName, &m.AgentName, &m.FromMe, &ts, &m.Text,
		&m.MediaType, &m.ViewOnce, &m.QuotedID, &mentions, &m.Transcript, &m.TranscriptUnclear, &m.HasMediaKey, &m.MediaFile}, extra...)
	if err := s.Scan(dest...); err != nil {
		return domain.Message{}, err
	}
	m.Timestamp = fromUnix(ts)
	m.Mentions = splitMentions(mentions)
	return m, nil
}

func joinMentions(mentions []domain.Mention) string {
	jids := make([]string, len(mentions))
	for i, m := range mentions {
		jids[i] = m.JID
	}
	return strings.Join(jids, ",")
}

func splitMentions(s string) []domain.Mention {
	if s == "" {
		return nil
	}
	jids := strings.Split(s, ",")
	out := make([]domain.Mention, len(jids))
	for i, jid := range jids {
		out[i] = domain.Mention{JID: jid}
	}
	return out
}

func nullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func stringsToAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
