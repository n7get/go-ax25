// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"bytes"
	"testing"
)

func TestKISSEncode_Structure(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03}
	enc := KISSEncode(0, 0, data)
	if enc[0] != KISSFEnd {
		t.Errorf("first byte: got 0x%02X, want FEND", enc[0])
	}
	if enc[len(enc)-1] != KISSFEnd {
		t.Errorf("last byte: got 0x%02X, want FEND", enc[len(enc)-1])
	}
}

func TestKISSEncode_Escaping(t *testing.T) {
	data := []byte{KISSFEnd, KISSFEsc, 0x42}
	enc := KISSEncode(0, 0, data)
	// Should not contain raw FEND or FESC in the data portion.
	for i := 1; i < len(enc)-1; i++ {
		if enc[i] == KISSFEnd {
			t.Errorf("raw FEND at position %d", i)
		}
	}
}

func TestEncodeFrameKISS_RoundTrip(t *testing.T) {
	f := &Frame{
		Destination: Address{Callsign: "DEST", SSID: 0},
		Source:      Address{Callsign: "SRC", SSID: 0},
		Type:        FrameUI,
		Control:     CtrlUI,
		PID:         PIDNone,
		Payload:     []byte("hi"),
	}

	enc, err := EncodeFrameKISS(3, f)
	if err != nil {
		t.Fatalf("EncodeFrameKISS: %v", err)
	}

	var gotPort, gotCmd byte
	var gotData []byte
	dec := NewKISSDecoder(func(port, cmd byte, data []byte) {
		gotPort, gotCmd = port, cmd
		gotData = append([]byte{}, data...)
	})
	_, _ = dec.Write(enc)

	if gotPort != 3 {
		t.Fatalf("port: got %d, want 3", gotPort)
	}
	if gotCmd != 0 {
		t.Fatalf("cmd: got %d, want 0", gotCmd)
	}

	parsed, err := ParseFrame(gotData)
	if err != nil {
		t.Fatalf("ParseFrame decoded KISS payload: %v", err)
	}
	if !bytes.Equal(parsed.Payload, f.Payload) {
		t.Fatalf("payload: got %q, want %q", parsed.Payload, f.Payload)
	}
}

func TestKISSDecoder_RoundTrip(t *testing.T) {
	original := []byte{0x01, 0x02, KISSFEnd, KISSFEsc, 0x03}
	enc := KISSEncode(2, 0, original)

	var got []byte
	var gotPort, gotCmd byte
	dec := NewKISSDecoder(func(port, cmd byte, data []byte) {
		gotPort = port
		gotCmd = cmd
		got = data
	})
	dec.Write(enc)

	if gotPort != 2 {
		t.Errorf("port: got %d, want 2", gotPort)
	}
	if gotCmd != 0 {
		t.Errorf("cmd: got %d, want 0", gotCmd)
	}
	if !bytes.Equal(got, original) {
		t.Errorf("data: got %v, want %v", got, original)
	}
}

func TestKISSDecoder_MultipleFrames(t *testing.T) {
	count := 0
	dec := NewKISSDecoder(func(port, cmd byte, data []byte) {
		count++
	})
	for i := 0; i < 5; i++ {
		dec.Write(KISSEncode(0, 0, []byte{byte(i)}))
	}
	if count != 5 {
		t.Errorf("got %d frames, want 5", count)
	}
}

func TestKISSDecoder_Reset(t *testing.T) {
	count := 0
	dec := NewKISSDecoder(func(port, cmd byte, data []byte) { count++ })
	// Write partial frame then reset.
	dec.Write([]byte{KISSFEnd, 0x00, 0x01})
	dec.Reset()
	// Now write a complete frame.
	dec.Write(KISSEncode(0, 0, []byte{0xFF}))
	if count != 1 {
		t.Errorf("got %d frames after reset, want 1", count)
	}
}

func TestKISSDecoder_EmptyPayload(t *testing.T) {
	called := false
	dec := NewKISSDecoder(func(port, cmd byte, data []byte) {
		called = true
		if len(data) != 0 {
			t.Fatalf("data len: got %d, want 0", len(data))
		}
	})
	dec.Write(KISSEncode(0, 0, nil))
	if !called {
		t.Fatal("expected callback")
	}
}

func TestKISSDecoder_HighPortAndCommandNibbles(t *testing.T) {
	var gotPort, gotCmd byte
	dec := NewKISSDecoder(func(port, cmd byte, data []byte) {
		gotPort, gotCmd = port, cmd
	})
	dec.Write(KISSEncode(0x0F, 0x0F, []byte{0x01}))
	if gotPort != 0x0F || gotCmd != 0x0F {
		t.Fatalf("got port/cmd=%d/%d, want 15/15", gotPort, gotCmd)
	}
}

func TestKISSDecoder_DanglingEscapeBeforeDelimiter(t *testing.T) {
	var got []byte
	dec := NewKISSDecoder(func(port, cmd byte, data []byte) {
		got = append([]byte{}, data...)
	})

	stream := []byte{KISSFEnd, 0x00, 0x11, KISSFEsc, KISSFEnd, KISSFEnd}
	dec.Write(stream)

	want := []byte{0x11, KISSFEnd}
	if !bytes.Equal(got, want) {
		t.Fatalf("decoded data: got %v, want %v", got, want)
	}
}

func TestKISSDecoder_UnknownEscapeCodePassThrough(t *testing.T) {
	var got []byte
	dec := NewKISSDecoder(func(port, cmd byte, data []byte) {
		got = append([]byte{}, data...)
	})

	// Unknown transposition byte 0x55 is passed through as-is.
	stream := []byte{KISSFEnd, 0x00, KISSFEsc, 0x55, KISSFEnd}
	dec.Write(stream)

	if !bytes.Equal(got, []byte{0x55}) {
		t.Fatalf("decoded data: got %v, want [85]", got)
	}
}

func TestKISSDecoder_FragmentedEscapedWrite(t *testing.T) {
	var got []byte
	dec := NewKISSDecoder(func(port, cmd byte, data []byte) {
		got = append([]byte{}, data...)
	})

	enc := KISSEncode(0, 0, []byte{0x01, KISSFEnd, 0x02})
	for _, b := range enc {
		_, _ = dec.Write([]byte{b})
	}

	want := []byte{0x01, KISSFEnd, 0x02}
	if !bytes.Equal(got, want) {
		t.Fatalf("decoded data: got %v, want %v", got, want)
	}
}

func TestKISSDecoder_NoStartDelimiterNoDispatch(t *testing.T) {
	count := 0
	dec := NewKISSDecoder(func(port, cmd byte, data []byte) { count++ })
	dec.Write([]byte{0x00, 0x01, 0x02, 0x03})
	if count != 0 {
		t.Fatalf("unexpected dispatch count: got %d, want 0", count)
	}
}

func TestKISSDecoder_DanglingEscapeAtEndNoDispatch(t *testing.T) {
	count := 0
	dec := NewKISSDecoder(func(port, cmd byte, data []byte) { count++ })
	dec.Write([]byte{KISSFEnd, 0x00, 0x01, KISSFEsc})
	if count != 0 {
		t.Fatalf("unexpected dispatch count before frame end: got %d, want 0", count)
	}
}

func FuzzKISSDecoder(f *testing.F) {
	f.Add(KISSEncode(0, 0, []byte{0x01, 0x02, 0x03}))
	f.Fuzz(func(t *testing.T, data []byte) {
		dec := NewKISSDecoder(func(port, cmd byte, d []byte) {})
		dec.Write(data)
	})
}
