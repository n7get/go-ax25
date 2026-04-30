// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

// Package store defines the pluggable message storage interface for the BBS.
package store

import "time"

// MessageIndex holds the metadata for a stored message (no body).
type MessageIndex struct {
	ID        uint32
	From      string
	To        string
	Subject   string
	Flags     uint8
	Size      int
	CreatedAt time.Time
}

// Message holds a complete message including body.
type Message struct {
	MessageIndex
	Body string
}

// MessageStore is the interface that any BBS message backend must implement.
type MessageStore interface {
	// Close releases resources held by the store.
	Close() error

	// Store saves a new message and returns its ID.
	Store(from, to, subject, body string, flags uint8) (uint32, error)

	// Read retrieves a complete message by ID.
	Read(id uint32) (*Message, error)

	// Delete removes a message by ID.
	Delete(id uint32) error

	// Find returns the index entry for a message, or nil if not found.
	Find(id uint32) (*MessageIndex, error)

	// List returns message index entries.
	// If limit > 0, only the most recent `limit` messages are returned.
	// If mineOnly is true, only messages to/from `callsign` are returned.
	// If isSysop is true, all messages are visible regardless of flags.
	List(limit int, mineOnly bool, callsign string, isSysop bool) ([]MessageIndex, error)

	// GetLastRead returns the last-read message ID for a callsign.
	GetLastRead(callsign string) (uint32, error)

	// SetLastRead updates the last-read message ID for a callsign.
	SetLastRead(callsign string, id uint32) error
}
