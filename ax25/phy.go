// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
)

// PHY is the interface implemented by all physical-layer drivers.
type PHY interface {
	// RxFrames returns the channel on which decoded inbound frames are delivered.
	RxFrames() <-chan *Frame
	// SendFrame encodes and enqueues a frame for transmission. Non-blocking.
	SendFrame(f *Frame) error
	// Start begins background goroutines. ctx cancellation stops them.
	Start(ctx context.Context) error
	// Stop waits for all goroutines to exit and closes RxFrames.
	Stop()
}

// Sentinel errors returned by PHY implementations.
var (
	ErrPHYClosed = errors.New("ax25: PHY is closed")
	ErrPHYTxFull = errors.New("ax25: PHY tx queue full")
)

// ---------------------------------------------------------------------------
// KISSSerialPHY — KISS over any io.ReadWriter (serial, TCP, pipe, …)
// ---------------------------------------------------------------------------

// KISSSerialPHYConfig holds tunable parameters for KISSSerialPHY.
type KISSSerialPHYConfig struct {
	Port         byte
	RxQueueDepth int
	TxQueueDepth int
	ReadBufSize  int
}

// KISSSerialPHY implements PHY over a KISS-framed io.ReadWriter.
type KISSSerialPHY struct {
	cfg         KISSSerialPHYConfig
	rw          io.ReadWriter
	rxCh        chan *Frame
	txCh        chan []byte
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	once        sync.Once
	closeRxOnce sync.Once
	closed      bool
	mu          sync.Mutex
}

// NewKISSSerialPHY creates a KISSSerialPHY that reads from and writes to rw.
func NewKISSSerialPHY(rw io.ReadWriter, cfg KISSSerialPHYConfig) *KISSSerialPHY {
	if cfg.RxQueueDepth <= 0 {
		cfg.RxQueueDepth = 64
	}
	if cfg.TxQueueDepth <= 0 {
		cfg.TxQueueDepth = 32
	}
	if cfg.ReadBufSize <= 0 {
		cfg.ReadBufSize = 1024
	}
	return &KISSSerialPHY{
		cfg:  cfg,
		rw:   rw,
		rxCh: make(chan *Frame, cfg.RxQueueDepth),
		txCh: make(chan []byte, cfg.TxQueueDepth),
	}
}

// RxFrames returns the channel on which decoded inbound frames are delivered.
func (p *KISSSerialPHY) RxFrames() <-chan *Frame { return p.rxCh }

// SendFrame encodes f as a KISS frame and enqueues it for transmission.
func (p *KISSSerialPHY) SendFrame(f *Frame) error {
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return ErrPHYClosed
	}
	raw, err := f.Encode()
	if err != nil {
		return fmt.Errorf("ax25: KISSSerialPHY.SendFrame: %w", err)
	}
	encoded := KISSEncode(p.cfg.Port, 0, raw)
	select {
	case p.txCh <- encoded:
		return nil
	default:
		return ErrPHYTxFull
	}
}

// Start begins the reader and writer goroutines.
func (p *KISSSerialPHY) Start(ctx context.Context) error {
	p.once.Do(func() {
		ctx, p.cancel = context.WithCancel(ctx)
		p.wg.Add(2)
		go p.rxLoop(ctx)
		go p.txLoop(ctx)
	})
	return nil
}

// Stop cancels the context and waits for both goroutines to exit.
// It drains any pending transmit frames before closing the underlying
// connection so that queued frames (e.g. a UA sent just before shutdown)
// are not silently discarded.
func (p *KISSSerialPHY) Stop() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	if p.cancel != nil {
		p.cancel()
	}
	if closer, ok := p.rw.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
	// Now drain txCh, but writes will fail fast since rw is closed.
	for {
		select {
		case encoded := <-p.txCh:
			_, _ = p.rw.Write(encoded)
		default:
			goto drained
		}
	}
drained:
	p.wg.Wait()
	p.closeRxOnce.Do(func() { close(p.rxCh) })
}

func (p *KISSSerialPHY) rxLoop(ctx context.Context) {
	defer p.wg.Done()
	defer p.closeRxOnce.Do(func() { close(p.rxCh) })
	dec := NewKISSDecoder(func(port, cmd byte, data []byte) {
		if cmd != 0 {
			return
		}
		f, err := ParseFrame(data)
		if err != nil {
			slog.Debug("ax25: KISSSerialPHY: rx parse error", "err", err)
			return
		}
		LogFrame(slog.LevelDebug, "ax25: KISSSerialPHY: rx", f)
		select {
		case p.rxCh <- f:
		default:
			slog.Warn("ax25: KISSSerialPHY: rx queue full, dropping frame")
		}
	})
	buf := make([]byte, p.cfg.ReadBufSize)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, err := p.rw.Read(buf)
		if n > 0 {
			dec.Write(buf[:n])
		}
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if !errors.Is(err, io.EOF) {
				slog.Error("ax25: KISSSerialPHY: read error", "err", err)
			}
			return
		}
	}
}

func (p *KISSSerialPHY) txLoop(ctx context.Context) {
	defer p.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case encoded := <-p.txCh:
			if _, err := p.rw.Write(encoded); err != nil {
				slog.Error("ax25: KISSSerialPHY: write error", "err", err)
				return
			}
		}
	}
}

// Compile-time assertion.
var _ PHY = (*KISSSerialPHY)(nil)

// KISSSerialConfigFromConfig populates KISSSerialPHYConfig from Config.
func KISSSerialConfigFromConfig(cfg *Config) KISSSerialPHYConfig {
	return KISSSerialPHYConfig{
		RxQueueDepth: cfg.GetInt(KeyKissSerialRxQueueDepth),
		TxQueueDepth: cfg.GetInt(KeyKissSerialTxQueueDepth),
		ReadBufSize:  cfg.GetInt(KeyKissSerialReadBuf),
	}
}
