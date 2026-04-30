// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"bytes"
	"errors"
	"log/slog"
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

func TestBuildSupervisoryControls(t *testing.T) {
	tests := []struct {
		name string
		ctrl byte
		mask byte
	}{
		{name: "RR", ctrl: BuildRRControl(6, true), mask: CtrlRRMask},
		{name: "RNR", ctrl: BuildRNRControl(2, false), mask: CtrlRNRMask},
		{name: "REJ", ctrl: BuildREJControl(7, true), mask: CtrlREJMask},
	}

	for _, tc := range tests {
		if got := tc.ctrl & 0x0F; got != tc.mask {
			t.Fatalf("%s mask: got 0x%02X, want 0x%02X", tc.name, got, tc.mask)
		}
	}

	if nr := ExtractNR(tests[0].ctrl); nr != 6 {
		t.Fatalf("RR N(R): got %d, want 6", nr)
	}
	if !HasPF(tests[0].ctrl) {
		t.Fatalf("RR PF bit: expected set")
	}
	if HasPF(tests[1].ctrl) {
		t.Fatalf("RNR PF bit: expected clear")
	}
}

func TestControlName_PFMaskedForUFrames(t *testing.T) {
	if got := ControlName(CtrlSABM | CtrlPFBit); got != "SABM" {
		t.Fatalf("ControlName(SABM|PF): got %q, want %q", got, "SABM")
	}
	if got := ControlName(CtrlUA | CtrlPFBit); got != "UA" {
		t.Fatalf("ControlName(UA|PF): got %q, want %q", got, "UA")
	}
}

func TestControlName_Supervisory(t *testing.T) {
	if got := ControlName(BuildRRControl(0, false)); got != "RR" {
		t.Fatalf("ControlName(RR): got %q, want RR", got)
	}
	if got := ControlName(BuildRNRControl(0, false)); got != "RNR" {
		t.Fatalf("ControlName(RNR): got %q, want RNR", got)
	}
	if got := ControlName(BuildREJControl(0, false)); got != "REJ" {
		t.Fatalf("ControlName(REJ): got %q, want REJ", got)
	}
}

func TestLogFrame_DoesNotPanic(t *testing.T) {
	f := &Frame{
		Destination: mustParseAddr(t, "DEST-0"),
		Source:      mustParseAddr(t, "SRC-0"),
		Type:        FrameUI,
		Control:     CtrlUI,
		PID:         PIDNone,
		Payload:     []byte("hello"),
	}

	// Should be safe to call regardless of logger level state.
	LogFrame(slog.LevelDebug, "test log frame", f)
}

func TestFrameName(t *testing.T) {
	f := &Frame{Control: CtrlUI}
	if got := f.Name(); got != "UI" {
		t.Fatalf("Frame.Name() UI: got %q, want UI", got)
	}

	f.Control = BuildIControl(1, 2, false)
	if got := f.Name(); got != "I" {
		t.Fatalf("Frame.Name() I: got %q, want I", got)
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

func TestFrameEncode_MaxPayloadBoundary(t *testing.T) {
	payload := bytes.Repeat([]byte{'A'}, MaxInfoLen)
	f := &Frame{
		Destination: mustParseAddr(t, "DEST-0"),
		Source:      mustParseAddr(t, "SRC-0"),
		Type:        FrameUI,
		Control:     CtrlUI,
		PID:         PIDNone,
		Payload:     payload,
	}

	raw, err := f.Encode()
	if err != nil {
		t.Fatalf("Encode MaxInfoLen: %v", err)
	}
	parsed, err := ParseFrame(raw)
	if err != nil {
		t.Fatalf("ParseFrame MaxInfoLen: %v", err)
	}
	if len(parsed.Payload) != MaxInfoLen {
		t.Fatalf("payload len: got %d, want %d", len(parsed.Payload), MaxInfoLen)
	}
}

func TestFrameEncode_TooManyDigipeaters(t *testing.T) {
	digis := make([]Address, MaxDigipeaters+1)
	for i := range digis {
		digis[i] = mustParseAddr(t, "RELAY-1")
	}

	f := &Frame{
		Destination: mustParseAddr(t, "DEST-0"),
		Source:      mustParseAddr(t, "SRC-0"),
		Digipeaters: digis,
		Type:        FrameUI,
		Control:     CtrlUI,
		PID:         PIDNone,
	}

	_, err := f.Encode()
	if err == nil {
		t.Fatal("expected ErrTooManyDigis")
	}
	if !errors.Is(err, ErrTooManyDigis) {
		t.Fatalf("got %v, want %v", err, ErrTooManyDigis)
	}
}

func TestFrameEncode_MaxDigipeatersBoundary(t *testing.T) {
	digis := make([]Address, MaxDigipeaters)
	for i := range digis {
		digis[i] = mustParseAddr(t, "RELAY-1")
	}

	f := &Frame{
		Destination: mustParseAddr(t, "DEST-0"),
		Source:      mustParseAddr(t, "SRC-0"),
		Digipeaters: digis,
		Type:        FrameUI,
		Control:     CtrlUI,
		PID:         PIDNone,
	}

	raw, err := f.Encode()
	if err != nil {
		t.Fatalf("Encode with MaxDigipeaters: %v", err)
	}
	parsed, err := ParseFrame(raw)
	if err != nil {
		t.Fatalf("ParseFrame with MaxDigipeaters: %v", err)
	}
	if got := len(parsed.Digipeaters); got != MaxDigipeaters {
		t.Fatalf("digipeater count: got %d, want %d", got, MaxDigipeaters)
	}
}

func TestParseFrame_TruncatedAfterSourceNotLast(t *testing.T) {
	dst := mustParseAddr(t, "DEST-0")
	src := mustParseAddr(t, "SRC-0")

	dstEnc := dst.Encode()
	srcEnc := src.Encode()
	srcEnc[6] &^= 0x01 // clear "last" to indicate digipeaters should follow

	raw := append(dstEnc[:], srcEnc[:]...)
	raw = append(raw, CtrlUI)

	_, err := ParseFrame(raw)
	if err == nil {
		t.Fatal("expected ErrFrameTooShort")
	}
	if !errors.Is(err, ErrFrameTooShort) {
		t.Fatalf("got %v, want %v", err, ErrFrameTooShort)
	}
}

func TestParseFrame_TooManyDigipeaters(t *testing.T) {
	dst := mustParseAddr(t, "DEST-0")
	src := mustParseAddr(t, "SRC-0")

	dstEnc := dst.Encode()
	srcEnc := src.Encode()
	srcEnc[6] &^= 0x01 // indicate digipeaters follow

	raw := append(dstEnc[:], srcEnc[:]...)
	for i := 0; i < MaxDigipeaters+1; i++ {
		d := mustParseAddr(t, "RELAY-1")
		d.IsLast = false
		dEnc := d.Encode()
		raw = append(raw, dEnc[:]...)
	}

	_, err := ParseFrame(raw)
	if err == nil {
		t.Fatal("expected ErrTooManyDigis")
	}
	if !errors.Is(err, ErrTooManyDigis) {
		t.Fatalf("got %v, want %v", err, ErrTooManyDigis)
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
