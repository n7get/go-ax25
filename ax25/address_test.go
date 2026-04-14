// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"testing"
)

func TestParseAddress_Valid(t *testing.T) {
	cases := []struct {
		input    string
		callsign string
		ssid     uint8
	}{
		{"N7GET", "N7GET", 0},
		{"N7GET-0", "N7GET", 0},
		{"N7GET-5", "N7GET", 5},
		{"N7GET-15", "N7GET", 15},
		{"W1AW", "W1AW", 0},
		{"n7get", "N7GET", 0}, // lower-case normalised
	}
	for _, tc := range cases {
		a, err := ParseAddress(tc.input)
		if err != nil {
			t.Errorf("ParseAddress(%q): unexpected error: %v", tc.input, err)
			continue
		}
		if a.Callsign != tc.callsign {
			t.Errorf("ParseAddress(%q).Callsign = %q, want %q", tc.input, a.Callsign, tc.callsign)
		}
		if a.SSID != tc.ssid {
			t.Errorf("ParseAddress(%q).SSID = %d, want %d", tc.input, a.SSID, tc.ssid)
		}
	}
}

func TestParseAddress_Invalid(t *testing.T) {
	cases := []string{
		"",
		"TOOLONGCALL",
		"N7GET-16",
		"N7GET-99",
		"BAD!CALL",
		"-5",
	}
	for _, s := range cases {
		_, err := ParseAddress(s)
		if err == nil {
			t.Errorf("ParseAddress(%q): expected error, got nil", s)
		}
	}
}

func TestAddressString(t *testing.T) {
	cases := []struct {
		a    Address
		want string
	}{
		{Address{Callsign: "N7GET", SSID: 0}, "N7GET"},
		{Address{Callsign: "N7GET", SSID: 5}, "N7GET-5"},
	}
	for _, tc := range cases {
		if got := tc.a.String(); got != tc.want {
			t.Errorf("Address.String() = %q, want %q", got, tc.want)
		}
	}
}

func TestAddressEncodeDecodeRoundTrip(t *testing.T) {
	original := Address{Callsign: "N7GET", SSID: 5, HasBeenRepeated: true, IsLast: true}
	enc := original.Encode()
	dec, err := DecodeAddress(enc[:])
	if err != nil {
		t.Fatalf("DecodeAddress: %v", err)
	}
	if dec.Callsign != original.Callsign {
		t.Errorf("Callsign: got %q, want %q", dec.Callsign, original.Callsign)
	}
	if dec.SSID != original.SSID {
		t.Errorf("SSID: got %d, want %d", dec.SSID, original.SSID)
	}
	if dec.HasBeenRepeated != original.HasBeenRepeated {
		t.Errorf("HasBeenRepeated: got %v, want %v", dec.HasBeenRepeated, original.HasBeenRepeated)
	}
	if dec.IsLast != original.IsLast {
		t.Errorf("IsLast: got %v, want %v", dec.IsLast, original.IsLast)
	}
}

func TestAddressEqual(t *testing.T) {
	a := Address{Callsign: "N7GET", SSID: 1}
	b := Address{Callsign: "n7get", SSID: 1}
	c := Address{Callsign: "N7GET", SSID: 2}
	if !a.Equal(b) {
		t.Error("Equal: case-insensitive match failed")
	}
	if a.Equal(c) {
		t.Error("Equal: different SSID should not match")
	}
}

func FuzzParseAddress(f *testing.F) {
	f.Add("N7GET-5")
	f.Add("")
	f.Add("TOOLONG-99")
	f.Fuzz(func(t *testing.T, s string) {
		a, err := ParseAddress(s)
		if err == nil {
			// Round-trip: encode then decode.
			enc := a.Encode()
			_, err2 := DecodeAddress(enc[:])
			if err2 != nil {
				t.Errorf("DecodeAddress after Encode failed: %v", err2)
			}
		}
	})
}
