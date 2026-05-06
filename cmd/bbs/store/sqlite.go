// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package store

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	_ "modernc.org/sqlite"
)

const (
	flagPrivate  uint8 = 0x01
	flagBulletin uint8 = 0x02
)

// SQLiteStore implements MessageStore backed by SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens (or creates) a SQLite database at the given path.
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %q: %w", dbPath, err)
	}

	// Enable WAL mode for better concurrent read performance.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: set WAL: %w", err)
	}

	if err := createTables(db); err != nil {
		db.Close()
		return nil, err
	}

	return &SQLiteStore{db: db}, nil
}

func createTables(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS messages (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		from_call  TEXT    NOT NULL,
		to_call    TEXT    NOT NULL,
		subject    TEXT    NOT NULL DEFAULT '',
		body       TEXT    NOT NULL DEFAULT '',
		flags      INTEGER NOT NULL DEFAULT 0,
		size       INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS last_read (
		callsign   TEXT PRIMARY KEY,
		message_id INTEGER NOT NULL DEFAULT 0
	);

	CREATE INDEX IF NOT EXISTS idx_messages_to   ON messages(to_call);
	CREATE INDEX IF NOT EXISTS idx_messages_from ON messages(from_call);
	`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("sqlite: create tables: %w", err)
	}
	return nil
}

// Close closes the database.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// Store saves a new message.
func (s *SQLiteStore) Store(from, to, subject, body string, flags uint8) (uint32, error) {
	res, err := s.db.Exec(
		"INSERT INTO messages (from_call, to_call, subject, body, flags, size) VALUES (?, ?, ?, ?, ?, ?)",
		from, to, subject, body, flags, len(body),
	)
	if err != nil {
		return 0, fmt.Errorf("sqlite: store: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("sqlite: last insert id: %w", err)
	}
	slog.Debug("sqlite: stored message", "id", id, "from", from, "to", to)
	return uint32(id), nil
}

// Read retrieves a complete message by ID.
func (s *SQLiteStore) Read(id uint32) (*Message, error) {
	row := s.db.QueryRow(
		"SELECT id, from_call, to_call, subject, body, flags, size, created_at FROM messages WHERE id = ?",
		id,
	)
	msg := &Message{}
	var createdAt string
	err := row.Scan(&msg.ID, &msg.From, &msg.To, &msg.Subject, &msg.Body,
		&msg.Flags, &msg.Size, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("message %d not found", id)
		}
		return nil, fmt.Errorf("sqlite: read %d: %w", id, err)
	}
	msg.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	return msg, nil
}

// Delete removes a message by ID.
func (s *SQLiteStore) Delete(id uint32) error {
	res, err := s.db.Exec("DELETE FROM messages WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("sqlite: delete %d: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("message %d not found", id)
	}
	slog.Debug("sqlite: deleted message", "id", id)
	return nil
}

// Find returns the index entry for a message.
func (s *SQLiteStore) Find(id uint32) (*MessageIndex, error) {
	row := s.db.QueryRow(
		"SELECT id, from_call, to_call, subject, flags, size, created_at FROM messages WHERE id = ?",
		id,
	)
	idx := &MessageIndex{}
	var createdAt string
	err := row.Scan(&idx.ID, &idx.From, &idx.To, &idx.Subject, &idx.Flags, &idx.Size, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("sqlite: find %d: %w", id, err)
	}
	idx.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	return idx, nil
}

// List returns message index entries matching the criteria.
func (s *SQLiteStore) List(limit int, mineOnly bool, callsign string, isSysop bool) ([]MessageIndex, error) {
	cols := "id, from_call, to_call, subject, flags, size, created_at"
	var where string
	var args []any

	if mineOnly {
		where = " WHERE from_call = ? OR to_call = ?"
		args = append(args, callsign, callsign)
	} else if !isSysop {
		where = " WHERE (flags & ?) != 0 OR to_call = ? OR from_call = ?"
		args = append(args, flagBulletin, callsign, callsign)
	}

	inner := "SELECT " + cols + " FROM messages" + where

	var query string
	if limit > 0 {
		query = "SELECT " + cols + " FROM (" + inner + " ORDER BY id DESC LIMIT ?) ORDER BY id ASC"
		args = append(args, limit)
	} else {
		query = inner + " ORDER BY id ASC"
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list: %w", err)
	}
	defer rows.Close()

	var results []MessageIndex
	for rows.Next() {
		var idx MessageIndex
		var createdAt string
		if err := rows.Scan(&idx.ID, &idx.From, &idx.To, &idx.Subject, &idx.Flags, &idx.Size, &createdAt); err != nil {
			return nil, fmt.Errorf("sqlite: list scan: %w", err)
		}
		idx.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		results = append(results, idx)
	}
	return results, rows.Err()
}

// GetLastRead returns the last-read message ID for a callsign.
func (s *SQLiteStore) GetLastRead(callsign string) (uint32, error) {
	row := s.db.QueryRow("SELECT message_id FROM last_read WHERE callsign = ?", callsign)
	var id uint32
	err := row.Scan(&id)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, fmt.Errorf("sqlite: get last read: %w", err)
	}
	return id, nil
}

// SetLastRead updates the last-read message ID for a callsign.
func (s *SQLiteStore) SetLastRead(callsign string, id uint32) error {
	_, err := s.db.Exec(
		"INSERT INTO last_read (callsign, message_id) VALUES (?, ?) ON CONFLICT(callsign) DO UPDATE SET message_id = ?",
		callsign, id, id,
	)
	if err != nil {
		return fmt.Errorf("sqlite: set last read: %w", err)
	}
	return nil
}
