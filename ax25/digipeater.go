// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"errors"
	"log/slog"
	"sync"
)

// ---------------------------------------------------------------------------
// Digipeater — MAC-layer frame relay
// ---------------------------------------------------------------------------

// DigiConfig holds digipeater configuration.
type DigiConfig struct {
	// Callsign is the digipeater's own callsign (e.g. "RELAY-1").
	// Empty string disables the digipeater.
	Callsign string
}

// DigiConfigFromConfig reads digipeater parameters from a Config.
func DigiConfigFromConfig(cfg *Config) DigiConfig {
	return DigiConfig{
		Callsign: cfg.GetStr(KeyDigiCallsign),
	}
}

// Digipeater registers a port with a Router in PortModeDigipeater and relays
// matching frames via a transmit callback.
type Digipeater struct {
	mu      sync.Mutex
	cfg     DigiConfig
	router  *Router
	port    *Port
	sendFn  func(*Frame) error
	enabled bool
}

var ErrDigiInvalidCallsign = errors.New("ax25: digipeater: invalid callsign")

// NewDigipeater creates a Digipeater. sendFn is called for each relayed frame;
// it must not block.
func NewDigipeater(cfg DigiConfig, router *Router, sendFn func(*Frame) error) (*Digipeater, error) {
	if sendFn == nil {
		return nil, errors.New("ax25: NewDigipeater: sendFn is required")
	}
	d := &Digipeater{
		cfg:    cfg,
		router: router,
		sendFn: sendFn,
	}
	if cfg.Callsign == "" {
		// Disabled — no port registered.
		return d, nil
	}
	addr, err := ParseAddress(cfg.Callsign)
	if err != nil {
		return nil, ErrDigiInvalidCallsign
	}
	d.port = &Port{
		Mode:        PortModeDigipeater,
		Destination: addr,
		OnRxFrame:   d.onFrame,
	}
	if err := router.RegisterPort(d.port); err != nil {
		return nil, err
	}
	d.enabled = true
	slog.Info("ax25: digipeater enabled", "callsign", cfg.Callsign)
	return d, nil
}

// Close unregisters the digipeater port from the router.
func (d *Digipeater) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.port != nil && d.router != nil {
		if err := d.router.UnregisterPort(d.port); err != nil {
			slog.Warn("ax25: digipeater: unregister port", "err", err)
		}
		d.port = nil
	}
	d.enabled = false
}

// IsEnabled reports whether the digipeater is active.
func (d *Digipeater) IsEnabled() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.enabled
}

// onFrame is called by the router worker goroutine for each matching frame.
// Must not block.
func (d *Digipeater) onFrame(f *Frame) {
	if err := d.sendFn(f); err != nil {
		slog.Warn("ax25: digipeater: relay transmit failed", "err", err)
	}
}
