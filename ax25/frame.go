// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"errors"
	"fmt"
)

var (
	ErrFrameTooShort    = errors.New("ax25: frame too short")
	ErrTooManyDigis     = errors.New("ax25: too many digipeaters")
	ErrPayloadTooLong   = errors.New("ax25: payload too long")
	ErrInvalidAddress   = errors.New("ax25: invalid address in frame")
)

// IdentifyFrameType returns the FrameType for a raw control byte.
func IdentifyFrameType(ctrl byte) FrameType {
	switch {
	case ctrl == CtrlUI:
		return FrameUI
	case ctrl&0x01 == 0:
		return FrameI
	case ctrl&0x03 == 0x01:
		return FrameS
	default:
		switch ctrl & 0xEF {
		case CtrlSABM, CtrlDISC, CtrlDM, CtrlUA, CtrlFRMR:
			return FrameU
		}
		return FrameUnknown
	}
}

// ExtractNS returns the N(S) send sequence number from an I-frame control byte.
func ExtractNS(ctrl byte) uint8 { return (ctrl >> 1) & 0x07 }

// ExtractNR returns the N(R) receive sequence number from an I or S frame.
func ExtractNR(ctrl byte) uint8 { return (ctrl >> 5) & 0x07 }

// HasPF reports whether the Poll/Final bit is set.
func HasPF(ctrl byte) bool { return ctrl&CtrlPFBit != 0 }

// BuildIControl builds an I-frame control byte.
func BuildIControl(ns, nr uint8, pf bool) byte {
	b := ((nr & 0x07) << 5) | ((ns & 0x07) << 1)
	if pf {
		b |= CtrlPFBit
	}
	return b
}

// BuildRRControl builds a Receive Ready supervisory frame control byte.
func BuildRRControl(nr uint8, pf bool) byte {
	b := ((nr & 0x07) << 5) | CtrlRRMask
	if pf {
		b |= CtrlPFBit
	}
	return b
}

// BuildRNRControl builds a Receive Not Ready supervisory frame control byte.
func BuildRNRControl(nr uint8, pf bool) byte {
	b := ((nr & 0x07) << 5) | CtrlRNRMask
	if pf {
		b |= CtrlPFBit
	}
	return b
}

// BuildREJControl builds a Reject supervisory frame control byte.
func BuildREJControl(nr uint8, pf bool) byte {
	b := ((nr & 0x07) << 5) | CtrlREJMask
	if pf {
		b |= CtrlPFBit
	}
	return b
}

// ParseFrame decodes a raw AX.25 frame byte slice.
func ParseFrame(data []byte) (*Frame, error) {
	if len(data) < AddressLen*2+1 {
		return nil, ErrFrameTooShort
	}

	pos := 0

	// Destination
	dst, err := DecodeAddress(data[pos:])
	if err != nil {
		return nil, fmt.Errorf("%w: destination", ErrInvalidAddress)
	}
	pos += AddressLen

	// Source
	src, err := DecodeAddress(data[pos:])
	if err != nil {
		return nil, fmt.Errorf("%w: source", ErrInvalidAddress)
	}
	pos += AddressLen

	// C/R bit: destination bit7=1 → command
	isCommand := (data[0] & 0x80) != 0 // actually encoded in address byte 6 bit7 of dst

	// Digipeaters
	var digis []Address
	for !src.IsLast && !dst.IsLast {
		if pos+AddressLen > len(data) {
			return nil, ErrFrameTooShort
		}
		if len(digis) >= MaxDigipeaters {
			return nil, ErrTooManyDigis
		}
		d, err := DecodeAddress(data[pos:])
		if err != nil {
			return nil, fmt.Errorf("%w: digipeater %d", ErrInvalidAddress, len(digis))
		}
		digis = append(digis, d)
		pos += AddressLen
		if d.IsLast {
			break
		}
	}
	// If src.IsLast there are no digipeaters; advance past src already done.
	// Re-check: the last address in the chain has IsLast=true.
	// We need to find the actual last address.
	_ = isCommand

	// Re-derive isCommand from the raw address bytes per AX.25 spec:
	// dst address byte[6] bit7 = C bit of destination
	// src address byte[6] bit7 = C bit of source
	// command frame: dst C=1, src C=0
	// response frame: dst C=0, src C=1
	dstCBit := (data[6] & 0x80) != 0
	isCommand = dstCBit

	if pos >= len(data) {
		return nil, ErrFrameTooShort
	}
	ctrl := data[pos]
	pos++

	ft := IdentifyFrameType(ctrl)

	var pid byte
	var payload []byte

	if ft == FrameUI || ft == FrameI {
		if pos < len(data) {
			pid = data[pos]
			pos++
		}
		if pos < len(data) {
			payload = make([]byte, len(data)-pos)
			copy(payload, data[pos:])
		}
	}

	return &Frame{
		Destination: dst,
		Source:      src,
		Digipeaters: digis,
		IsCommand:   isCommand,
		Type:        ft,
		Control:     ctrl,
		PID:         pid,
		Payload:     payload,
	}, nil
}

// Encode serialises the frame to its AX.25 wire representation.
func (f *Frame) Encode() ([]byte, error) {
	if len(f.Digipeaters) > MaxDigipeaters {
		return nil, ErrTooManyDigis
	}
	if len(f.Payload) > MaxInfoLen {
		return nil, ErrPayloadTooLong
	}

	buf := make([]byte, 0, BufferSize)

	// Encode destination — set C bit in byte[6] bit7 for command frames.
	dst := f.Destination
	dstEnc := dst.Encode()
	if f.IsCommand {
		dstEnc[6] |= 0x80
	} else {
		dstEnc[6] &^= 0x80
	}
	buf = append(buf, dstEnc[:]...)

	// Encode source — set C bit for response frames.
	src := f.Source
	srcEnc := src.Encode()
	if !f.IsCommand {
		srcEnc[6] |= 0x80
	} else {
		srcEnc[6] &^= 0x80
	}
	// Mark source as last if no digipeaters.
	if len(f.Digipeaters) == 0 {
		srcEnc[6] |= 0x01
	} else {
		srcEnc[6] &^= 0x01
	}
	buf = append(buf, srcEnc[:]...)

	// Encode digipeaters.
	for i, d := range f.Digipeaters {
		enc := d.Encode()
		if i == len(f.Digipeaters)-1 {
			enc[6] |= 0x01 // last address
		} else {
			enc[6] &^= 0x01
		}
		buf = append(buf, enc[:]...)
	}

	buf = append(buf, f.Control)

	if f.Type == FrameUI || f.Type == FrameI {
		buf = append(buf, f.PID)
		buf = append(buf, f.Payload...)
	}

	return buf, nil
}
