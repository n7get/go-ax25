package agwpe

import (
	"bytes"
	"strings"
	"testing"

	"github.com/n7get/go-ax25/ax25"
)

func TestBuildVersionResp8Bytes(t *testing.T) {
	f := BuildVersionResp(2005, 127)
	if len(f.Data) != 8 {
		t.Fatalf("expected 8 bytes, got %d", len(f.Data))
	}
	major, minor, err := ParseVersionResp(&f)
	if err != nil {
		t.Fatalf("ParseVersionResp: %v", err)
	}
	if major != 2005 || minor != 127 {
		t.Errorf("expected 2005.127, got %d.%d", major, minor)
	}
}

func TestBuildPortInfoResp(t *testing.T) {
	f := BuildPortInfoResp(0, 1, "Port1 go-ax25 radio")
	if f.Kind != KindPortInfoResp {
		t.Errorf("kind: expected 'G', got '%c'", f.Kind)
	}
	want := "1;Port1 go-ax25 radio;\x00"
	if string(f.Data) != want {
		t.Errorf("unexpected data: %q, want %q", f.Data, want)
	}
}

func TestBuildConnectedResp(t *testing.T) {
	// Local initiated.
	f := BuildConnectedResp(0, "N7GET", "W7AW", true)
	if !strings.Contains(string(f.Data), "*** CONNECTED With Station W7AW") {
		t.Errorf("local-initiated: unexpected data: %q", f.Data)
	}
	if f.CallFrom != "W7AW" || f.CallTo != "N7GET" {
		t.Errorf("callsigns: from=%q to=%q", f.CallFrom, f.CallTo)
	}

	// Remote initiated.
	f = BuildConnectedResp(0, "N7GET", "W7AW", false)
	if !strings.Contains(string(f.Data), "*** CONNECTED To Station W7AW") {
		t.Errorf("remote-initiated: unexpected data: %q", f.Data)
	}
}

func TestBuildDisconnectedResp(t *testing.T) {
	f := BuildDisconnectedResp(0, "N7GET", "W7AW")
	if !strings.Contains(string(f.Data), "*** DISCONNECTED From Station W7AW") {
		t.Errorf("unexpected data: %q", f.Data)
	}
}

func TestBuildDisconnectedTimeoutResp(t *testing.T) {
	f := BuildDisconnectedTimeoutResp(0, "N7GET", "W7AW")
	if !strings.Contains(string(f.Data), "*** DISCONNECTED RETRYOUT With W7AW") {
		t.Errorf("unexpected data: %q", f.Data)
	}
}

func TestFromAX25MonitorUI(t *testing.T) {
	src, _ := ax25.ParseAddress("N7GET")
	dst, _ := ax25.ParseAddress("BEACON")
	frame := &ax25.Frame{
		Source:      src,
		Destination: dst,
		Type:        ax25.FrameUI,
		Control:     ax25.CtrlUI,
		PID:         ax25.PIDNone,
		Payload:     []byte("test payload"),
	}

	f, err := FromAX25Monitor(frame, 0)
	if err != nil {
		t.Fatalf("FromAX25Monitor: %v", err)
	}
	if f.Kind != 'U' {
		t.Errorf("kind: expected 'U', got '%c'", f.Kind)
	}
	data := string(f.Data)
	if !strings.Contains(data, "1:Fm N7GET To BEACON") {
		t.Errorf("missing address header in: %q", data)
	}
	if !strings.Contains(data, "<UI") {
		t.Errorf("missing UI descriptor in: %q", data)
	}
	if !strings.Contains(data, "test payload") {
		t.Errorf("missing payload in: %q", data)
	}
	// Must end with \r\0
	if !bytes.HasSuffix(f.Data, []byte("\r\x00")) {
		t.Errorf("expected trailing \\r\\0, got last 2 bytes: %02x %02x", f.Data[len(f.Data)-2], f.Data[len(f.Data)-1])
	}
}

func TestFromAX25MonitorWithDigis(t *testing.T) {
	src, _ := ax25.ParseAddress("N7GET")
	dst, _ := ax25.ParseAddress("BEACON")
	digi, _ := ax25.ParseAddress("WIDE1-1")
	digi.HasBeenRepeated = true
	frame := &ax25.Frame{
		Source:      src,
		Destination: dst,
		Digipeaters: []ax25.Address{digi},
		Type:        ax25.FrameUI,
		Control:     ax25.CtrlUI,
		PID:         ax25.PIDNone,
		Payload:     []byte("test"),
	}

	f, err := FromAX25Monitor(frame, 0)
	if err != nil {
		t.Fatalf("FromAX25Monitor: %v", err)
	}
	data := string(f.Data)
	if !strings.Contains(data, "Via WIDE1-1*") {
		t.Errorf("missing digi with * in: %q", data)
	}
}

func TestFormatMonitorDescSupervisory(t *testing.T) {
	frame := &ax25.Frame{
		Control: ax25.BuildRRControl(3, true),
		Type:    ax25.FrameS,
	}
	kind, desc := FormatMonitorDesc(frame)
	if kind != 'S' {
		t.Errorf("kind: expected 'S', got '%c'", kind)
	}
	if !strings.Contains(desc, "RR") {
		t.Errorf("expected RR in desc: %q", desc)
	}
}
