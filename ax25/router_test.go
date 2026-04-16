// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"sync"
	"testing"
	"time"
)

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
	r := NewRouter()
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
	r := NewRouter()
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
	r := NewRouter()
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
	r := NewRouter()
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
			if !got.Digipeaters[0].HasBeenRepeated {
				t.Error("H-bit not set on relayed frame")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("timeout waiting for digipeated frame")
}

func TestRouter_SrcPortSkipped(t *testing.T) {
	r := NewRouter()
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
	r := NewRouter()
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
