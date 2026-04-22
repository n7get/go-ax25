// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Beacon — periodic UI frame transmission
// ---------------------------------------------------------------------------

// BeaconConfig holds beacon parameters read from Config.
type BeaconConfig struct {
	Source      string        // callsign, e.g. "N7GET-1"
	Destination string        // e.g. "BEACON"
	Via         string        // comma-separated digipeater path
	Text        string        // payload text (supports \r \n \xHH escapes)
	Every       time.Duration // 0 = disabled
}

// BeaconConfigFromConfig reads beacon parameters from a Config.
func BeaconConfigFromConfig(cfg *Config) BeaconConfig {
	minutes := cfg.GetInt(KeyBeaconEvery)
	var every time.Duration
	if minutes > 0 {
		every = time.Duration(minutes) * time.Minute
	}
	return BeaconConfig{
		Source:      cfg.GetStr(KeyBeaconSource),
		Destination: cfg.GetStr(KeyBeaconDestination),
		Via:         cfg.GetStr(KeyBeaconVia),
		Text:        cfg.GetStr(KeyBeaconText),
		Every:       every,
	}
}

// Beacon periodically transmits a UI frame through a send callback.
type Beacon struct {
	mu     sync.Mutex
	cfg    BeaconConfig
	sendFn func(*Frame) error

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewBeacon creates a Beacon. sendFn is called for each beacon frame; it must
// not block.
func NewBeacon(cfg BeaconConfig, sendFn func(*Frame) error) *Beacon {
	return &Beacon{cfg: cfg, sendFn: sendFn}
}

// Start begins the periodic beacon goroutine. ctx cancellation stops it.
func (b *Beacon) Start(ctx context.Context) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancel != nil {
		return // already running
	}
	ctx, b.cancel = context.WithCancel(ctx)
	b.wg.Add(1)
	go b.run(ctx)
}

// Stop halts the beacon goroutine and waits for it to exit.
func (b *Beacon) Stop() {
	b.mu.Lock()
	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
	b.mu.Unlock()
	b.wg.Wait()
}

// Trigger sends a beacon frame immediately (from the caller's goroutine).
func (b *Beacon) Trigger() error {
	b.mu.Lock()
	cfg := b.cfg
	b.mu.Unlock()
	return b.send(cfg)
}

// UpdateConfig replaces the beacon configuration at runtime.
func (b *Beacon) UpdateConfig(cfg BeaconConfig) {
	b.mu.Lock()
	b.cfg = cfg
	b.mu.Unlock()
}

func (b *Beacon) run(ctx context.Context) {
	defer b.wg.Done()
	for {
		b.mu.Lock()
		cfg := b.cfg
		b.mu.Unlock()

		if cfg.Every <= 0 || cfg.Source == "" {
			// Disabled — wait for context cancellation.
			<-ctx.Done()
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(cfg.Every):
			if err := b.send(cfg); err != nil {
				slog.Warn("ax25: beacon send failed", "err", err)
			}
		}
	}
}

func (b *Beacon) send(cfg BeaconConfig) error {
	if cfg.Source == "" {
		return nil
	}
	slog.Debug("ax25: beacon: send frame", "src", cfg.Source, "dst", cfg.Destination, "text", cfg.Text)
	src, err := ParseAddress(cfg.Source)
	if err != nil {
		return err
	}
	dst, err := ParseAddress(cfg.Destination)
	if err != nil {
		return err
	}

	var digis []Address
	if cfg.Via != "" {
		for _, part := range strings.Split(cfg.Via, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			d, err := ParseAddress(part)
			if err != nil {
				slog.Warn("ax25: beacon: invalid digipeater", "addr", part, "err", err)
				continue
			}
			digis = append(digis, d)
		}
	}

	text := unescapeText(cfg.Text)
	if len(text) > MaxInfoLen {
		text = text[:MaxInfoLen]
	}

	f := &Frame{
		Destination: dst,
		Source:      src,
		Digipeaters: digis,
		IsCommand:   true,
		Type:        FrameUI,
		Control:     CtrlUI,
		PID:         PIDNone,
		Payload:     []byte(text),
	}

	if b.sendFn != nil {
		return b.sendFn(f)
	}
	return nil
}

// unescapeText expands \r, \n, \t, \\, \xHH escape sequences.
func unescapeText(s string) string {
	var out []byte
	i := 0
	for i < len(s) {
		if s[i] != '\\' || i+1 >= len(s) {
			out = append(out, s[i])
			i++
			continue
		}
		i++
		switch s[i] {
		case 'r':
			out = append(out, '\r')
		case 'n':
			out = append(out, '\n')
		case 't':
			out = append(out, '\t')
		case '\\':
			out = append(out, '\\')
		case 'x':
			if i+2 < len(s) {
				hi := hexNibble(s[i+1])
				lo := hexNibble(s[i+2])
				if hi >= 0 && lo >= 0 {
					out = append(out, byte(hi<<4|lo))
					i += 2
				} else {
					out = append(out, '\\', s[i])
				}
			} else {
				out = append(out, '\\', s[i])
			}
		default:
			out = append(out, '\\', s[i])
		}
		i++
	}
	return string(out)
}

func hexNibble(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}
