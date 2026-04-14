// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"bytes"
	"testing"
)

func mustParseAddr(t *testing.T, s string) Address {
	t.Helper()
	a, err := ParseAddress(s)
	if err != nil {
		t.Fatalf("ParseAddress(%q): %v", s, err)
	}
	return a
}

func TestIdentifyFrameType(t *testing.T) {
	cases := []struct {
		ctrl byte
		want FrameType
	}{
		{CtrlUI, FrameUI},
		{0x00, FrameI},
		{0x10, FrameI},
		{CtrlRRMask, FrameS},
		{CtrlRNRMask, FrameS},
		{CtrlREJMask, FrameS},
		{CtrlSABM, FrameU},
		{CtrlDISC, FrameU},
		{CtrlDM, FrameU},
		{CtrlUA, FrameU},
		{CtrlFRMR, FrameU},
	}
	for _, tc := range cases {
		got := IdentifyFrameType(tc.ctrl)
		if got != tc.want {
			t.Errorf("IdentifyFrameType(0x%02X) = %v, want %v", tc.ctrl, got, tc.want)
		}
	}
}

func TestBuildIControl(t *testing.T) {
	ctrl := BuildIControl(3, 5, true)
	if ExtractNS(ctrl) != 3 {
		t.Errorf("NS: got %d, want 3", ExtractNS(ctrl))
	}
	if ExtractNR(ctrl) != 5 {
		t.Errorf("NR: got %d, want 5", ExtractNR(ctrl))
	}
	if !HasPF(ctrl) {
		t.Error("PF bit not set")
	}
}

func TestParseFrame_UI(t *testing.T) {
	dst := mustParseAddr(t, "DEST-0")
	src := mustParseAddr(t, "SRC-0")

	f := &Frame{
		Destination: dst,
		Source:      src,
		IsCommand:   true,
		Type:        FrameUI,
		Control:     CtrlUI,
		PID:         PIDNone,
		Payload:     []byte("hello"),
	}
	raw, err := f.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	parsed, err := ParseFrame(raw)
	if err != nil {
		t.Fatalf("ParseFrame: %v", err)
	}
	if !parsed.Destination.Equal(dst) {
		t.Errorf("Destination: got %v, want %v", parsed.Destination, dst)
	}
	if !parsed.Source.Equal(src) {
		t.Errorf("Source: got %v, want %v", parsed.Source, src)
	}
	if !bytes.Equal(parsed.Payload, f.Payload) {
		t.Errorf("Payload: got %q, want %q", parsed.Payload, f.Payload)
	}
	if parsed.Type != FrameUI {
		t.Errorf("Type: got %v, want UI", parsed.Type)
	}
}

func TestParseFrame_WithDigipeaters(t *testing.T) {
	dst := mustParseAddr(t, "DEST-0")
	src := mustParseAddr(t, "SRC-0")
	digi := mustParseAddr(t, "RELAY-1")

	f := &Frame{
		Destination: dst,
		Source:      src,
		Digipeaters: []Address{digi},
		IsCommand:   true,
		Type:        FrameUI,
		Control:     CtrlUI,
		PID:         PIDNone,
		Payload:     []byte("test"),
	}
	raw, err := f.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	parsed, err := ParseFrame(raw)
	if err != nil {
		t.Fatalf("ParseFrame: %v", err)
	}
	if len(parsed.Digipeaters) != 1 {
		t.Fatalf("Digipeaters: got %d, want 1", len(parsed.Digipeaters))
	}
	if !parsed.Digipeaters[0].Equal(digi) {
		t.Errorf("Digipeater[0]: got %v, want %v", parsed.Digipeaters[0], digi)
	}
}

func TestParseFrame_TooShort(t *testing.T) {
	_, err := ParseFrame([]byte{0x01, 0x02})
	if err == nil {
		t.Error("expected error for short frame")
	}
}

func TestFrameEncode_PayloadTooLong(t *testing.T) {
	f := &Frame{
		Destination: mustParseAddr(t, "DEST-0"),
		Source:      mustParseAddr(t, "SRC-0"),
		Type:        FrameUI,
		Control:     CtrlUI,
		PID:         PIDNone,
		Payload:     make([]byte, MaxInfoLen+1),
	}
	_, err := f.Encode()
	if err == nil {
		t.Error("expected error for payload too long")
	}
}

func FuzzParseFrame(f *testing.F) {
	f.Add([]byte{0x82, 0x84, 0x86, 0x88, 0x8A, 0x40, 0xE0,
		0x9C, 0x6E, 0x8A, 0x8E, 0x40, 0x61,
		0x03, 0xF0, 0x48, 0x65, 0x6C, 0x6C, 0x6F})
	f.Fuzz(func(t *testing.T, data []byte) {
		f, err := ParseFrame(data)
		if err == nil && f != nil {
			// Encode should not panic.
			f.Encode()
		}
	})
}
