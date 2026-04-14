// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	ErrCallsignTooLong  = errors.New("ax25: callsign too long (max 6 chars)")
	ErrCallsignEmpty    = errors.New("ax25: callsign is empty")
	ErrSSIDOutOfRange   = errors.New("ax25: SSID out of range (0-15)")
	ErrInvalidCallsign  = errors.New("ax25: invalid callsign character")
	ErrShortAddressData = errors.New("ax25: address data too short")
)

// Encode serialises the address into the 7-byte AX.25 wire format.
func (a Address) Encode() [AddressLen]byte {
	var b [AddressLen]byte
	call := strings.ToUpper(a.Callsign)
	for i := 0; i < 6; i++ {
		if i < len(call) {
			b[i] = call[i] << 1
		} else {
			b[i] = ' ' << 1
		}
	}
	b[6] = (a.SSID & 0x0F) << 1
	if a.HasBeenRepeated {
		b[6] |= 0x80
	}
	if a.IsLast {
		b[6] |= 0x01
	}
	return b
}

// DecodeAddress parses a 7-byte AX.25 wire address.
func DecodeAddress(b []byte) (Address, error) {
	if len(b) < AddressLen {
		return Address{}, ErrShortAddressData
	}
	var call [6]byte
	for i := 0; i < 6; i++ {
		call[i] = b[i] >> 1
	}
	callStr := strings.TrimRight(string(call[:]), " ")
	ssid := (b[6] >> 1) & 0x0F
	hbr := (b[6] & 0x80) != 0
	isLast := (b[6] & 0x01) != 0
	return Address{
		Callsign:        callStr,
		SSID:            ssid,
		HasBeenRepeated: hbr,
		IsLast:          isLast,
	}, nil
}

// ParseAddress parses a human-readable address string like "N7GET-5".
func ParseAddress(s string) (Address, error) {
	if s == "" {
		return Address{}, ErrCallsignEmpty
	}
	s = strings.ToUpper(s)
	var callsign string
	var ssid uint8
	if idx := strings.IndexByte(s, '-'); idx >= 0 {
		callsign = s[:idx]
		n, err := strconv.ParseUint(s[idx+1:], 10, 8)
		if err != nil || n > 15 {
			return Address{}, ErrSSIDOutOfRange
		}
		ssid = uint8(n)
	} else {
		callsign = s
	}
	if len(callsign) == 0 {
		return Address{}, ErrCallsignEmpty
	}
	if len(callsign) > MaxCallsign {
		return Address{}, ErrCallsignTooLong
	}
	for _, c := range callsign {
		if !((c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return Address{}, ErrInvalidCallsign
		}
	}
	return Address{Callsign: callsign, SSID: ssid}, nil
}

// String returns the human-readable form, e.g. "N7GET-5" or "N7GET".
func (a Address) String() string {
	if a.SSID == 0 {
		return a.Callsign
	}
	return fmt.Sprintf("%s-%d", a.Callsign, a.SSID)
}

// Equal reports whether two addresses have the same callsign and SSID.
func (a Address) Equal(b Address) bool {
	return strings.EqualFold(a.Callsign, b.Callsign) && a.SSID == b.SSID
}
