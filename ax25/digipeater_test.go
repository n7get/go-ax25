// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"sync"
	"testing"
	"time"
)

func TestDigipeater_Disabled(t *testing.T) {
	r := NewRouter()
	defer r.Close()

	called := false
	_, err := NewDigipeater(DigiConfig{Callsign: ""}, r, func(f *Frame) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("NewDigipeater: %v", err)
	}

	f := makeUIFrame("DEST-0", "SRC-0")
	relay, _ := ParseAddress("RELAY-1")
	f.Digipeaters = []Address{relay}
	sender := &Port{Mode: PortModeDefault}
	r.Send(f, sender)
	time.Sleep(100 * time.Millisecond)
	if called {
		t.Error("disabled digipeater should not relay frames")
	}
}

func TestDigipeater_RelaysMatchingFrame(t *testing.T) {
	r := NewRouter()
	defer r.Close()

	var mu sync.Mutex
	var relayed *Frame
	d, err := NewDigipeater(DigiConfig{Callsign: "RELAY-1"}, r, func(f *Frame) error {
		mu.Lock()
		relayed = f
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("NewDigipeater: %v", err)
	}
	defer d.Close()

	f := makeUIFrame("DEST-0", "SRC-0")
	relay, _ := ParseAddress("RELAY-1")
	f.Digipeaters = []Address{relay}
	sender := &Port{Mode: PortModeDefault}
	r.Send(f, sender)

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := relayed
		mu.Unlock()
		if got != nil {
			if !got.Digipeaters[0].HasBeenRepeated {
				t.Error("H-bit not set on relayed frame")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("timeout waiting for relayed frame")
}

func TestDigipeater_IgnoresNonMatchingFrame(t *testing.T) {
	r := NewRouter()
	defer r.Close()

	called := false
	d, err := NewDigipeater(DigiConfig{Callsign: "RELAY-2"}, r, func(f *Frame) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("NewDigipeater: %v", err)
	}
	defer d.Close()

	f := makeUIFrame("DEST-0", "SRC-0")
	relay1, _ := ParseAddress("RELAY-1")
	relay2, _ := ParseAddress("RELAY-2")
	f.Digipeaters = []Address{relay1, relay2} // RELAY-1 is next hop, not RELAY-2
	sender := &Port{Mode: PortModeDefault}
	r.Send(f, sender)
	time.Sleep(100 * time.Millisecond)
	if called {
		t.Error("digipeater should not relay when it is not the next hop")
	}
}

func TestDigipeater_InvalidCallsign(t *testing.T) {
	r := NewRouter()
	defer r.Close()

	_, err := NewDigipeater(DigiConfig{Callsign: "BAD!CALL"}, r, func(f *Frame) error { return nil })
	if err == nil {
		t.Error("expected error for invalid callsign")
	}
}

func TestDigipeater_Close(t *testing.T) {
	r := NewRouter()
	defer r.Close()

	called := false
	d, err := NewDigipeater(DigiConfig{Callsign: "RELAY-1"}, r, func(f *Frame) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("NewDigipeater: %v", err)
	}
	d.Close()

	f := makeUIFrame("DEST-0", "SRC-0")
	relay, _ := ParseAddress("RELAY-1")
	f.Digipeaters = []Address{relay}
	sender := &Port{Mode: PortModeDefault}
	r.Send(f, sender)
	time.Sleep(100 * time.Millisecond)
	if called {
		t.Error("closed digipeater should not relay frames")
	}
}
