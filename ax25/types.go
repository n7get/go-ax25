// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

// Package ax25 implements the AX.25 amateur radio packet protocol.
package ax25

// ---------------------------------------------------------------------------
// Protocol constants
// ---------------------------------------------------------------------------

const (
	KISSFEnd  byte = 0xC0
	KISSFEsc  byte = 0xDB
	KISSTFEnd byte = 0xDC
	KISSTFEsc byte = 0xDD

	PIDNone byte = 0xF0
	PIDText byte = 0xF0

	AddressLen     = 7
	MaxCallsign    = 6
	MaxDigipeaters = 8
	MaxInfoLen     = 256
	BufferSize     = AddressLen*(2+MaxDigipeaters) + 2 + MaxInfoLen + 10
)

// ---------------------------------------------------------------------------
// Control byte constants
// ---------------------------------------------------------------------------

const (
	CtrlUI   byte = 0x03
	CtrlSABM byte = 0x2F
	CtrlDISC byte = 0x43
	CtrlDM   byte = 0x0F
	CtrlUA   byte = 0x63
	CtrlFRMR byte = 0x87

	CtrlRRMask  byte = 0x01
	CtrlRNRMask byte = 0x05
	CtrlREJMask byte = 0x09

	CtrlPFBit byte = 0x10
)

// ---------------------------------------------------------------------------
// Enumerations
// ---------------------------------------------------------------------------

// FrameType classifies an AX.25 frame by its control-byte bit pattern.
type FrameType int

const (
	FrameUI FrameType = iota
	FrameI
	FrameS
	FrameU
	FrameUnknown
)

func (t FrameType) String() string {
	switch t {
	case FrameUI:
		return "UI"
	case FrameI:
		return "I"
	case FrameS:
		return "S"
	case FrameU:
		return "U"
	default:
		return "UNKNOWN"
	}
}

// IOMode selects the framing used on a physical link.
type IOMode int

const (
	IOModeKISS IOMode = iota
	IOModeRaw
)

// ---------------------------------------------------------------------------
// Core structures
// ---------------------------------------------------------------------------

// Address represents a single AX.25 station address.
type Address struct {
	Callsign        string
	SSID            uint8
	HasBeenRepeated bool
	IsLast          bool
}

// Frame represents a fully parsed AX.25 frame.
type Frame struct {
	Destination Address
	Source      Address
	Digipeaters []Address
	IsCommand   bool
	Type        FrameType
	Control     byte
	PID         byte
	Payload     []byte
}

// ---------------------------------------------------------------------------
// Callback types
// ---------------------------------------------------------------------------

// FrameCallback is called when a decoded AX.25 frame is available.
// The frame pointer is only valid for the duration of the call.
type FrameCallback func(f *Frame)

// ErrorCallback is called when a non-fatal error occurs.
type ErrorCallback func(err error)
