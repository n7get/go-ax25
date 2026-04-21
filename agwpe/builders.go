// builders.go - convenience frame builders used by Server.
package agwpe

import (
	"encoding/binary"
	"fmt"
	"strings"
	"time"

	"github.com/n7get/go-ax25/ax25"
)

// BuildVersionResp builds an AGWPE version response frame (kind 'R').
func BuildVersionResp(major, minor int) Frame {
	data := make([]byte, 8)
	binary.LittleEndian.PutUint32(data[0:4], uint32(major))
	binary.LittleEndian.PutUint32(data[4:8], uint32(minor))
	return Frame{Kind: KindVersionResp, Data: data}
}

// BuildPortInfoResp builds an AGWPE port info response frame (kind 'G').
// The data area follows the AGWPE spec format:
//
//	"{numPorts};Port1 description;Port2 description;\x00"
//
// desc is the description of the single port (e.g. "Port1 go-ax25 radio").
// A null terminator is appended per Direwolf convention.
func BuildPortInfoResp(port, numPorts int, desc string) Frame {
	body := fmt.Sprintf("%d;%s;\x00", numPorts, desc)
	return Frame{Port: uint8(port), Kind: KindPortInfoResp, Data: []byte(body)}
}

// BuildPortCapResp builds an AGWPE port capabilities response frame (kind 'g').
func BuildPortCapResp(port int) Frame {
	data := make([]byte, 12)
	return Frame{Port: uint8(port), Kind: KindPortCapResp, Data: data}
}

// BuildRegisterCallResp builds an AGWPE register callsign response (kind 'X').
func BuildRegisterCallResp(call string, success bool, port uint8) Frame {
	var ok byte
	if success {
		ok = 1
	}
	return Frame{Port: port, Kind: KindRegisterCall, CallFrom: call, Data: []byte{ok}}
}

// BuildConnectedResp builds an AGWPE connection established frame (kind 'C').
// Direwolf uses:
//   - "*** CONNECTED With Station %s" when we initiated the connection
//   - "*** CONNECTED To Station %s" when the remote station connected to us
func BuildConnectedResp(port int, localCall, remoteCall string, localInitiated bool) Frame {
	var msg string
	if localInitiated {
		msg = fmt.Sprintf("*** CONNECTED With Station %s\r", remoteCall)
	} else {
		msg = fmt.Sprintf("*** CONNECTED To Station %s\r", remoteCall)
	}
	msg += "\x00" // null terminator included in data_len per Direwolf
	return Frame{Port: uint8(port), Kind: KindConnectResp, CallFrom: remoteCall, CallTo: localCall, Data: []byte(msg)}
}

// BuildDisconnectedResp builds an AGWPE disconnected frame (kind 'd').
func BuildDisconnectedResp(port int, localCall, remoteCall string) Frame {
	msg := fmt.Sprintf("*** DISCONNECTED From Station %s\r\x00", remoteCall)
	return Frame{
		Port:     uint8(port),
		Kind:     KindDisconnectResp,
		CallFrom: remoteCall,
		CallTo:   localCall,
		Data:     []byte(msg),
	}
}

// BuildDisconnectedTimeoutResp builds an AGWPE disconnected frame for a
// timeout (retry-out) condition, matching Direwolf's format.
func BuildDisconnectedTimeoutResp(port int, localCall, remoteCall string) Frame {
	msg := fmt.Sprintf("*** DISCONNECTED RETRYOUT With %s\r\x00", remoteCall)
	return Frame{
		Port:     uint8(port),
		Kind:     KindDisconnectResp,
		CallFrom: remoteCall,
		CallTo:   localCall,
		Data:     []byte(msg),
	}
}

// BuildConnectedData builds an AGWPE connected data frame (kind 'D').
func BuildConnectedData(port int, localCall, remoteCall string, data []byte) Frame {
	return Frame{Port: uint8(port), Kind: KindRecvData, CallFrom: remoteCall, CallTo: localCall, Data: append([]byte(nil), data...)}
}

// BuildOutstandingPort builds an AGWPE outstanding frames on port response (kind 'y').
func BuildOutstandingPort(port, count int) Frame {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, uint32(count))
	return Frame{Port: uint8(port), Kind: KindOutstandingResp, Data: data}
}

// BuildOutstandingConn builds an AGWPE outstanding frames on connection
// response (kind 'Y'). Direwolf uses 'Y' for both request and response.
func BuildOutstandingConn(port int, localCall, remoteCall string, count int) Frame {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, uint32(count))
	return Frame{Port: uint8(port), Kind: KindOutstandingReq, CallFrom: localCall, CallTo: remoteCall, Data: data}
}

// ─── Monitor frame formatting ─────────────────────────────────────────────────

// FormatMonitorAddrs formats the address header for a monitor frame in the
// Direwolf/AGWPE format: " 1:Fm SRC To DST Via DIGI1,DIGI2* "
func FormatMonitorAddrs(port int, frame *ax25.Frame) string {
	src := frame.Source.String()
	dst := frame.Destination.String()

	if len(frame.Digipeaters) > 0 {
		parts := make([]string, len(frame.Digipeaters))
		for i, d := range frame.Digipeaters {
			s := d.String()
			if d.HasBeenRepeated {
				s += "*"
			}
			parts[i] = s
		}
		return fmt.Sprintf(" %d:Fm %s To %s Via %s ", port+1, src, dst, strings.Join(parts, ","))
	}
	return fmt.Sprintf(" %d:Fm %s To %s ", port+1, src, dst)
}

// FormatMonitorDesc formats the frame description tag and returns the
// appropriate monitor kind byte ('U', 'I', or 'S').
func FormatMonitorDesc(frame *ax25.Frame) (kind byte, desc string) {
	ctrl := frame.Control
	pfBit := ctrl & ax25.CtrlPFBit
	pfChar := "P"
	if !frame.IsCommand {
		pfChar = "F"
	}

	switch frame.Type {
	case ax25.FrameUI:
		desc = fmt.Sprintf("<UI pid=%02X Len=%d %s=%d>", frame.PID, len(frame.Payload), pfChar, pfBit>>4)
		return 'U', desc
	case ax25.FrameI:
		ns := ax25.ExtractNS(ctrl)
		nr := ax25.ExtractNR(ctrl)
		desc = fmt.Sprintf("<I S%d R%d pid=%02X Len=%d %s=%d>", ns, nr, frame.PID, len(frame.Payload), pfChar, pfBit>>4)
		return 'I', desc
	default:
		name := ax25.ControlName(ctrl)
		nr := ax25.ExtractNR(ctrl)
		switch {
		case ctrl&0x0F == ax25.CtrlRRMask:
			desc = fmt.Sprintf("<RR R%d %s=%d>", nr, pfChar, pfBit>>4)
		case ctrl&0x0F == ax25.CtrlRNRMask:
			desc = fmt.Sprintf("<RNR R%d %s=%d>", nr, pfChar, pfBit>>4)
		case ctrl&0x0F == ax25.CtrlREJMask:
			desc = fmt.Sprintf("<REJ R%d %s=%d>", nr, pfChar, pfBit>>4)
		default:
			desc = fmt.Sprintf("<%s %s=%d>", name, pfChar, pfBit>>4)
		}
		return 'S', desc
	}
}

// FromAX25Monitor wraps an AX.25 frame as an AGWPE monitoring frame in the
// human-readable text format that Direwolf sends. The data payload is:
//
//	" 1:Fm SRC To DST <desc>[HH:MM:SS]\r<payload>\r\0"
func FromAX25Monitor(frame *ax25.Frame, port int) (Frame, error) {
	if frame == nil {
		return Frame{}, fmt.Errorf("nil frame")
	}

	kind, desc := FormatMonitorDesc(frame)
	addrs := FormatMonitorAddrs(port, frame)

	now := time.Now()
	ts := fmt.Sprintf("[%02d:%02d:%02d]\r", now.Hour(), now.Minute(), now.Second())

	var sb strings.Builder
	sb.WriteString(addrs)
	sb.WriteString(desc)
	sb.WriteString(ts)

	if len(frame.Payload) > 0 {
		sb.Write(frame.Payload)
		sb.WriteByte('\r')
	}
	sb.WriteByte(0) // null terminator, included in data length per Direwolf

	return Frame{
		Port:     uint8(port),
		Kind:     kind,
		CallFrom: frame.Source.String(),
		CallTo:   frame.Destination.String(),
		Data:     []byte(sb.String()),
	}, nil
}
