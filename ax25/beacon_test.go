// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestBeaconConfigFromConfig(t *testing.T) {
	cfg := NewConfig(nil)
	cfg.Set(KeyBeaconSource, "N7GET-1")
	cfg.Set(KeyBeaconDestination, "BEACON")
	cfg.Set(KeyBeaconVia, "RELAY-1")
	cfg.Set(KeyBeaconText, "hello")
	cfg.Set(KeyBeaconEvery, "2")

	b := BeaconConfigFromConfig(cfg)
	if b.Source != "N7GET-1" || b.Destination != "BEACON" || b.Via != "RELAY-1" || b.Text != "hello" {
		t.Fatalf("unexpected config mapping: %+v", b)
	}
	if b.Every != 2*time.Minute {
		t.Fatalf("Every: got %v, want %v", b.Every, 2*time.Minute)
	}
}

func TestBeacon_Trigger(t *testing.T) {
	var got *Frame
	b := NewBeacon(BeaconConfig{
		Source:      "N7GET-1",
		Destination: "BEACON",
		Text:        "hello",
	}, func(f *Frame) error {
		got = f
		return nil
	})

	if err := b.Trigger(); err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if got == nil {
		t.Fatal("no frame sent")
	}
	if got.Source.String() != "N7GET-1" {
		t.Errorf("Source: got %v, want N7GET-1", got.Source)
	}
	if got.Destination.String() != "BEACON" {
		t.Errorf("Destination: got %v, want BEACON", got.Destination)
	}
	if string(got.Payload) != "hello" {
		t.Errorf("Payload: got %q, want \"hello\"", got.Payload)
	}
}

func TestBeacon_TriggerDisabled(t *testing.T) {
	called := false
	b := NewBeacon(BeaconConfig{Source: ""}, func(f *Frame) error {
		called = true
		return nil
	})
	b.Trigger()
	if called {
		t.Error("sendFn should not be called when source is empty")
	}
}

func TestBeacon_Periodic(t *testing.T) {
	count := 0
	b := NewBeacon(BeaconConfig{
		Source:      "N7GET-1",
		Destination: "BEACON",
		Every:       50 * time.Millisecond,
	}, func(f *Frame) error {
		count++
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.Start(ctx)
	time.Sleep(180 * time.Millisecond)
	b.Stop()

	if count < 2 {
		t.Errorf("expected at least 2 beacons, got %d", count)
	}
}

func TestBeacon_UnescapeText(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{`hello`, "hello"},
		{`\r\n`, "\r\n"},
		{`\x41`, "A"},
		{`\\`, "\\"},
	}
	for _, tc := range cases {
		got := unescapeText(tc.input)
		if got != tc.want {
			t.Errorf("unescapeText(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestBeacon_Trigger_InvalidAddress(t *testing.T) {
	b := NewBeacon(BeaconConfig{
		Source:      "BAD!CALL",
		Destination: "BEACON",
	}, func(f *Frame) error { return nil })
	if err := b.Trigger(); err == nil {
		t.Fatal("expected source parse error")
	}

	b2 := NewBeacon(BeaconConfig{
		Source:      "N7GET-1",
		Destination: "BAD!CALL",
	}, func(f *Frame) error { return nil })
	if err := b2.Trigger(); err == nil {
		t.Fatal("expected destination parse error")
	}
}

func TestBeacon_Trigger_InvalidViaSkipped(t *testing.T) {
	var got *Frame
	b := NewBeacon(BeaconConfig{
		Source:      "N7GET-1",
		Destination: "BEACON",
		Via:         "RELAY-1, BAD!CALL, RELAY-2",
	}, func(f *Frame) error {
		got = f
		return nil
	})

	if err := b.Trigger(); err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if got == nil {
		t.Fatal("no frame captured")
	}
	if len(got.Digipeaters) != 2 {
		t.Fatalf("digipeater count: got %d, want 2", len(got.Digipeaters))
	}
}

func TestBeacon_Trigger_PayloadTrimmedToMaxInfoLen(t *testing.T) {
	var got *Frame
	b := NewBeacon(BeaconConfig{
		Source:      "N7GET-1",
		Destination: "BEACON",
		Text:        string(bytes.Repeat([]byte{'A'}, MaxInfoLen+25)),
	}, func(f *Frame) error {
		got = f
		return nil
	})

	if err := b.Trigger(); err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if got == nil {
		t.Fatal("no frame captured")
	}
	if len(got.Payload) != MaxInfoLen {
		t.Fatalf("payload len: got %d, want %d", len(got.Payload), MaxInfoLen)
	}
}

func TestBeacon_UpdateConfig(t *testing.T) {
	var got *Frame
	b := NewBeacon(BeaconConfig{
		Source:      "N7GET-1",
		Destination: "BEACON",
		Text:        "first",
	}, func(f *Frame) error {
		got = f
		return nil
	})

	b.UpdateConfig(BeaconConfig{
		Source:      "N7GET-2",
		Destination: "APRS",
		Text:        "second",
	})

	if err := b.Trigger(); err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if got == nil {
		t.Fatal("no frame captured")
	}
	if got.Source.String() != "N7GET-2" || got.Destination.String() != "APRS" || string(got.Payload) != "second" {
		t.Fatalf("unexpected updated frame: src=%s dst=%s payload=%q", got.Source.String(), got.Destination.String(), string(got.Payload))
	}
}
