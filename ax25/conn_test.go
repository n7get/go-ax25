// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"errors"
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
	aErrors, bErrors           []*ConnError
	aLinkResets, bLinkResets   int
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
		OnLinkReset:  func() { h.mu.Lock(); h.aLinkResets++; h.mu.Unlock() },
		OnError:      func(err *ConnError) { h.mu.Lock(); h.aErrors = append(h.aErrors, err); h.mu.Unlock() },
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
		OnLinkReset:  func() { h.mu.Lock(); h.bLinkResets++; h.mu.Unlock() },
		OnError:      func(err *ConnError) { h.mu.Lock(); h.bErrors = append(h.bErrors, err); h.mu.Unlock() },
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

func makeSABM(t *testing.T, src, dst string) *Frame {
	t.Helper()
	s, err := ParseAddress(src)
	if err != nil {
		t.Fatalf("ParseAddress src: %v", err)
	}
	d, err := ParseAddress(dst)
	if err != nil {
		t.Fatalf("ParseAddress dst: %v", err)
	}
	return &Frame{
		Destination: d,
		Source:      s,
		IsCommand:   true,
		Type:        FrameU,
		Control:     CtrlSABM | CtrlPFBit,
	}
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

func TestConn_NewConn_RequiresCallbacks(t *testing.T) {
	local, _ := ParseAddress("N7GET-1")
	_, err := NewConn(local, ConnCallbacks{OnData: func([]byte) {}}, nil)
	if err == nil {
		t.Fatal("expected error when OnTxFrame is missing")
	}

	_, err = NewConn(local, ConnCallbacks{OnTxFrame: func(*Frame) {}}, nil)
	if err == nil {
		t.Fatal("expected error when OnData is missing")
	}
}

func TestConn_ShutdownWhenDisconnected(t *testing.T) {
	local, _ := ParseAddress("N7GET-1")
	c, err := NewConn(local, ConnCallbacks{OnData: func([]byte) {}, OnTxFrame: func(*Frame) {}}, nil)
	if err != nil {
		t.Fatalf("NewConn: %v", err)
	}
	defer c.Close()

	err = c.Shutdown()
	if !errors.Is(err, ErrConnNotConnected) {
		t.Fatalf("Shutdown when disconnected: got %v, want %v", err, ErrConnNotConnected)
	}
}

func TestConn_ConnectAlreadyActive(t *testing.T) {
	h := newConnHarness(t)
	defer h.A.Close()
	defer h.B.Close()

	remoteB, _ := ParseAddress("W1AW-2")
	if err := h.A.Connect(remoteB); err != nil {
		t.Fatalf("Connect first: %v", err)
	}
	if err := h.A.Connect(remoteB); !errors.Is(err, ErrConnAlreadyActive) {
		t.Fatalf("Connect while active: got %v, want %v", err, ErrConnAlreadyActive)
	}
}

func TestConn_RefuseIncomingConnection(t *testing.T) {
	local, _ := ParseAddress("N7GET-1")
	refused := false
	var tx []*Frame
	var c *Conn

	conn, err := NewConn(local, ConnCallbacks{
		OnConnect: func(remote Address, local bool) { c.Refuse() },
		OnData:    func([]byte) {},
		OnTxFrame: func(f *Frame) { tx = append(tx, f) },
	}, nil)
	if err != nil {
		t.Fatalf("NewConn: %v", err)
	}
	c = conn
	defer c.Close()

	if err := c.OnFrame(makeSABM(t, "W1AW-2", "N7GET-1")); err != nil {
		t.Fatalf("OnFrame SABM: %v", err)
	}
	if c.State() != ConnStateDisconnected {
		t.Fatalf("state after refuse: got %v, want %v", c.State(), ConnStateDisconnected)
	}
	for _, f := range tx {
		if f.Control&0xEF == CtrlDM {
			refused = true
			break
		}
	}
	if !refused {
		t.Fatal("expected DM frame on refused connection")
	}
}

func TestConn_WindowFullBoundary(t *testing.T) {
	local, _ := ParseAddress("N7GET-1")
	var tx []*Frame
	c, err := NewConn(local, ConnCallbacks{
		OnData:    func([]byte) {},
		OnTxFrame: func(f *Frame) { tx = append(tx, f) },
	}, &ConnConfig{T1: time.Second, T2: time.Second, T3: time.Second, N2: 1, Window: 1})
	if err != nil {
		t.Fatalf("NewConn: %v", err)
	}
	defer c.Close()

	if err := c.OnFrame(makeSABM(t, "W1AW-2", "N7GET-1")); err != nil {
		t.Fatalf("OnFrame SABM: %v", err)
	}
	if !c.IsConnected() {
		t.Fatal("expected connected state after inbound SABM")
	}

	if err := c.SendData([]byte("one")); err != nil {
		t.Fatalf("SendData first: %v", err)
	}
	if err := c.SendData([]byte("two")); !errors.Is(err, ErrConnSendBufFull) {
		t.Fatalf("SendData second: got %v, want %v", err, ErrConnSendBufFull)
	}
	if len(tx) == 0 {
		t.Fatal("expected transmitted frames")
	}
}

func TestConn_InvalidNRRecoveryFromConnected(t *testing.T) {
	h := newConnHarness(t)
	defer h.A.Close()
	defer h.B.Close()

	remoteB, _ := ParseAddress("W1AW-2")
	if err := h.A.Connect(remoteB); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	h.waitConnects(t, 1, 1)

	bad := &Frame{
		Destination: mustParseAddr(t, "N7GET-1"),
		Source:      mustParseAddr(t, "W1AW-2"),
		IsCommand:   false,
		Type:        FrameS,
		Control:     BuildRRControl(1, false), // invalid when vs=va=0
	}
	if err := h.A.OnFrame(bad); err != nil {
		t.Fatalf("OnFrame bad RR: %v", err)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		nErr := len(h.aErrors)
		h.mu.Unlock()
		if nErr > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.mu.Lock()
	nErr := len(h.aErrors)
	h.mu.Unlock()
	if nErr == 0 {
		t.Fatal("expected OnError callback on invalid N(R)")
	}
	if h.A.State() != ConnStateDisconnected {
		t.Fatalf("state after invalid N(R): got %v, want %v", h.A.State(), ConnStateDisconnected)
	}
}

func TestConn_TimerRecoveryRetransmitsUnackedIFrame(t *testing.T) {
	local := mustParseAddr(t, "N7GET-1")
	remote := mustParseAddr(t, "W1AW-2")

	var tx []*Frame
	c, err := NewConn(local, ConnCallbacks{
		OnData: func([]byte) {},
		OnTxFrame: func(f *Frame) {
			tx = append(tx, cloneConnFrame(f))
		},
	}, &ConnConfig{T1: time.Second, T2: time.Second, T3: time.Second, N2: 3, Window: 4})
	if err != nil {
		t.Fatalf("NewConn: %v", err)
	}
	defer c.Close()

	if err := c.OnFrame(makeSABM(t, remote.String(), local.String())); err != nil {
		t.Fatalf("OnFrame SABM: %v", err)
	}
	if !c.IsConnected() {
		t.Fatal("expected connected state after inbound SABM")
	}
	tx = nil

	if err := c.SendData([]byte("hello")); err != nil {
		t.Fatalf("SendData: %v", err)
	}
	if len(tx) != 1 || tx[0].Type != FrameI {
		t.Fatalf("expected one outbound I frame, got %d frames", len(tx))
	}

	c.onT1Expired()
	if c.State() != ConnStateTimerRecovery {
		t.Fatalf("state after T1 expiry: got %v, want %v", c.State(), ConnStateTimerRecovery)
	}
	if len(tx) != 2 || tx[1].Type != FrameS || !HasPF(tx[1].Control) {
		t.Fatalf("expected poll RR after T1 expiry, got %#v", tx)
	}

	rrFinal := &Frame{
		Destination: local,
		Source:      remote,
		IsCommand:   false,
		Type:        FrameS,
		Control:     BuildRRControl(0, true),
	}
	if err := c.OnFrame(rrFinal); err != nil {
		t.Fatalf("OnFrame RR(F): %v", err)
	}

	if len(tx) < 3 {
		t.Fatalf("expected retransmitted I frame after RR(F), got %d frames", len(tx))
	}
	last := tx[len(tx)-1]
	if last.Type != FrameI {
		t.Fatalf("expected last frame to be retransmitted I frame, got type %v control=0x%02x", last.Type, last.Control)
	}
	if string(last.Payload) != "hello" {
		t.Fatalf("retransmitted payload = %q, want %q", last.Payload, "hello")
	}
	if !c.t1Active {
		t.Fatal("expected T1 to restart after retransmission")
	}
}

func TestConn_LinkResetOnSABMWhileConnected(t *testing.T) {
	h := newConnHarness(t)
	defer h.A.Close()
	defer h.B.Close()

	remoteB, _ := ParseAddress("W1AW-2")
	if err := h.A.Connect(remoteB); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	h.waitConnects(t, 1, 1)

	if err := h.A.OnFrame(makeSABM(t, "W1AW-2", "N7GET-1")); err != nil {
		t.Fatalf("OnFrame SABM while connected: %v", err)
	}

	h.mu.Lock()
	resets := h.aLinkResets
	h.mu.Unlock()
	if resets == 0 {
		t.Fatal("expected OnLinkReset callback")
	}
	if !h.A.IsConnected() {
		t.Fatal("connection should remain active after link reset SABM")
	}
}

func TestConn_ConcurrentStateAndBusyCalls(t *testing.T) {
	h := newConnHarness(t)
	defer h.A.Close()
	defer h.B.Close()

	remoteB, _ := ParseAddress("W1AW-2")
	if err := h.A.Connect(remoteB); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	h.waitConnects(t, 1, 1)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = h.A.State()
				_ = h.A.IsConnected()
				h.A.SetBusy((i+j)%2 == 0)
			}
		}(i)
	}
	wg.Wait()
}

func TestExtractReturnPath_ClearsHBit(t *testing.T) {
	relay, _ := ParseAddress("RELAY")
	relay.HasBeenRepeated = true

	f := &Frame{Digipeaters: []Address{relay}}
	path := extractReturnPath(f)

	if len(path) != 1 {
		t.Fatalf("want 1 digi, got %d", len(path))
	}
	if path[0].HasBeenRepeated {
		t.Error("extractReturnPath must clear HasBeenRepeated; got H-bit still set")
	}
}

func TestExtractReturnPath_ReversesMultiHop(t *testing.T) {
	r1, _ := ParseAddress("RELAY1")
	r1.HasBeenRepeated = true
	r2, _ := ParseAddress("RELAY2")
	r2.HasBeenRepeated = true

	f := &Frame{Digipeaters: []Address{r1, r2}}
	path := extractReturnPath(f)

	if len(path) != 2 {
		t.Fatalf("want 2 digis, got %d", len(path))
	}
	if path[0].Callsign != r2.Callsign {
		t.Errorf("want first return hop %s, got %s", r2.Callsign, path[0].Callsign)
	}
	if path[1].Callsign != r1.Callsign {
		t.Errorf("want second return hop %s, got %s", r1.Callsign, path[1].Callsign)
	}
	for i, d := range path {
		if d.HasBeenRepeated {
			t.Errorf("path[%d] HasBeenRepeated must be false", i)
		}
	}
}

// TestConn_ReplyViaDigiClearsHBit verifies that when a Conn receives an
// incoming SABM whose digipeater path is already marked repeated (H-bit set),
// its reply frames carry the return path with H-bit cleared so the repeater
// can relay the reply correctly.
func TestConn_ReplyViaDigiClearsHBit(t *testing.T) {
	locB, _ := ParseAddress("N7GET-2")
	relay, _ := ParseAddress("RELAY")
	relay.HasBeenRepeated = true // digipeater already forwarded the SABM

	var txFrames []*Frame
	var mu sync.Mutex

	cbs := ConnCallbacks{
		OnConnect:    func(Address, bool) {},
		OnDisconnect: func() {},
		OnLinkReset:  func() {},
		OnError:      func(*ConnError) {},
		OnData:       func([]byte) {},
		OnTxFrame: func(f *Frame) {
			mu.Lock()
			txFrames = append(txFrames, f)
			mu.Unlock()
		},
	}
	conn, err := NewConn(locB, cbs, nil)
	if err != nil {
		t.Fatalf("NewConn: %v", err)
	}

	// Deliver SABM from N7GET via RELAY* (H-bit set — already digipeated).
	src, _ := ParseAddress("N7GET")
	sabm := &Frame{
		Destination: locB,
		Source:      src,
		Digipeaters: []Address{relay},
		IsCommand:   true,
		Type:        FrameU,
		Control:     CtrlSABM | CtrlPFBit,
	}
	conn.OnFrame(sabm)

	mu.Lock()
	frames := append([]*Frame(nil), txFrames...)
	mu.Unlock()

	if len(frames) == 0 {
		t.Fatal("expected at least one reply frame (UA)")
	}
	for _, f := range frames {
		for i, d := range f.Digipeaters {
			if d.HasBeenRepeated {
				t.Errorf("reply frame %v digi[%d] %s has H-bit set; want cleared", f.Type, i, d.Callsign)
			}
		}
	}
}
