// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

// Package heard maintains an MRU-ordered list of recently heard callsigns.
package heard

import (
	"strings"
	"sync"
	"time"
)

// Entry represents a single heard station.
type Entry struct {
	Callsign string
	Time     time.Time
}

// List is a thread-safe MRU-ordered heard list.
type List struct {
	mu      sync.Mutex
	entries []Entry
	max     int
}

// New creates a new heard list with the given maximum size.
func New(max int) *List {
	if max <= 0 {
		max = 20
	}
	return &List{
		entries: make([]Entry, 0, max),
		max:     max,
	}
}

// Add records a callsign as heard. If already present, it moves to the front.
func (l *List) Add(callsign string) {
	callsign = strings.ToUpper(callsign)
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	// Check if already present; if so, move to front.
	for i, e := range l.entries {
		if strings.EqualFold(e.Callsign, callsign) {
			// Remove from current position.
			l.entries = append(l.entries[:i], l.entries[i+1:]...)
			// Prepend.
			l.entries = append([]Entry{{Callsign: callsign, Time: now}}, l.entries...)
			return
		}
	}

	// New entry: prepend.
	l.entries = append([]Entry{{Callsign: callsign, Time: now}}, l.entries...)

	// Trim to max.
	if len(l.entries) > l.max {
		l.entries = l.entries[:l.max]
	}
}

// Entries returns a snapshot of the heard list (most recent first).
func (l *List) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()

	out := make([]Entry, len(l.entries))
	copy(out, l.entries)
	return out
}
