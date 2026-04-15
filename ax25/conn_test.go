// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"sync"
	"testing"
	"time"
)

// connHarness wires two Conn instances back-to-back for testing.
type connHarness struct {
	A, B *Conn
	mu   sync.Mutex

	aConnects, bConnects       int
	aDisconnects, bDisconnects int
	aData, bData               [][]byte
	aTxFrames, bTxFrames       []*Frame
}

func newConnHarness(t *testing.T) *connHarness {
	t.Helper()
	h := &connHarness{}

	locA, _ := ParseAddress("N7GET-1")
	locB, _ := ParseAddress("W1AW-2")

	var connA, connB *Conn

	cbsA := ConnCallbacks{
		OnConnect:    func(r Address, local bool) { h.mu.Lock(); h.aConnects++; h.mu.Unlock() },
		OnDisconnect: func() { h.mu.Lock(); h.aDisconnects++; h.mu.Unlock() },
		OnData:       func(d []byte) { h.mu.Lock(); h.aData = append(h.aData, d); h.mu.Unlock() },
		OnTxFrame: func(f *Frame) {
			h.mu.Lock()
			h.aTxFrames = append(h.aTxFrames, f)
			h.mu.Unlock()
			if connB != nil {
				connB.OnFrame(f)
			}
		},
	}
	cbsB := ConnCallbacks{
		OnConnect:    func(r Address, local bool) { h.mu.Lock(); h.bConnects++; h.mu.Unlock() },
		OnDisconnect: func() { h.mu.Lock(); h.bDisconnects++; h.mu.Unlock() },
		OnData:       func(d []byte) { h.mu.Lock(); h.bData = append(h.bData, d); h.mu.Unlock() },
		OnTxFrame: func(f *Frame) {
			h.mu.Lock()
			h.bTxFrames = append(h.bTxFrames, f)
			h.mu.Unlock()
			if connA != nil {
				connA.OnFrame(f)
			}
		},
	}

	var err error
	connA, err = NewConn(locA, cbsA, nil)
	if err != nil {
		t.Fatalf("NewConn A: %v", err)
	}
	connB, err = NewConn(locB, cbsB, nil)
	if err != nil {
		t.Fatalf("NewConn B: %v", err)
	}
	h.A = connA
	h.B = connB
	return h
}

func (h *connHarness) waitConnects(t *testing.T, wantA, wantB int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		a, b := h.aConnects, h.bConnects
		h.mu.Unlock()
		if a >= wantA && b >= wantB {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.mu.Lock()
	a, b := h.aConnects, h.bConnects
	h.mu.Unlock()
	t.Errorf("timeout: A connects=%d (want %d), B connects=%d (want %d)", a, wantA, b, wantB)
}

func TestConn_ConnectDisconnect(t *testing.T) {
	h := newConnHarness(t)
	defer h.A.Close()
	defer h.B.Close()

	remoteB, _ := ParseAddress("W1AW-2")
	if err := h.A.Connect(remoteB); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	h.waitConnects(t, 1, 1)

	if !h.A.IsConnected() {
		t.Error("A should be connected")
	}
	if !h.B.IsConnected() {
		t.Error("B should be connected")
	}

	h.A.Shutdown()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		d := h.bDisconnects
		h.mu.Unlock()
		if d >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("timeout waiting for B disconnect")
}

func TestConn_SendData(t *testing.T) {
	h := newConnHarness(t)
	defer h.A.Close()
	defer h.B.Close()

	remoteB, _ := ParseAddress("W1AW-2")
	h.A.Connect(remoteB)
	h.waitConnects(t, 1, 1)

	msg := []byte("hello world")
	if err := h.A.SendData(msg); err != nil {
		t.Fatalf("SendData: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		got := len(h.bData)
		h.mu.Unlock()
		if got >= 1 {
			h.mu.Lock()
			data := h.bData[0]
			h.mu.Unlock()
			if string(data) != string(msg) {
				t.Errorf("data: got %q, want %q", data, msg)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("timeout waiting for data at B")
}

func TestConn_SendDataWhenDisconnected(t *testing.T) {
	local, _ := ParseAddress("N7GET-1")
	cbs := ConnCallbacks{
		OnData:    func([]byte) {},
		OnTxFrame: func(*Frame) {},
	}
	c, _ := NewConn(local, cbs, nil)
	defer c.Close()

	err := c.SendData([]byte("test"))
	if err == nil {
		t.Error("expected error when sending while disconnected")
	}
}
