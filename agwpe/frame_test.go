package agwpe

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/n7get/go-ax25/ax25"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	f := &Frame{
		Port:     1,
		Kind:     KindSendData,
		PID:      0xF0,
		CallFrom: "N7GET",
		CallTo:   "W7AW",
		Data:     []byte("hello"),
		User:     42,
	}
	enc, err := f.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	dec, err := DecodeFrame(enc)
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}
	if dec.Port != f.Port || dec.Kind != f.Kind || dec.PID != f.PID {
		t.Errorf("header mismatch: got port=%d kind=%c pid=%02x", dec.Port, dec.Kind, dec.PID)
	}
	if dec.CallFrom != f.CallFrom || dec.CallTo != f.CallTo {
		t.Errorf("callsign mismatch: from=%q to=%q", dec.CallFrom, dec.CallTo)
	}
	if !bytes.Equal(dec.Data, f.Data) {
		t.Errorf("data mismatch: %q vs %q", dec.Data, f.Data)
	}
	if dec.User != f.User {
		t.Errorf("user mismatch: %d vs %d", dec.User, f.User)
	}
}

func TestDecoderStreaming(t *testing.T) {
	var received []*Frame
	dec := NewDecoder(func(f *Frame) {
		received = append(received, f)
	})

	f1 := &Frame{Kind: KindVersionReq}
	f2 := &Frame{Kind: KindSendData, CallFrom: "N7GET", CallTo: "W7AW", Data: []byte("test")}

	enc1, _ := f1.Encode()
	enc2, _ := f2.Encode()
	combined := append(enc1, enc2...)

	// Feed one byte at a time.
	for _, b := range combined {
		_, _ = dec.Write([]byte{b})
	}

	if len(received) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(received))
	}
	if received[0].Kind != KindVersionReq {
		t.Errorf("frame 0: expected kind 'R', got '%c'", received[0].Kind)
	}
	if received[1].Kind != KindSendData {
		t.Errorf("frame 1: expected kind 'D', got '%c'", received[1].Kind)
	}
}

func TestFromAX25RawTNCByte(t *testing.T) {
	// Build a minimal AX.25 UI frame.
	src, _ := ax25.ParseAddress("N7GET")
	dst, _ := ax25.ParseAddress("BEACON")
	axFrame := &ax25.Frame{
		Source:      src,
		Destination: dst,
		Type:        ax25.FrameUI,
		Control:     ax25.CtrlUI,
		PID:         ax25.PIDNone,
		Payload:     []byte("test"),
	}

	agwFrame, err := FromAX25Raw(axFrame, 2)
	if err != nil {
		t.Fatalf("FromAX25Raw: %v", err)
	}

	// First byte should be the TNC indicator (always 0 per AGWPE/Direwolf convention).
	if agwFrame.Data[0] != 0x00 {
		t.Errorf("TNC byte: expected 0x00, got 0x%02X", agwFrame.Data[0])
	}

	// Round-trip through ToAX25.
	// Simulate received raw by setting kind to 'K'.
	agwFrame.Kind = KindRecvRaw
	parsed, err := ToAX25(agwFrame)
	if err != nil {
		t.Fatalf("ToAX25: %v", err)
	}
	if parsed.Source.Callsign != "N7GET" {
		t.Errorf("source: expected N7GET, got %s", parsed.Source.Callsign)
	}
	if !bytes.Equal(parsed.Payload, []byte("test")) {
		t.Errorf("payload mismatch: %q", parsed.Payload)
	}
}

func TestParseVersionResp8Bytes(t *testing.T) {
	f := BuildVersionResp(2005, 127)
	major, minor, err := ParseVersionResp(&f)
	if err != nil {
		t.Fatalf("ParseVersionResp: %v", err)
	}
	if major != 2005 || minor != 127 {
		t.Errorf("expected 2005.127, got %d.%d", major, minor)
	}
}

func TestKindStringCoverage(t *testing.T) {
	cases := []struct {
		kind byte
		want string
	}{
		{'P', "Login"},
		{'R', "Version"},
		{'G', "PortInfo"},
		{'g', "PortCap"},
		{'X', "RegisterCall"},
		{'x', "UnregisterCall"},
		{'H', "HeardReq"},
		{'C', "Connect"},
		{'v', "ConnectVia"},
		{'c', "ConnectPID"},
		{'D', "Data"},
		{'d', "Disconnect"},
		{'M', "SendUnproto"},
		{'V', "SendUnprotoVia"},
		{'U', "RecvUnproto"},
		{'S', "RecvSupervisory"},
		{'I', "RecvIFrame"},
		{'T', "RecvOwn"},
		{'K', "Raw"},
		{'k', "ToggleRaw"},
		{'m', "ToggleMonitor"},
		{'Y', "OutstandingConn"},
		{'y', "OutstandingPort"},
	}
	for _, tc := range cases {
		got := KindString(tc.kind)
		if got != tc.want {
			t.Errorf("KindString(%c): got %q, want %q", tc.kind, got, tc.want)
		}
	}
}

func TestBuildLogin(t *testing.T) {
	f := BuildLogin("user", "pass")
	if f.Kind != KindLogin {
		t.Errorf("kind: expected 'P', got '%c'", f.Kind)
	}
	if len(f.Data) != LoginDataLen {
		t.Errorf("data len: expected %d, got %d", LoginDataLen, len(f.Data))
	}
	if string(f.Data[0:4]) != "user" {
		t.Errorf("username: expected 'user', got %q", f.Data[0:4])
	}
	if string(f.Data[255:259]) != "pass" {
		t.Errorf("password: expected 'pass', got %q", f.Data[255:259])
	}
}

func TestIsMonitored(t *testing.T) {
	monitored := []byte{KindRecvUnproto, KindRecvSupervisory, KindRecvIFrame, KindRecvOwn}
	for _, k := range monitored {
		if !IsMonitored(k) {
			t.Errorf("IsMonitored(%c) should be true", k)
		}
	}
	notMonitored := []byte{KindSendData, KindConnectReq, KindDisconnectReq}
	for _, k := range notMonitored {
		if IsMonitored(k) {
			t.Errorf("IsMonitored(%c) should be false", k)
		}
	}
}

func TestDecodeFrameMalformed(t *testing.T) {
	if _, err := DecodeFrame([]byte{1, 2, 3}); err == nil {
		t.Fatal("expected short-frame error")
	}

	hdr := make([]byte, HeaderSize)
	binary.LittleEndian.PutUint32(hdr[28:32], MaxDataLen+1)
	if _, err := DecodeFrame(hdr); err == nil {
		t.Fatal("expected oversized data_len error")
	}

	hdr2 := make([]byte, HeaderSize)
	binary.LittleEndian.PutUint32(hdr2[28:32], 5)
	truncated := append(hdr2, []byte{1, 2, 3}...)
	if _, err := DecodeFrame(truncated); err == nil {
		t.Fatal("expected truncated-frame error")
	}
}

func TestParseOutstandingResp(t *testing.T) {
	f := BuildOutstandingPort(1, 7)
	count, err := ParseOutstandingResp(&f)
	if err != nil {
		t.Fatalf("ParseOutstandingResp: %v", err)
	}
	if count != 7 {
		t.Fatalf("count: got %d, want 7", count)
	}

	if _, err := ParseOutstandingResp(&Frame{Kind: KindVersionResp}); err == nil {
		t.Fatal("expected wrong-kind error")
	}
	if _, err := ParseOutstandingResp(&Frame{Kind: KindOutstandingResp, Data: []byte{1, 2, 3}}); err == nil {
		t.Fatal("expected short-data error")
	}
}

func TestToAX25Errors(t *testing.T) {
	if _, err := ToAX25(nil); err == nil {
		t.Fatal("expected nil-frame error")
	}
	if _, err := ToAX25(&Frame{Kind: KindSendData}); err == nil {
		t.Fatal("expected wrong-kind error")
	}
	if _, err := ToAX25(&Frame{Kind: KindRecvRaw, Data: []byte{0x00}}); err == nil {
		t.Fatal("expected short-data error")
	}
}
