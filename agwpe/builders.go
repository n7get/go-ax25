// builders.go - convenience frame builders used by Server.
package agwpe

import (
	"encoding/binary"
	"fmt"

	"github.com/n7get/go-ax25/ax25"
)

// BuildVersionResp builds an AGWPE version response frame (kind 'R').
func BuildVersionResp(major, minor int) Frame {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint16(data[0:2], uint16(major))
	binary.LittleEndian.PutUint16(data[2:4], uint16(minor))
	return Frame{Kind: KindVersionResp, Data: data}
}

// BuildPortInfoResp builds an AGWPE port info response frame (kind 'G').
func BuildPortInfoResp(port, numPorts int, desc string) Frame {
	body := fmt.Sprintf("%d;%s", numPorts, desc)
	return Frame{Port: uint8(port), Kind: KindPortInfoResp, Data: []byte(body)}
}

// BuildPortCapResp builds an AGWPE port capabilities response frame (kind 'g').
func BuildPortCapResp(port int) Frame {
	data := make([]byte, 12)
	return Frame{Port: uint8(port), Kind: KindPortCapResp, Data: data}
}

// BuildRegisterCallResp builds an AGWPE register callsign response (kind 'X').
func BuildRegisterCallResp(call string, success bool) Frame {
	var ok byte
	if success {
		ok = 1
	}
	return Frame{Kind: KindRegisterCall, CallFrom: call, Data: []byte{ok}}
}

// BuildConnectedResp builds an AGWPE connection established frame (kind 'C').
func BuildConnectedResp(port int, localCall, remoteCall string, localInitiated bool) Frame {
	msg := fmt.Sprintf("*** CONNECTED To Station %s", remoteCall)
	if !localInitiated {
		msg = fmt.Sprintf("*** CONNECTED From Station %s", remoteCall)
	}
	return Frame{Port: uint8(port), Kind: KindConnectResp, CallFrom: remoteCall, CallTo: localCall, Data: []byte(msg)}
}

// BuildDisconnectedResp builds an AGWPE disconnected frame (kind 'd').
func BuildDisconnectedResp(port int, localCall, remoteCall string) Frame {
	return Frame{
		Port:     uint8(port),
		Kind:     KindDisconnectResp,
		CallFrom: remoteCall,
		CallTo:   localCall,
		Data:     []byte(fmt.Sprintf("*** DISCONNECTED From Station %s", remoteCall)),
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

// BuildOutstandingConn builds an AGWPE outstanding frames on connection response (kind 'Y').
func BuildOutstandingConn(port int, localCall, remoteCall string, count int) Frame {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, uint32(count))
	return Frame{Port: uint8(port), Kind: KindOutstandingReq, CallFrom: remoteCall, CallTo: localCall, Data: data}
}

// FromAX25Monitor wraps an AX.25 frame as an AGWPE monitoring frame.
// kind must be 'U', 'I', or 'S'.
func FromAX25Monitor(frame *ax25.Frame, kind byte) (Frame, error) {
	if frame == nil {
		return Frame{}, fmt.Errorf("nil frame")
	}
	raw, err := frame.Encode()
	if err != nil {
		return Frame{}, err
	}
	return Frame{
		Kind:     kind,
		CallFrom: frame.Source.String(),
		CallTo:   frame.Destination.String(),
		Data:     raw,
	}, nil
}
