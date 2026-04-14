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

func FuzzKISSDecoder(f *testing.F) {
	f.Add(KISSEncode(0, 0, []byte{0x01, 0x02, 0x03}))
	f.Fuzz(func(t *testing.T, data []byte) {
		dec := NewKISSDecoder(func(port, cmd byte, d []byte) {})
		dec.Write(data)
	})
}
