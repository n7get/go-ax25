// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

// KISSCallback is called by KISSDecoder for each complete KISS frame.
type KISSCallback func(port, cmd byte, data []byte)

// KISSEncode wraps raw AX.25 bytes in a KISS frame for the given port/command.
func KISSEncode(port, cmd byte, data []byte) []byte {
	out := make([]byte, 0, len(data)+4)
	out = append(out, KISSFEnd)
	out = append(out, (port<<4)|cmd)
	for _, b := range data {
		switch b {
		case KISSFEnd:
			out = append(out, KISSFEsc, KISSTFEnd)
		case KISSFEsc:
			out = append(out, KISSFEsc, KISSTFEsc)
		default:
			out = append(out, b)
		}
	}
	out = append(out, KISSFEnd)
	return out
}

// EncodeFrameKISS is a convenience wrapper that encodes a Frame then KISS-wraps it.
func EncodeFrameKISS(port byte, f *Frame) ([]byte, error) {
	raw, err := f.Encode()
	if err != nil {
		return nil, err
	}
	return KISSEncode(port, 0, raw), nil
}

// kissState is the internal state of the KISS decoder state machine.
type kissState int

const (
	kissStateHunt  kissState = iota // waiting for FEND
	kissStateData                   // inside a frame
	kissStateEscape                 // saw FESC, next byte is transposed
)

// KISSDecoder is a streaming KISS frame decoder.
// Feed bytes via Write; complete frames are delivered to the callback.
type KISSDecoder struct {
	cb    KISSCallback
	state kissState
	buf   []byte
	port  byte
	cmd   byte
}

// NewKISSDecoder creates a KISSDecoder that calls cb for each complete frame.
func NewKISSDecoder(cb KISSCallback) *KISSDecoder {
	return &KISSDecoder{cb: cb}
}

// Write implements io.Writer. It processes p and calls the callback for each
// complete KISS frame found.
func (d *KISSDecoder) Write(p []byte) (int, error) {
	for _, b := range p {
		switch d.state {
		case kissStateHunt:
			if b == KISSFEnd {
				d.state = kissStateData
				d.buf = d.buf[:0]
			}
		case kissStateData:
			switch b {
			case KISSFEnd:
				if len(d.buf) > 0 {
					d.dispatch()
				}
				d.buf = d.buf[:0]
			case KISSFEsc:
				d.state = kissStateEscape
			default:
				if len(d.buf) == 0 {
					d.port = (b >> 4) & 0x0F
					d.cmd = b & 0x0F
				} else {
					d.buf = append(d.buf, b)
				}
				if len(d.buf) == 0 {
					// first byte consumed as port/cmd marker
					d.buf = append(d.buf) // keep buf non-nil but empty
				}
			}
		case kissStateEscape:
			d.state = kissStateData
			switch b {
			case KISSTFEnd:
				d.buf = append(d.buf, KISSFEnd)
			case KISSTFEsc:
				d.buf = append(d.buf, KISSFEsc)
			default:
				d.buf = append(d.buf, b)
			}
		}
	}
	return len(p), nil
}

func (d *KISSDecoder) dispatch() {
	if d.cb != nil {
		out := make([]byte, len(d.buf))
		copy(out, d.buf)
		d.cb(d.port, d.cmd, out)
	}
}

// Reset clears decoder state.
func (d *KISSDecoder) Reset() {
	d.state = kissStateHunt
	d.buf = d.buf[:0]
}
