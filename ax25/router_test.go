// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRouterModeFromConfig(t *testing.T) {
	cfg := NewConfig(nil)

	cfg.Set(KeyRouterMode, "switch")
	if m := RouterModeFromConfig(cfg); m == nil || *m != RouterModeSwitch {
		t.Fatalf("switch mode parse failed: got %v", m)
	}

	cfg.Set(KeyRouterMode, "bridge")
	if m := RouterModeFromConfig(cfg); m == nil || *m != RouterModeBridge {
		t.Fatalf("bridge mode parse failed: got %v", m)
	}

	cfg.Set(KeyRouterMode, "hub")
	if m := RouterModeFromConfig(cfg); m == nil || *m != RouterModeHub {
		t.Fatalf("hub mode parse failed: got %v", m)
	}

	cfg.Set(KeyRouterMode, "unknown")
	if m := RouterModeFromConfig(cfg); m == nil || *m != RouterModeSwitch {
		t.Fatalf("unknown mode fallback failed: got %v", m)
	}
}

func makeUIFrame(dst, src string) *Frame {
	d, _ := ParseAddress(dst)
	s, _ := ParseAddress(src)
	return &Frame{
		Destination: d,
		Source:      s,
		IsCommand:   true,
		Type:        FrameUI,
		Control:     CtrlUI,
		PID:         PIDNone,
	}
}

func waitForCount(t *testing.T, count *int, mu *sync.Mutex, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := *count
		mu.Unlock()
		if got >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	got := *count
	mu.Unlock()
	if got < want {
		t.Errorf("timeout: got %d frames, want %d", got, want)
	}
}

func TestRouter_StaticRouting(t *testing.T) {
	r := NewRouter(nil)
	defer r.Close()

	var mu sync.Mutex
	count := 0
	p := &Port{
		Mode:        PortModeStatic,
		Destination: mustParseAddr(t, "DEST-0"),
		OnRxFrame:   func(f *Frame) { mu.Lock(); count++; mu.Unlock() },
	}
	r.RegisterPort(p)

	sender := &Port{Mode: PortModeDefault}
	r.Send(makeUIFrame("DEST-0", "SRC-0"), sender)
	waitForCount(t, &count, &mu, 1, 500*time.Millisecond)
}

func TestRouter_PromiscuousReceivesAll(t *testing.T) {
	r := NewRouter(nil)
	defer r.Close()

	var mu sync.Mutex
	count := 0
	p := &Port{
		Mode:      PortModePromiscuous,
		OnRxFrame: func(f *Frame) { mu.Lock(); count++; mu.Unlock() },
	}
	r.RegisterPort(p)

	sender := &Port{Mode: PortModeDefault}
	for i := 0; i < 5; i++ {
		r.Send(makeUIFrame("DEST-0", "SRC-0"), sender)
	}
	waitForCount(t, &count, &mu, 5, 500*time.Millisecond)
}

func TestRouter_DefaultFallback(t *testing.T) {
	r := NewRouter(nil)
	defer r.Close()

	var mu sync.Mutex
	count := 0
	p := &Port{
		Mode:      PortModeDefault,
		OnRxFrame: func(f *Frame) { mu.Lock(); count++; mu.Unlock() },
	}
	r.RegisterPort(p)

	// No static port for DEST-0, so default should receive it.
	sender := &Port{Mode: PortModePromiscuous}
	r.Send(makeUIFrame("DEST-0", "SRC-0"), sender)
	waitForCount(t, &count, &mu, 1, 500*time.Millisecond)
}

func TestRouter_DigipeaterHBit(t *testing.T) {
	r := NewRouter(nil)
	defer r.Close()

	var mu sync.Mutex
	var lastFrame *Frame
	p := &Port{
		Mode:        PortModeDigipeater,
		Destination: mustParseAddr(t, "RELAY-1"),
		OnRxFrame: func(f *Frame) {
			mu.Lock()
			lastFrame = f
			mu.Unlock()
		},
	}
	r.RegisterPort(p)

	f := makeUIFrame("DEST-0", "SRC-0")
	relay, _ := ParseAddress("RELAY-1")
	f.Digipeaters = []Address{relay}

	sender := &Port{Mode: PortModeDefault}
	r.Send(f, sender)

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := lastFrame
		mu.Unlock()
		if got != nil {
			// Router delivers the raw clone to digipeater ports without H-bit mutation.
			// The Digipeater component (ax25.Digipeater) is responsible for H-bit advancement.
			if got.Digipeaters[0].HasBeenRepeated {
				t.Error("router must not set H-bit on frames delivered to digipeater ports")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("timeout waiting for digipeated frame")
}

func TestRouter_SrcPortSkipped(t *testing.T) {
	r := NewRouter(nil)
	defer r.Close()

	var mu sync.Mutex
	count := 0
	src := &Port{
		Mode:      PortModePromiscuous,
		OnRxFrame: func(f *Frame) { mu.Lock(); count++; mu.Unlock() },
	}
	r.RegisterPort(src)

	// Send from src — src should not receive its own frame.
	r.Send(makeUIFrame("DEST-0", "SRC-0"), src)
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	got := count
	mu.Unlock()
	if got != 0 {
		t.Errorf("src port received its own frame (%d times)", got)
	}
}

func TestRouter_UnregisterPort(t *testing.T) {
	r := NewRouter(nil)
	defer r.Close()

	var mu sync.Mutex
	count := 0
	p := &Port{
		Mode:      PortModePromiscuous,
		OnRxFrame: func(f *Frame) { mu.Lock(); count++; mu.Unlock() },
	}
	r.RegisterPort(p)
	r.UnregisterPort(p)

	sender := &Port{Mode: PortModeDefault}
	r.Send(makeUIFrame("DEST-0", "SRC-0"), sender)
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	got := count
	mu.Unlock()
	if got != 0 {
		t.Errorf("unregistered port received %d frames", got)
	}
}

func TestRouter_RegisterPort_DuplicateAndClosed(t *testing.T) {
	r := NewRouter(nil)
	p := &Port{Mode: PortModePromiscuous}
	if err := r.RegisterPort(p); err != nil {
		t.Fatalf("RegisterPort first: %v", err)
	}
	if err := r.RegisterPort(p); !errors.Is(err, ErrPortAlreadyRegistered) {
		t.Fatalf("RegisterPort duplicate: got %v, want %v", err, ErrPortAlreadyRegistered)
	}
	r.Close()
	if err := r.RegisterPort(&Port{}); !errors.Is(err, ErrRouterClosed) {
		t.Fatalf("RegisterPort after close: got %v, want %v", err, ErrRouterClosed)
	}
}

func TestRouter_UnregisterPort_NotFound(t *testing.T) {
	r := NewRouter(nil)
	defer r.Close()

	if err := r.UnregisterPort(&Port{}); !errors.Is(err, ErrPortNotFound) {
		t.Fatalf("UnregisterPort not found: got %v, want %v", err, ErrPortNotFound)
	}
}

func TestRouter_Send_ErrorPaths(t *testing.T) {
	r := NewRouter(nil)
	sender := &Port{Mode: PortModeDefault}

	if err := r.Send(nil, sender); !errors.Is(err, ErrNilFrame) {
		t.Fatalf("Send nil frame: got %v, want %v", err, ErrNilFrame)
	}
	if err := r.Send(makeUIFrame("DEST-0", "SRC-0"), nil); !errors.Is(err, ErrNilPort) {
		t.Fatalf("Send nil port: got %v, want %v", err, ErrNilPort)
	}

	r.Close()
	if err := r.Send(makeUIFrame("DEST-0", "SRC-0"), sender); !errors.Is(err, ErrRouterClosed) {
		t.Fatalf("Send after close: got %v, want %v", err, ErrRouterClosed)
	}
}

func TestRouter_DynamicPort_BindsFirstSource(t *testing.T) {
	r := NewRouter(nil)
	defer r.Close()

	src := &Port{Mode: PortModeDynamic}
	f1 := makeUIFrame("DEST-0", "SRC-1")
	if err := r.Send(f1, src); err != nil {
		t.Fatalf("Send first from dynamic: %v", err)
	}
	if got := src.Destination.String(); got != "SRC-1" {
		t.Fatalf("dynamic bind: got %q, want %q", got, "SRC-1")
	}

	f2 := makeUIFrame("DEST-0", "SRC-2")
	if err := r.Send(f2, src); err != nil {
		t.Fatalf("Send second from dynamic: %v", err)
	}
	if got := src.Destination.String(); got != "SRC-1" {
		t.Fatalf("dynamic rebind occurred: got %q, want %q", got, "SRC-1")
	}
}

// ---------------------------------------------------------------------------
// Bridge mode tests
// ---------------------------------------------------------------------------

func TestBridge_ClientToDefault(t *testing.T) {
	m := RouterModeBridge
	r := NewRouter(&m)
	defer r.Close()

	var mu sync.Mutex
	defaultGot, clientGot := 0, 0

	defaultPort := &Port{
		Mode:      PortModeDefault,
		OnRxFrame: func(f *Frame) { mu.Lock(); defaultGot++; mu.Unlock() },
	}
	clientPort := &Port{
		Mode:      PortModeStatic,
		OnRxFrame: func(f *Frame) { mu.Lock(); clientGot++; mu.Unlock() },
	}
	r.RegisterPort(defaultPort)
	r.RegisterPort(clientPort)

	// Send from client port; should arrive at default port only.
	r.Send(makeUIFrame("DEST-0", "SRC-0"), clientPort)
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	d, c := defaultGot, clientGot
	mu.Unlock()
	if d != 1 {
		t.Errorf("default port: want 1 frame, got %d", d)
	}
	if c != 0 {
		t.Errorf("client port: want 0 frames, got %d", c)
	}
}

func TestBridge_DefaultToAllClients(t *testing.T) {
	m := RouterModeBridge
	r := NewRouter(&m)
	defer r.Close()

	var mu sync.Mutex
	counts := [3]int{}

	defaultPort := &Port{Mode: PortModeDefault}
	clients := [3]*Port{}
	for i := range clients {
		i := i
		clients[i] = &Port{
			Mode:      PortModeStatic,
			OnRxFrame: func(f *Frame) { mu.Lock(); counts[i]++; mu.Unlock() },
		}
		r.RegisterPort(clients[i])
	}
	r.RegisterPort(defaultPort)

	// Send from default port; should arrive at all client ports.
	r.Send(makeUIFrame("DEST-0", "SRC-0"), defaultPort)
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	got := counts
	mu.Unlock()
	for i, c := range got {
		if c != 1 {
			t.Errorf("client[%d]: want 1 frame, got %d", i, c)
		}
	}
}

func TestBridge_NoClientToClient(t *testing.T) {
	m := RouterModeBridge
	r := NewRouter(&m)
	defer r.Close()

	var mu sync.Mutex
	got := 0

	clientA := &Port{Mode: PortModeStatic}
	clientB := &Port{
		Mode:      PortModeStatic,
		OnRxFrame: func(f *Frame) { mu.Lock(); got++; mu.Unlock() },
	}
	r.RegisterPort(clientA)
	r.RegisterPort(clientB)

	// Send from one client; the other client must not receive it.
	r.Send(makeUIFrame("DEST-0", "SRC-0"), clientA)
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	n := got
	mu.Unlock()
	if n != 0 {
		t.Errorf("client-to-client forwarding occurred (%d frames)", n)
	}
}

// ---------------------------------------------------------------------------
// Hub mode tests
// ---------------------------------------------------------------------------

func TestHub_BroadcastsToAll(t *testing.T) {
	m := RouterModeHub
	r := NewRouter(&m)
	defer r.Close()

	var mu sync.Mutex
	counts := [3]int{}

	ports := [3]*Port{}
	for i := range ports {
		i := i
		ports[i] = &Port{
			Mode:      PortModeStatic,
			OnRxFrame: func(f *Frame) { mu.Lock(); counts[i]++; mu.Unlock() },
		}
		r.RegisterPort(ports[i])
	}

	// Send from ports[0]; ports[1] and ports[2] should each get 1 frame.
	r.Send(makeUIFrame("DEST-0", "SRC-0"), ports[0])
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	got := counts
	mu.Unlock()
	if got[0] != 0 {
		t.Errorf("src port received its own frame")
	}
	for i := 1; i < 3; i++ {
		if got[i] != 1 {
			t.Errorf("ports[%d]: want 1 frame, got %d", i, got[i])
		}
	}
}

func TestHub_SrcExcluded(t *testing.T) {
	m := RouterModeHub
	r := NewRouter(&m)
	defer r.Close()

	var mu sync.Mutex
	got := 0

	src := &Port{
		Mode:      PortModeDefault,
		OnRxFrame: func(f *Frame) { mu.Lock(); got++; mu.Unlock() },
	}
	r.RegisterPort(src)

	r.Send(makeUIFrame("DEST-0", "SRC-0"), src)
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	n := got
	mu.Unlock()
	if n != 0 {
		t.Errorf("src port received its own frame (%d times)", n)
	}
}

// ---------------------------------------------------------------------------
// Switch mode — digi-path interaction with static matching
// ---------------------------------------------------------------------------

// TestSwitch_StaticSuppressedByPendingDigi verifies that a static port whose
// destination matches the frame's final destination does NOT receive the frame
// when an unrepeated digipeater hop remains in the path. The digipeater port
// should receive it instead.
func TestSwitch_StaticSuppressedByPendingDigi(t *testing.T) {
	r := NewRouter(nil) // Switch mode
	defer r.Close()

	var mu sync.Mutex
	staticGot, digiGot := 0, 0

	staticPort := &Port{
		Mode:        PortModeStatic,
		Destination: mustParseAddr(t, "DEST-0"),
		OnRxFrame:   func(f *Frame) { mu.Lock(); staticGot++; mu.Unlock() },
	}
	digiPort := &Port{
		Mode:        PortModeDigipeater,
		Destination: mustParseAddr(t, "RELAY-1"),
		OnRxFrame:   func(f *Frame) { mu.Lock(); digiGot++; mu.Unlock() },
	}
	r.RegisterPort(staticPort)
	r.RegisterPort(digiPort)

	f := makeUIFrame("DEST-0", "SRC-0")
	relay, _ := ParseAddress("RELAY-1")
	f.Digipeaters = []Address{relay} // unrepeated hop pending

	sender := &Port{Mode: PortModeDefault}
	r.Send(f, sender)
	waitForCount(t, &digiGot, &mu, 1, 500*time.Millisecond)

	mu.Lock()
	s, d := staticGot, digiGot
	mu.Unlock()
	if s != 0 {
		t.Errorf("static port must not receive frame while digi hop is pending; got %d", s)
	}
	if d != 1 {
		t.Errorf("digi port: want 1 frame, got %d", d)
	}
}

// TestSwitch_StaticMatchesAfterAllDigisRepeated verifies that once every hop
// in the digipeater path has the H-bit set the static port receives the frame
// normally and the digipeater port does not.
func TestSwitch_StaticMatchesAfterAllDigisRepeated(t *testing.T) {
	r := NewRouter(nil) // Switch mode
	defer r.Close()

	var mu sync.Mutex
	staticGot, digiGot := 0, 0

	staticPort := &Port{
		Mode:        PortModeStatic,
		Destination: mustParseAddr(t, "DEST-0"),
		OnRxFrame:   func(f *Frame) { mu.Lock(); staticGot++; mu.Unlock() },
	}
	digiPort := &Port{
		Mode:        PortModeDigipeater,
		Destination: mustParseAddr(t, "RELAY-1"),
		OnRxFrame:   func(f *Frame) { mu.Lock(); digiGot++; mu.Unlock() },
	}
	r.RegisterPort(staticPort)
	r.RegisterPort(digiPort)

	f := makeUIFrame("DEST-0", "SRC-0")
	relay, _ := ParseAddress("RELAY-1")
	relay.HasBeenRepeated = true // hop already done
	f.Digipeaters = []Address{relay}

	sender := &Port{Mode: PortModeDefault}
	r.Send(f, sender)
	waitForCount(t, &staticGot, &mu, 1, 500*time.Millisecond)

	mu.Lock()
	s, d := staticGot, digiGot
	mu.Unlock()
	if s != 1 {
		t.Errorf("static port: want 1 frame after all hops repeated, got %d", s)
	}
	if d != 0 {
		t.Errorf("digi port must not receive frame when its hop is already marked; got %d", d)
	}
}

// TestSwitch_DroppedWhenNoMatchAndNoDefault verifies that a frame addressed to
// a destination with no matching static port and no default port is silently
// dropped — no panic, no delivery.
func TestSwitch_DroppedWhenNoMatchAndNoDefault(t *testing.T) {
	r := NewRouter(nil) // Switch mode
	defer r.Close()

	var mu sync.Mutex
	got := 0
	p := &Port{
		Mode:        PortModeStatic,
		Destination: mustParseAddr(t, "OTHER"),
		OnRxFrame:   func(f *Frame) { mu.Lock(); got++; mu.Unlock() },
	}
	r.RegisterPort(p)

	sender := &Port{Mode: PortModeStatic, Destination: mustParseAddr(t, "SRC-0")}
	r.Send(makeUIFrame("DEST-0", "SRC-0"), sender)
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	n := got
	mu.Unlock()
	if n != 0 {
		t.Errorf("expected frame to be dropped silently, got %d deliveries", n)
	}
}

// TestSwitch_AllDigiPortsReceiveWhenHopPending verifies that when a frame has
// two digipeater hops and the first is already marked, ALL registered digipeater
// ports receive the raw frame (router-level delivery is unfiltered; next-hop
// gating is the responsibility of the ax25.Digipeater handler).
func TestSwitch_AllDigiPortsReceiveWhenHopPending(t *testing.T) {
	r := NewRouter(nil) // Switch mode
	defer r.Close()

	var mu sync.Mutex
	firstGot, secondGot := 0, 0

	firstDigiPort := &Port{
		Mode:        PortModeDigipeater,
		Destination: mustParseAddr(t, "RELAY-1"),
		OnRxFrame:   func(f *Frame) { mu.Lock(); firstGot++; mu.Unlock() },
	}
	secondDigiPort := &Port{
		Mode:        PortModeDigipeater,
		Destination: mustParseAddr(t, "RELAY-2"),
		OnRxFrame:   func(f *Frame) { mu.Lock(); secondGot++; mu.Unlock() },
	}
	r.RegisterPort(firstDigiPort)
	r.RegisterPort(secondDigiPort)

	f := makeUIFrame("DEST-0", "SRC-0")
	hop1, _ := ParseAddress("RELAY-1")
	hop1.HasBeenRepeated = true // already done
	hop2, _ := ParseAddress("RELAY-2")
	f.Digipeaters = []Address{hop1, hop2}

	sender := &Port{Mode: PortModeDefault}
	r.Send(f, sender)
	// Wait for both ports to receive.
	waitForCount(t, &secondGot, &mu, 1, 500*time.Millisecond)
	waitForCount(t, &firstGot, &mu, 1, 500*time.Millisecond)

	mu.Lock()
	first, second := firstGot, secondGot
	mu.Unlock()
	if first != 1 {
		t.Errorf("first digi port: want 1 frame (router delivers to all digi ports), got %d", first)
	}
	if second != 1 {
		t.Errorf("second digi port (next hop): want 1 frame, got %d", second)
	}
}

// ---------------------------------------------------------------------------
// Bridge mode — no default port
// ---------------------------------------------------------------------------

// TestBridge_NoDefaultPort verifies that when a client port sends a frame in
// Bridge mode and no default port is registered, no panic occurs and no port
// receives the frame.
func TestBridge_NoDefaultPort(t *testing.T) {
	m := RouterModeBridge
	r := NewRouter(&m)
	defer r.Close()

	var mu sync.Mutex
	got := 0
	clientPort := &Port{
		Mode:      PortModeStatic,
		OnRxFrame: func(f *Frame) { mu.Lock(); got++; mu.Unlock() },
	}
	r.RegisterPort(clientPort)

	otherClient := &Port{
		Mode:      PortModeStatic,
		OnRxFrame: func(f *Frame) { mu.Lock(); got++; mu.Unlock() },
	}
	r.RegisterPort(otherClient)

	r.Send(makeUIFrame("DEST-0", "SRC-0"), clientPort)
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	n := got
	mu.Unlock()
	if n != 0 {
		t.Errorf("expected no delivery with no default port; got %d", n)
	}
}

// ---------------------------------------------------------------------------
// Hub mode — single port
// ---------------------------------------------------------------------------

// TestHub_SinglePort verifies that when only one port is registered and it is
// the sender, no panic occurs and the sender does not receive its own frame.
func TestHub_SinglePort(t *testing.T) {
	m := RouterModeHub
	r := NewRouter(&m)
	defer r.Close()

	var mu sync.Mutex
	got := 0
	only := &Port{
		Mode:      PortModeStatic,
		OnRxFrame: func(f *Frame) { mu.Lock(); got++; mu.Unlock() },
	}
	r.RegisterPort(only)

	r.Send(makeUIFrame("DEST-0", "SRC-0"), only)
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	n := got
	mu.Unlock()
	if n != 0 {
		t.Errorf("single port must not receive its own frame; got %d", n)
	}
}
