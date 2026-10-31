package messagesrepo

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite"
)

var migrations = []string{
	`CREATE TABLE chats (
		jid             TEXT PRIMARY KEY,
		name            TEXT NOT NULL DEFAULT '',
		is_group        INTEGER NOT NULL DEFAULT 0,
		last_message_at INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE messages (
		chat_jid    TEXT NOT NULL,
		id          TEXT NOT NULL,
		sender_jid  TEXT NOT NULL DEFAULT '',
		sender_name TEXT NOT NULL DEFAULT '',
		from_me     INTEGER NOT NULL DEFAULT 0,
		timestamp   INTEGER NOT NULL,
		text        TEXT NOT NULL DEFAULT '',
		media_type  TEXT NOT NULL DEFAULT '',
		quoted_id   TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (chat_jid, id)
	);
	CREATE INDEX messages_chat_timestamp ON messages (chat_jid, timestamp);
	CREATE INDEX chats_last_message_at ON chats (last_message_at);`,
	`ALTER TABLE messages ADD COLUMN media_ref BLOB;
	ALTER TABLE messages ADD COLUMN transcript TEXT NOT NULL DEFAULT '';`,
	`ALTER TABLE messages ADD COLUMN seq INTEGER;
	ALTER TABLE messages ADD COLUMN agent_name TEXT NOT NULL DEFAULT '';
	ALTER TABLE messages ADD COLUMN mentions TEXT NOT NULL DEFAULT '';
	ALTER TABLE messages ADD COLUMN transcript_unclear INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE messages ADD COLUMN media_file TEXT NOT NULL DEFAULT '';
	ALTER TABLE messages ADD COLUMN view_once INTEGER NOT NULL DEFAULT 0;
	UPDATE messages SET seq = rowid;
	CREATE UNIQUE INDEX messages_seq ON messages (seq);`,
}

type Repository struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Repository, error) {
	db, err := sql.Open("sqlite", DSN(path))
	if err != nil {
		return nil, fmt.Errorf("failed to open messages database: %w", err)
	}
	r := &Repository{db: db}
	if err := r.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return r, nil
}

func DSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "synchronous(NORMAL)")
	return "file:" + path + "?" + q.Encode()
}

func (r *Repository) Close() error {
	return r.db.Close()
}

func (r *Repository) migrate(ctx context.Context) error {
	var version int
	if err := r.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("failed to read schema version: %w", err)
	}
	for i := version; i < len(migrations); i++ {
		tx, err := r.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to begin migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed to apply migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed to record migration %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration %d: %w", i+1, err)
		}
	}
	return nil
}
