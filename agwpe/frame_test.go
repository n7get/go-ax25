package agwpe_test

import (
	"bytes"
	"testing"

	"github.com/n7get/go-ax25/agwpe"
	"github.com/n7get/go-ax25/ax25"
)

func TestFrame_EncodeDecodeRoundTrip(t *testing.T) {
	f := &agwpe.Frame{
		Port:     1,
		Kind:     agwpe.KindSendUnproto,
		PID:      0xF0,
		CallFrom: "N0CALL-1",
		CallTo:   "APRS",
		Data:     []byte("hello world"),
		User:     42,
	}
	enc, err := f.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(enc) != agwpe.HeaderSize+len(f.Data) {
		t.Errorf("encoded length = %d, want %d", len(enc), agwpe.HeaderSize+len(f.Data))
	}
	got, err := agwpe.DecodeFrame(enc)
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}
	if got.Port != f.Port || got.Kind != f.Kind || got.PID != f.PID {
		t.Errorf("header mismatch: got %+v", got)
	}
	if got.CallFrom != f.CallFrom {
		t.Errorf("CallFrom = %q, want %q", got.CallFrom, f.CallFrom)
	}
	if got.CallTo != f.CallTo {
		t.Errorf("CallTo = %q, want %q", got.CallTo, f.CallTo)
	}
	if !bytes.Equal(got.Data, f.Data) {
		t.Errorf("Data mismatch")
	}
	if got.User != f.User {
		t.Errorf("User = %d, want %d", got.User, f.User)
	}
}

func TestDecoder_MultipleFrames(t *testing.T) {
	var received []*agwpe.Frame
	dec := agwpe.NewDecoder(func(f *agwpe.Frame) {
		received = append(received, f)
	})
	for _, kind := range []byte{agwpe.KindVersionReq, agwpe.KindPortInfoReq, agwpe.KindEnableMonitor} {
		f := &agwpe.Frame{Kind: kind}
		enc, _ := f.Encode()
		_, _ = dec.Write(enc)
	}
	if len(received) != 3 {
		t.Fatalf("got %d frames, want 3", len(received))
	}
}

func TestDecoder_ByteByByte(t *testing.T) {
	var received []*agwpe.Frame
	dec := agwpe.NewDecoder(func(f *agwpe.Frame) {
		received = append(received, f)
	})
	f := &agwpe.Frame{Kind: agwpe.KindSendRaw, Data: []byte{0x01, 0x02, 0x03}}
	enc, _ := f.Encode()
	for _, b := range enc {
		_, _ = dec.Write([]byte{b})
	}
	if len(received) != 1 {
		t.Fatalf("got %d frames, want 1", len(received))
	}
	if !bytes.Equal(received[0].Data, f.Data) {
		t.Errorf("data mismatch")
	}
}

func TestFromAX25Raw(t *testing.T) {
	src, _ := ax25.ParseAddress("N0CALL-1")
	dst, _ := ax25.ParseAddress("APRS")
	frame := &ax25.Frame{
		Source:      src,
		Destination: dst,
		Control:     ax25.CtrlUI,
		PID:         ax25.PIDNone,
		Payload:     []byte("test"),
	}
	agf, err := agwpe.FromAX25Raw(frame, 0)
	if err != nil {
		t.Fatalf("FromAX25Raw: %v", err)
	}
	if agf.Kind != agwpe.KindSendRaw {
		t.Errorf("Kind = %c, want K", agf.Kind)
	}
	if len(agf.Data) == 0 {
		t.Error("Data is empty")
	}
}

func TestFromAX25Unproto_NoDigis(t *testing.T) {
	src, _ := ax25.ParseAddress("N0CALL-1")
	dst, _ := ax25.ParseAddress("APRS")
	frame := &ax25.Frame{
		Source:      src,
		Destination: dst,
		Control:     ax25.CtrlUI,
		PID:         ax25.PIDNone,
		Payload:     []byte("beacon"),
	}
	agf, err := agwpe.FromAX25Unproto(frame, 0)
	if err != nil {
		t.Fatalf("FromAX25Unproto: %v", err)
	}
	if agf.Kind != agwpe.KindSendUnproto {
		t.Errorf("Kind = %c, want M", agf.Kind)
	}
}

func TestBuilders(t *testing.T) {
	tests := []struct {
		name string
		f    *agwpe.Frame
		kind byte
	}{
		{"VersionReq", agwpe.BuildVersionReq(), agwpe.KindVersionReq},
		{"PortInfoReq", agwpe.BuildPortInfoReq(), agwpe.KindPortInfoReq},
		{"PortCapReq", agwpe.BuildPortCapReq(1), agwpe.KindPortCapReq},
		{"EnableMonitor", agwpe.BuildEnableMonitor(), agwpe.KindEnableMonitor},
		{"EnableRaw", agwpe.BuildEnableRaw(), agwpe.KindEnableRaw},
		{"RegisterCall", agwpe.BuildRegisterCall(0, "N0CALL"), agwpe.KindRegisterCall},
		{"ConnectReq", agwpe.BuildConnectReq(0, "SRC", "DST"), agwpe.KindConnectReq},
		{"DisconnectReq", agwpe.BuildDisconnectReq(0, "SRC", "DST"), agwpe.KindDisconnectReq},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.f.Kind != tt.kind {
				t.Errorf("Kind = %c, want %c", tt.f.Kind, tt.kind)
			}
			_, err := tt.f.Encode()
			if err != nil {
				t.Errorf("Encode: %v", err)
			}
		})
	}
}

func TestParseVersionResp(t *testing.T) {
	f := &agwpe.Frame{
		Kind: agwpe.KindVersionResp,
		Data: []byte{0xD5, 0x07, 0x00, 0x00},
	}
	major, minor, err := agwpe.ParseVersionResp(f)
	if err != nil {
		t.Fatalf("ParseVersionResp: %v", err)
	}
	if major != 2005 || minor != 0 {
		t.Errorf("version = %d.%d, want 2005.0", major, minor)
	}
}

func TestKindString(t *testing.T) {
	if s := agwpe.KindString('R'); s != "Version" {
		t.Errorf("KindString(R) = %q, want Version", s)
	}
	if s := agwpe.KindString(0xFF); s == "" {
		t.Error("KindString(unknown) returned empty string")
	}
}
