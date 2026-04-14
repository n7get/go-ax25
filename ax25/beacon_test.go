// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"testing"
	"time"
)

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

	b.Start(t.Context())
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
