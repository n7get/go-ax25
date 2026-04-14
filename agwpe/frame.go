// Package agwpe implements the AGW Packet Engine (AGWPE) protocol.
//
// AGWPE is a TCP-based protocol used to communicate with software TNCs such
// as Direwolf. Each AGWPE message consists of a fixed 36-byte header followed
// by an optional variable-length data payload.
package agwpe

import (
	"encoding/binary"
	"fmt"
	"io"
	"strings"

	"github.com/n7get/go-ax25/ax25"
)

const (
	HeaderSize   = 36
	CallsignLen  = 10
	MaxDataLen   = 512
	MaxFrameSize = HeaderSize + MaxDataLen
)

// DataKind constants (ASCII codes used in the data_kind header field).
const (
	KindVersionReq      byte = 'R'
	KindVersionResp     byte = 'R'
	KindPortInfoReq     byte = 'G'
	KindPortInfoResp    byte = 'G'
	KindPortCapReq      byte = 'g'
	KindPortCapResp     byte = 'g'
	KindRegisterCall    byte = 'X'
	KindUnregisterCall  byte = 'x'
	KindHeardReq        byte = 'H'
	KindConnectReq      byte = 'C'
	KindConnectViaReq   byte = 'v'
	KindConnectResp     byte = 'c'
	KindSendData        byte = 'D'
	KindRecvData        byte = 'D'
	KindDisconnectReq   byte = 'd'
	KindDisconnectResp  byte = 'd'
	KindSendUnproto     byte = 'M'
	KindSendUnprotoVia  byte = 'V'
	KindRecvUnproto     byte = 'U'
	KindRecvSupervisory byte = 'S'
	KindRecvIFrame      byte = 'I'
	KindRecvRaw         byte = 'T'
	KindSendRaw         byte = 'K'
	KindEnableRaw       byte = 'k'
	KindEnableMonitor   byte = 'm'
	KindOutstandingReq  byte = 'Y'
	KindOutstandingResp byte = 'y'
)

// Frame is a complete AGWPE message (header + data).
type Frame struct {
	Port     uint8
	Kind     byte
	PID      uint8
	CallFrom string
	CallTo   string
	Data     []byte
	User     uint32
}

// Encode serialises the frame to wire format (little-endian header).
func (f *Frame) Encode() ([]byte, error) {
	if len(f.Data) > MaxDataLen {
		return nil, fmt.Errorf("agwpe: data length %d exceeds maximum %d", len(f.Data), MaxDataLen)
	}
	buf := make([]byte, HeaderSize+len(f.Data))
	buf[0] = f.Port
	buf[4] = f.Kind
	buf[6] = f.PID
	setCallsign(buf[8:18], f.CallFrom)
	setCallsign(buf[18:28], f.CallTo)
	binary.LittleEndian.PutUint32(buf[28:32], uint32(len(f.Data)))
	binary.LittleEndian.PutUint32(buf[32:36], f.User)
	copy(buf[HeaderSize:], f.Data)
	return buf, nil
}

// DecodeFrame parses a complete AGWPE frame from raw bytes.
func DecodeFrame(data []byte) (*Frame, error) {
	if len(data) < HeaderSize {
		return nil, fmt.Errorf("agwpe: frame too short: %d < %d", len(data), HeaderSize)
	}
	dataLen := binary.LittleEndian.Uint32(data[28:32])
	if dataLen > MaxDataLen {
		return nil, fmt.Errorf("agwpe: data_len %d exceeds maximum %d", dataLen, MaxDataLen)
	}
	if uint32(len(data)) < uint32(HeaderSize)+dataLen {
		return nil, fmt.Errorf("agwpe: frame truncated")
	}
	f := &Frame{
		Port:     data[0],
		Kind:     data[4],
		PID:      data[6],
		CallFrom: getCallsign(data[8:18]),
		CallTo:   getCallsign(data[18:28]),
		User:     binary.LittleEndian.Uint32(data[32:36]),
	}
	if dataLen > 0 {
		f.Data = make([]byte, dataLen)
		copy(f.Data, data[HeaderSize:HeaderSize+int(dataLen)])
	}
	return f, nil
}

// ─── Streaming Decoder ────────────────────────────────────────────────────────

// FrameCallback is called for each decoded AGWPE frame.
type FrameCallback func(frame *Frame)

type decoderState int

const (
	stateHeader decoderState = iota
	stateData
)

// Decoder is a streaming AGWPE frame decoder satisfying io.Writer.
type Decoder struct {
	cb      FrameCallback
	state   decoderState
	header  [HeaderSize]byte
	hdrN    int
	dataLen uint32
	data    []byte
	dataN   int
}

// NewDecoder creates a new Decoder. cb must not be nil.
func NewDecoder(cb FrameCallback) *Decoder { return &Decoder{cb: cb} }

// Reset discards any partial frame.
func (d *Decoder) Reset() {
	d.state = stateHeader
	d.hdrN = 0
	d.dataN = 0
	d.data = nil
}

// Write implements io.Writer.
func (d *Decoder) Write(p []byte) (int, error) {
	for _, b := range p {
		switch d.state {
		case stateHeader:
			d.header[d.hdrN] = b
			d.hdrN++
			if d.hdrN == HeaderSize {
				d.dataLen = binary.LittleEndian.Uint32(d.header[28:32])
				if d.dataLen > MaxDataLen {
					d.dataLen = MaxDataLen
				}
				if d.dataLen > 0 {
					d.data = make([]byte, d.dataLen)
					d.dataN = 0
					d.state = stateData
				} else {
					d.deliver()
				}
			}
		case stateData:
			d.data[d.dataN] = b
			d.dataN++
			if uint32(d.dataN) >= d.dataLen {
				d.deliver()
			}
		}
	}
	return len(p), nil
}

func (d *Decoder) deliver() {
	f := &Frame{
		Port:     d.header[0],
		Kind:     d.header[4],
		PID:      d.header[6],
		CallFrom: getCallsign(d.header[8:18]),
		CallTo:   getCallsign(d.header[18:28]),
		User:     binary.LittleEndian.Uint32(d.header[32:36]),
	}
	if d.dataLen > 0 {
		f.Data = make([]byte, d.dataLen)
		copy(f.Data, d.data[:d.dataLen])
	}
	d.Reset()
	if d.cb != nil {
		d.cb(f)
	}
}

// ─── AX.25 <-> AGWPE conversion ───────────────────────────────────────────────

// FromAX25Raw wraps an AX.25 frame in an AGWPE 'K' (send raw) frame.
func FromAX25Raw(frame *ax25.Frame, port uint8) (*Frame, error) {
	if frame == nil {
		return nil, fmt.Errorf("agwpe: FromAX25Raw: nil frame")
	}
	raw, err := frame.Encode()
	if err != nil {
		return nil, fmt.Errorf("agwpe: FromAX25Raw: encode: %w", err)
	}
	if len(raw) > MaxDataLen {
		return nil, fmt.Errorf("agwpe: FromAX25Raw: frame too large")
	}
	return &Frame{
		Port:     port,
		Kind:     KindSendRaw,
		CallFrom: frame.Source.String(),
		CallTo:   frame.Destination.String(),
		Data:     raw,
	}, nil
}

// FromAX25Unproto wraps an AX.25 UI frame in an AGWPE 'M' or 'V' frame.
func FromAX25Unproto(frame *ax25.Frame, port uint8) (*Frame, error) {
	if frame == nil {
		return nil, fmt.Errorf("agwpe: FromAX25Unproto: nil frame")
	}
	kind := KindSendUnproto
	payload := frame.Payload
	if len(frame.Digipeaters) > 0 {
		kind = KindSendUnprotoVia
		parts := make([]string, len(frame.Digipeaters))
		for i, d := range frame.Digipeaters {
			parts[i] = d.String()
		}
		prefix := []byte(strings.Join(parts, " ") + "\r")
		payload = append(prefix, frame.Payload...)
	}
	if len(payload) > MaxDataLen {
		return nil, fmt.Errorf("agwpe: FromAX25Unproto: payload too large")
	}
	return &Frame{
		Port:     port,
		Kind:     kind,
		PID:      frame.PID,
		CallFrom: frame.Source.String(),
		CallTo:   frame.Destination.String(),
		Data:     payload,
	}, nil
}

// ToAX25 converts an AGWPE 'T' (received raw) frame to an AX.25 frame.
func ToAX25(f *Frame) (*ax25.Frame, error) {
	if f == nil {
		return nil, fmt.Errorf("agwpe: ToAX25: nil frame")
	}
	if f.Kind != KindRecvRaw {
		return nil, fmt.Errorf("agwpe: ToAX25: expected kind 'T', got '%c'", f.Kind)
	}
	return ax25.ParseFrame(f.Data)
}

// ─── Builder helpers ──────────────────────────────────────────────────────────

func BuildVersionReq() *Frame    { return &Frame{Kind: KindVersionReq} }
func BuildPortInfoReq() *Frame   { return &Frame{Kind: KindPortInfoReq} }
func BuildPortCapReq(port uint8) *Frame { return &Frame{Port: port, Kind: KindPortCapReq} }
func BuildEnableMonitor() *Frame { return &Frame{Kind: KindEnableMonitor} }
func BuildEnableRaw() *Frame     { return &Frame{Kind: KindEnableRaw} }
func BuildRegisterCall(port uint8, callsign string) *Frame {
	return &Frame{Port: port, Kind: KindRegisterCall, CallFrom: callsign}
}
func BuildUnregisterCall(port uint8, callsign string) *Frame {
	return &Frame{Port: port, Kind: KindUnregisterCall, CallFrom: callsign}
}
func BuildConnectReq(port uint8, from, to string) *Frame {
	return &Frame{Port: port, Kind: KindConnectReq, CallFrom: from, CallTo: to}
}
func BuildDisconnectReq(port uint8, from, to string) *Frame {
	return &Frame{Port: port, Kind: KindDisconnectReq, CallFrom: from, CallTo: to}
}
func BuildSendData(port uint8, from, to string, pid uint8, data []byte) *Frame {
	return &Frame{Port: port, Kind: KindSendData, PID: pid, CallFrom: from, CallTo: to, Data: data}
}
func BuildHeardReq(port uint8) *Frame { return &Frame{Port: port, Kind: KindHeardReq} }

// ─── Response parsing ─────────────────────────────────────────────────────────

// ParseVersionResp extracts major/minor version from an 'R' response frame.
func ParseVersionResp(f *Frame) (major, minor uint16, err error) {
	if f == nil || f.Kind != KindVersionResp {
		return 0, 0, fmt.Errorf("agwpe: ParseVersionResp: wrong kind")
	}
	if len(f.Data) < 4 {
		return 0, 0, fmt.Errorf("agwpe: ParseVersionResp: data too short")
	}
	return binary.LittleEndian.Uint16(f.Data[0:2]),
		binary.LittleEndian.Uint16(f.Data[2:4]), nil
}

// ParseOutstandingResp extracts the outstanding-frames count from a 'y' frame.
func ParseOutstandingResp(f *Frame) (uint32, error) {
	if f == nil || f.Kind != KindOutstandingResp {
		return 0, fmt.Errorf("agwpe: ParseOutstandingResp: wrong kind")
	}
	if len(f.Data) < 4 {
		return 0, fmt.Errorf("agwpe: ParseOutstandingResp: data too short")
	}
	return binary.LittleEndian.Uint32(f.Data[0:4]), nil
}

// ─── Utility ──────────────────────────────────────────────────────────────────

// KindString returns a human-readable name for an AGWPE frame kind byte.
func KindString(kind byte) string {
	switch kind {
	case 'R':
		return "Version"
	case 'G':
		return "PortInfo"
	case 'g':
		return "PortCap"
	case 'X':
		return "RegisterCall"
	case 'x':
		return "UnregisterCall"
	case 'H':
		return "HeardReq"
	case 'C':
		return "ConnectReq"
	case 'v':
		return "ConnectViaReq"
	case 'c':
		return "ConnectResp"
	case 'D':
		return "Data"
	case 'd':
		return "Disconnect"
	case 'M':
		return "SendUnproto"
	case 'V':
		return "SendUnprotoVia"
	case 'U':
		return "RecvUnproto"
	case 'S':
		return "RecvSupervisory"
	case 'I':
		return "RecvIFrame"
	case 'T':
		return "RecvRaw"
	case 'K':
		return "SendRaw"
	case 'k':
		return "EnableRaw"
	case 'm':
		return "EnableMonitor"
	case 'Y':
		return "OutstandingReq"
	case 'y':
		return "OutstandingResp"
	default:
		return fmt.Sprintf("Unknown(0x%02X)", kind)
	}
}

// IsMonitored returns true for frame kinds that carry monitored (received) data.
func IsMonitored(kind byte) bool {
	switch kind {
	case KindRecvUnproto, KindRecvSupervisory, KindRecvIFrame, KindRecvRaw:
		return true
	}
	return false
}

// ReadFrame reads exactly one AGWPE frame from r.
func ReadFrame(r io.Reader) (*Frame, error) {
	hdr := make([]byte, HeaderSize)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, fmt.Errorf("agwpe: read header: %w", err)
	}
	dataLen := binary.LittleEndian.Uint32(hdr[28:32])
	if dataLen > MaxDataLen {
		return nil, fmt.Errorf("agwpe: data_len %d exceeds maximum", dataLen)
	}
	f := &Frame{
		Port:     hdr[0],
		Kind:     hdr[4],
		PID:      hdr[6],
		CallFrom: getCallsign(hdr[8:18]),
		CallTo:   getCallsign(hdr[18:28]),
		User:     binary.LittleEndian.Uint32(hdr[32:36]),
	}
	if dataLen > 0 {
		f.Data = make([]byte, dataLen)
		if _, err := io.ReadFull(r, f.Data); err != nil {
			return nil, fmt.Errorf("agwpe: read data: %w", err)
		}
	}
	return f, nil
}

func setCallsign(dst []byte, s string) {
	for i := range dst {
		dst[i] = 0
	}
	n := len(s)
	if n > len(dst) {
		n = len(dst)
	}
	copy(dst, s[:n])
}

func getCallsign(src []byte) string {
	for i, b := range src {
		if b == 0 {
			return string(src[:i])
		}
	}
	return string(src)
}
