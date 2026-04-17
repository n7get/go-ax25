package agwpe_test

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/n7get/go-ax25/agwpe"
	"github.com/n7get/go-ax25/ax25"
)

func startAGWPEServer(t *testing.T, frames []*agwpe.Frame) (host string, port uint16, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		for _, f := range frames {
			enc, _ := f.Encode()
			_, _ = conn.Write(enc)
		}
		buf := make([]byte, 4096)
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		conn.Read(buf)
	}()
	h, p, _ := net.SplitHostPort(ln.Addr().String())
	var portNum uint16
	fmt.Sscanf(p, "%d", &portNum)
	return h, portNum, func() { ln.Close() }
}

func TestClient_ReceivesFrame(t *testing.T) {
	rxCh := make(chan *agwpe.Frame, 4)

	f := agwpe.BuildVersionReq()
	f.Data = []byte{0xD5, 0x07, 0x00, 0x00}

	host, port, stop := startAGWPEServer(t, []*agwpe.Frame{f})
	defer stop()

	client, err := agwpe.NewClient(agwpe.ClientConfig{
		Host:           host,
		Port:           port,
		ConnectTimeout: time.Second,
		ReconnectDelay: 100 * time.Millisecond,
		OnRxFrame: func(f *agwpe.Frame) {
			rxCh <- f
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	client.Start()
	defer client.Stop()

	select {
	case got := <-rxCh:
		if got.Kind != agwpe.KindVersionResp {
			t.Errorf("Kind = %c, want R", got.Kind)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for RX frame")
	}
}

func TestClient_SendUnproto(t *testing.T) {
	recvCh := make(chan []byte, 4)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _ := conn.Read(buf)
		if n > 0 {
			cp := make([]byte, n)
			copy(cp, buf[:n])
			recvCh <- cp
		}
	}()

	h, p, _ := net.SplitHostPort(ln.Addr().String())
	var portNum uint16
	fmt.Sscanf(p, "%d", &portNum)

	client, err := agwpe.NewClient(agwpe.ClientConfig{
		Host:           h,
		Port:           portNum,
		ConnectTimeout: time.Second,
		ReconnectDelay: 100 * time.Millisecond,
		OnRxFrame:      func(*agwpe.Frame) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	client.Start()
	defer client.Stop()

	time.Sleep(200 * time.Millisecond)

	src, _ := ax25.ParseAddress("N0CALL-1")
	dst, _ := ax25.ParseAddress("APRS")
	frame := &ax25.Frame{
		Source:      src,
		Destination: dst,
		Control:     ax25.CtrlUI,
		PID:         ax25.PIDNone,
		Payload:     []byte("beacon"),
	}
	if err := client.SendUnproto(frame, 0); err != nil {
		t.Fatalf("SendUnproto: %v", err)
	}

	select {
	case data := <-recvCh:
		if len(data) < agwpe.HeaderSize {
			t.Errorf("received %d bytes, want >= %d", len(data), agwpe.HeaderSize)
		}
		if data[4] != agwpe.KindSendUnproto {
			t.Errorf("Kind = %c, want M", data[4])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for TX data")
	}
}

func TestClient_InvalidConfig(t *testing.T) {
	_, err := agwpe.NewClient(agwpe.ClientConfig{})
	if err == nil {
		t.Error("expected error for empty config")
	}
}

func TestClient_SendNotConnected(t *testing.T) {
	client, err := agwpe.NewClient(agwpe.ClientConfig{
		Host:      "127.0.0.1",
		Port:      19998,
		OnRxFrame: func(*agwpe.Frame) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = client.SendFrame(agwpe.BuildVersionReq())
	if err != ax25.ErrNotConnected {
		t.Errorf("expected ErrNotConnected, got %v", err)
	}
}
func TestNewClientConfigFromConfig_Defaults(t *testing.T) {
	cfg := ax25.NewConfig(nil)
	c := agwpe.NewClientConfigFromConfig(cfg)
	if c.Host != "localhost" {
		t.Errorf("Host: got %q, want \"localhost\"", c.Host)
	}
	if c.Port != 8000 {
		t.Errorf("Port: got %d, want 8000", c.Port)
	}
	if c.TXQueueDepth != 8 {
		t.Errorf("TXQueueDepth: got %d, want 8", c.TXQueueDepth)
	}
	if c.ReadBufSize != 4132 {
		t.Errorf("ReadBufSize: got %d, want 4132", c.ReadBufSize)
	}
}

func TestNewClientConfigFromConfig_Override(t *testing.T) {
	cfg := ax25.NewConfig(nil)
	cfg.Set("agwpe.client.host", "10.0.0.1")
	cfg.Set("agwpe.client.port", "9000")
	cfg.Set("agwpe.client.tx_queue_depth", "16")
	c := agwpe.NewClientConfigFromConfig(cfg)
	if c.Host != "10.0.0.1" {
		t.Errorf("Host: got %q, want \"10.0.0.1\"", c.Host)
	}
	if c.Port != 9000 {
		t.Errorf("Port: got %d, want 9000", c.Port)
	}
	if c.TXQueueDepth != 16 {
		t.Errorf("TXQueueDepth: got %d, want 16", c.TXQueueDepth)
	}
}
