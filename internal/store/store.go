// Package store keeps timers and presets in SQLite, so they survive restarts.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure Go driver: no CGO, works in a distroless image
)

var ErrNotFound = errors.New("not found")

type Timer struct {
	ID     int64
	ChatID int64
	Label  string
	FireAt time.Time
}

type Preset struct {
	ChatID int64
	Name   string
	Length time.Duration
}

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS timers (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id    INTEGER NOT NULL,
	label      TEXT    NOT NULL DEFAULT '',
	fire_at    INTEGER NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS timers_chat ON timers(chat_id);
CREATE TABLE IF NOT EXISTS presets (
	chat_id INTEGER NOT NULL,
	name    TEXT    NOT NULL,
	seconds INTEGER NOT NULL,
	PRIMARY KEY (chat_id, name)
);`

// Open opens (or creates) the database file and applies the schema.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer; a single connection avoids "database is locked" errors.
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) AddTimer(ctx context.Context, t Timer) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO timers (chat_id, label, fire_at, created_at) VALUES (?, ?, ?, ?)`,
		t.ChatID, t.Label, t.FireAt.Unix(), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeleteTimer removes a timer of the given chat; a chat can't delete other chats' timers.
func (s *Store) DeleteTimer(ctx context.Context, chatID, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM timers WHERE id = ? AND chat_id = ?`, id, chatID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CountTimers(ctx context.Context, chatID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM timers WHERE chat_id = ?`, chatID).Scan(&n)
	return n, err
}

// ListTimers returns timers of one chat, or of all chats when chatID is 0, soonest first.
func (s *Store) ListTimers(ctx context.Context, chatID int64) ([]Timer, error) {
	q := `SELECT id, chat_id, label, fire_at FROM timers`
	var args []any
	if chatID != 0 {
		q += ` WHERE chat_id = ?`
		args = append(args, chatID)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY fire_at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Timer
	for rows.Next() {
		var t Timer
		var fireAt int64
		if err := rows.Scan(&t.ID, &t.ChatID, &t.Label, &fireAt); err != nil {
			return nil, err
		}
		t.FireAt = time.Unix(fireAt, 0)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) SavePreset(ctx context.Context, p Preset) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO presets (chat_id, name, seconds) VALUES (?, ?, ?)
		 ON CONFLICT (chat_id, name) DO UPDATE SET seconds = excluded.seconds`,
		p.ChatID, p.Name, int64(p.Length/time.Second))
	return err
}

func (s *Store) DeletePreset(ctx context.Context, chatID int64, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM presets WHERE chat_id = ? AND name = ?`, chatID, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetPreset(ctx context.Context, chatID int64, name string) (Preset, error) {
	p := Preset{ChatID: chatID, Name: name}
	var sec int64
	err := s.db.QueryRowContext(ctx, `SELECT seconds FROM presets WHERE chat_id = ? AND name = ?`, chatID, name).Scan(&sec)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	p.Length = time.Duration(sec) * time.Second
	return p, err
}

func (s *Store) ListPresets(ctx context.Context, chatID int64) ([]Preset, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, seconds FROM presets WHERE chat_id = ? ORDER BY seconds, name`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Preset
	for rows.Next() {
		p := Preset{ChatID: chatID}
		var sec int64
		if err := rows.Scan(&p.Name, &sec); err != nil {
			return nil, err
		}
		p.Length = time.Duration(sec) * time.Second
		out = append(out, p)
	}
	return out, rows.Err()
}
