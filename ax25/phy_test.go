// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"
)

// bidirectionalPipe creates two io.ReadWriters connected back-to-back.
// func newBidirectionalPipe() (io.ReadWriter, io.ReadWriter) {
// 	arToB, bToA := io.Pipe()
// 	bToA2, aToB := io.Pipe()
// 	_ = bToA
// 	_ = bToA2
// 	// A reads from arToB, writes to aToB
// 	// B reads from bToA2... let's use a simpler approach with sync.Pipe pairs
// 	_ = arToB
// 	_ = aToB
// 	// Use channel-based loopback instead
// 	return newChanRW(), newChanRW()
// }

type chanRW struct {
	mu     sync.Mutex
	buf    []byte
	notify chan struct{}
	closed bool
}

func newChanRW() *chanRW {
	return &chanRW{notify: make(chan struct{}, 1)}
}

func (c *chanRW) Write(p []byte) (int, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	c.buf = append(c.buf, p...)
	c.mu.Unlock()
	select {
	case c.notify <- struct{}{}:
	default:
	}
	return len(p), nil
}

func (c *chanRW) Read(p []byte) (int, error) {
	for {
		c.mu.Lock()
		if len(c.buf) > 0 {
			n := copy(p, c.buf)
			c.buf = c.buf[n:]
			c.mu.Unlock()
			return n, nil
		}
		if c.closed {
			c.mu.Unlock()
			return 0, io.EOF
		}
		c.mu.Unlock()
		<-c.notify
	}
}

func (c *chanRW) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	select {
	case c.notify <- struct{}{}:
	default:
	}
}

// loopback connects two chanRW so writes to A appear as reads on B and vice versa.
// type loopbackPair struct {
// 	aWrite, bWrite *chanRW
// }

type loopbackRW struct {
	reader *chanRW
	writer *chanRW
}

func (rw loopbackRW) Read(p []byte) (int, error) {
	return rw.reader.Read(p)
}

func (rw loopbackRW) Write(p []byte) (int, error) {
	return rw.writer.Write(p)
}

func (rw loopbackRW) Close() error {
	rw.reader.Close()
	rw.writer.Close()
	return nil
}

func newLoopbackPair() (io.ReadWriteCloser, io.ReadWriteCloser) {
	aWrite := newChanRW()
	bWrite := newChanRW()
	a := loopbackRW{reader: bWrite, writer: aWrite}
	b := loopbackRW{reader: aWrite, writer: bWrite}
	return a, b
}

func TestKISSSerialPHY_BasicRoundTrip(t *testing.T) {
	rwA, rwB := newLoopbackPair()

	phyA := NewKISSSerialPHY(rwA, KISSSerialPHYConfig{Port: 0})
	phyB := NewKISSSerialPHY(rwB, KISSSerialPHYConfig{Port: 0})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	phyA.Start(ctx)
	phyB.Start(ctx)

	tx := makeUIFrame("DEST-0", "SRC-0")
	tx.Payload = []byte("hello PHY")

	if err := phyA.SendFrame(tx); err != nil {
		t.Fatalf("SendFrame: %v", err)
	}

	select {
	case rx := <-phyB.RxFrames():
		if !rx.Destination.Equal(tx.Destination) {
			t.Errorf("Destination: got %v, want %v", rx.Destination, tx.Destination)
		}
		if !bytes.Equal(rx.Payload, tx.Payload) {
			t.Errorf("Payload: got %q, want %q", rx.Payload, tx.Payload)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for frame")
	}

	cancel()
	phyA.Stop()
	phyB.Stop()
}

func TestKISSSerialPHY_MultipleFrames(t *testing.T) {
	rwA, rwB := newLoopbackPair()
	phyA := NewKISSSerialPHY(rwA, KISSSerialPHYConfig{Port: 0})
	phyB := NewKISSSerialPHY(rwB, KISSSerialPHYConfig{Port: 0})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	phyA.Start(ctx)
	phyB.Start(ctx)

	const n = 10
	for i := 0; i < n; i++ {
		f := makeUIFrame("DEST-0", "SRC-0")
		f.Payload = []byte{byte(i)}
		if err := phyA.SendFrame(f); err != nil {
			t.Fatalf("SendFrame %d: %v", i, err)
		}
	}

	received := 0
	deadline := time.After(1 * time.Second)
	for received < n {
		select {
		case <-phyB.RxFrames():
			received++
		case <-deadline:
			t.Fatalf("timeout: received %d/%d frames", received, n)
		}
	}

	cancel()
	phyA.Stop()
	phyB.Stop()
}

func TestKISSSerialPHY_TxQueueFull(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()

	rw := struct {
		io.Reader
		io.Writer
		io.Closer
	}{Reader: pr, Writer: pw, Closer: closerFunc(func() error {
		_ = pr.Close()
		return pw.Close()
	})}

	phy := NewKISSSerialPHY(rw, KISSSerialPHYConfig{TxQueueDepth: 2})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	phy.Start(ctx)

	f := makeUIFrame("DEST-0", "SRC-0")
	var lastErr error
	for i := 0; i < 20; i++ {
		lastErr = phy.SendFrame(f)
		if lastErr == ErrPHYTxFull {
			break
		}
	}
	if lastErr != ErrPHYTxFull {
		t.Errorf("expected ErrPHYTxFull, got %v", lastErr)
	}
	cancel()
	phy.Stop()
}

type closerFunc func() error

func (fn closerFunc) Close() error {
	return fn()
}
