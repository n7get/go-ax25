// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"errors"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
)

// PortMode controls how a port receives frames from the router.
type PortMode int

const (
	PortModeStatic      PortMode = iota // receives frames addressed to Destination
	PortModeDefault                     // receives frames with no matching static port
	PortModePromiscuous                 // receives all frames
	PortModeDigipeater                  // receives frames whose next digi hop matches Destination
	PortModeDynamic                     // receives frames addressed to a dynamically bound address
)

var (
	ErrPortAlreadyRegistered = errors.New("ax25: port already registered")
	ErrPortNotFound          = errors.New("ax25: port not found")
	ErrRouterClosed          = errors.New("ax25: router is closed")
	ErrTxQueueFull           = errors.New("ax25: router tx queue full")
	ErrNilFrame              = errors.New("ax25: nil frame")
	ErrNilPort               = errors.New("ax25: nil source port")
)

// PortStats holds per-port counters.
type PortStats struct {
	RxFrames uint64
	TxFrames uint64
	Dropped  uint64
}

// Port represents a logical connection point in the router.
type Port struct {
	Mode        PortMode
	Destination Address // used by Static, Digipeater, Dynamic modes
	OnRxFrame   FrameCallback
	UserData    any
	QueueDepth  int // 0 means use defaultPortQueueDepth

	// internal
	mu      sync.Mutex
	stats   PortStats
	txCh    chan *Frame
	quitCh  chan struct{}
	wg      sync.WaitGroup
	started bool
}

// Stats returns a snapshot of the port counters.
func (p *Port) Stats() PortStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stats
}

func (p *Port) start(queueDepth int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return
	}
	if p.QueueDepth > 0 {
		queueDepth = p.QueueDepth
	}
	p.txCh = make(chan *Frame, queueDepth)
	p.quitCh = make(chan struct{})
	p.started = true
	p.wg.Add(1)
	go p.worker()
}

func (p *Port) stop() {
	p.mu.Lock()
	if !p.started {
		p.mu.Unlock()
		return
	}
	close(p.quitCh)
	p.mu.Unlock()
	p.wg.Wait()
}

func (p *Port) worker() {
	defer p.wg.Done()
	for {
		select {
		case <-p.quitCh:
			return
		case f := <-p.txCh:
			if p.OnRxFrame != nil {
				p.OnRxFrame(f)
			}
			p.mu.Lock()
			p.stats.RxFrames++
			p.mu.Unlock()
		}
	}
}

func (p *Port) deliver(f *Frame) bool {
	select {
	case p.txCh <- f:
		p.mu.Lock()
		p.stats.TxFrames++
		p.mu.Unlock()
		return true
	default:
		p.mu.Lock()
		p.stats.Dropped++
		p.mu.Unlock()
		return false
	}
}

// ---------------------------------------------------------------------------
// Router
// ---------------------------------------------------------------------------

const defaultPortQueueDepth = 32

// Router routes AX.25 frames between registered ports.
type Router struct {
	mu     sync.RWMutex
	ports  []*Port
	closed atomic.Bool
}

// NewRouter creates a new Router.
func NewRouter() *Router {
	return &Router{}
}

// RegisterPort adds a port to the router and starts its worker goroutine.
func (r *Router) RegisterPort(p *Port) error {
	if r.closed.Load() {
		return ErrRouterClosed
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if slices.Contains(r.ports, p) {
		return ErrPortAlreadyRegistered
	}
	p.start(defaultPortQueueDepth)
	r.ports = append(r.ports, p)
	return nil
}

// UnregisterPort removes a port and stops its worker goroutine.
func (r *Router) UnregisterPort(p *Port) error {
	r.mu.Lock()
	for i, existing := range r.ports {
		if existing == p {
			r.ports = append(r.ports[:i], r.ports[i+1:]...)
			r.mu.Unlock()
			p.stop()
			return nil
		}
	}
	r.mu.Unlock()
	return ErrPortNotFound
}

// Close stops all ports and shuts down the router.
func (r *Router) Close() {
	r.closed.Store(true)
	r.mu.Lock()
	ports := make([]*Port, len(r.ports))
	copy(ports, r.ports)
	r.ports = nil
	r.mu.Unlock()
	for _, p := range ports {
		p.stop()
	}
}

// Send routes a frame from srcPort to all matching destination ports.
// srcPort may be nil (e.g. for internally generated frames).
// If srcPort is a Dynamic port with no Destination bound yet, it is
// automatically bound to f.Source (the callsign of the first sender).
func (r *Router) Send(f *Frame, srcPort *Port) error {
	if f == nil {
		return ErrNilFrame
	}
	if srcPort == nil {
		return ErrNilPort
	}
	if r.closed.Load() {
		return ErrRouterClosed
	}

	// Bind an unbound dynamic port to the source callsign of its first frame.
	if srcPort.Mode == PortModeDynamic {
		srcPort.mu.Lock()
		if srcPort.Destination.Callsign == "" {
			srcPort.Destination = f.Source
			slog.Debug("ax25: router: dynamic port bound",
				"src", f.Source.String(),
				"dst", f.Destination.String(),
			)
		}
		srcPort.mu.Unlock()
	}

	LogFrame(slog.LevelDebug, "ax25: router: send", f,
		slog.Bool("has_next_digi", r.hasUnrepeatedDigi(f)),
	)

	r.mu.RLock()
	ports := make([]*Port, len(r.ports))
	copy(ports, r.ports)
	r.mu.RUnlock()

	var (
		staticMatch  *Port
		defaultPorts []*Port
		promoPorts   []*Port
		digiPorts    []*Port
	)

	hasNextDigi := r.hasUnrepeatedDigi(f)

	for _, p := range ports {
		if p == srcPort {
			continue
		}
		switch p.Mode {
		case PortModeStatic, PortModeDynamic:
			// Only match when there is no pending digipeater hop
			if !hasNextDigi && p.Destination.Equal(f.Destination) {
				staticMatch = p
			}
		case PortModeDefault:
			defaultPorts = append(defaultPorts, p)
		case PortModePromiscuous:
			promoPorts = append(promoPorts, p)
		case PortModeDigipeater:
			if r.isNextDigiHop(f, p.Destination) {
				digiPorts = append(digiPorts, p)
			}
		}
	}

	// Deliver to promiscuous ports always.
	for _, p := range promoPorts {
		LogFrame(slog.LevelDebug, "ax25: router: deliver promiscuous", f)
		p.deliver(cloneFrame(f))
	}

	// Deliver to digipeater ports (with H-bit update).
	for _, p := range digiPorts {
		LogFrame(slog.LevelDebug, "ax25: router: deliver digipeater", f,
			slog.String("via", p.Destination.String()),
		)
		relayed := cloneFrame(f)
		r.markDigiHop(relayed, p.Destination)
		p.deliver(relayed)
	}

	// Deliver to static/dynamic match, or fall back to default ports.
	if staticMatch != nil {
		LogFrame(slog.LevelDebug, "ax25: router: deliver static/dynamic", f)
		staticMatch.deliver(cloneFrame(f))
	} else if len(defaultPorts) > 0 {
		LogFrame(slog.LevelDebug, "ax25: router: deliver default", f,
			slog.Int("count", len(defaultPorts)),
		)
		for _, p := range defaultPorts {
			p.deliver(cloneFrame(f))
		}
	} else {
		LogFrame(slog.LevelDebug, "ax25: router: no match, frame dropped", f)
	}

	return nil
}

// hasUnrepeatedDigi returns true if f has any digipeater hop not yet marked as repeated.
func (r *Router) hasUnrepeatedDigi(f *Frame) bool {
	for _, d := range f.Digipeaters {
		if !d.HasBeenRepeated {
			return true
		}
	}
	return false
}

// isNextDigiHop returns true if addr is the first unrepeated digipeater in f.
func (r *Router) isNextDigiHop(f *Frame, addr Address) bool {
	for _, d := range f.Digipeaters {
		if !d.HasBeenRepeated {
			return d.Equal(addr)
		}
	}
	return false
}

// markDigiHop sets the H-bit on the matching digipeater entry.
func (r *Router) markDigiHop(f *Frame, addr Address) {
	for i := range f.Digipeaters {
		if !f.Digipeaters[i].HasBeenRepeated && f.Digipeaters[i].Equal(addr) {
			f.Digipeaters[i].HasBeenRepeated = true
			return
		}
	}
}

func cloneFrame(f *Frame) *Frame {
	copy := *f
	if f.Digipeaters != nil {
		copy.Digipeaters = make([]Address, len(f.Digipeaters))
		_ = copy.Digipeaters
		for i, d := range f.Digipeaters {
			copy.Digipeaters[i] = d
		}
	}
	if f.Payload != nil {
		copy.Payload = make([]byte, len(f.Payload))
		_ = copy.Payload
		for i, b := range f.Payload {
			copy.Payload[i] = b
		}
	}
	return &copy
}
