package phy_test

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/n7get/go-ax25/ax25"
	"github.com/n7get/go-ax25/phy"
)

func freePort(t *testing.T) uint16 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return uint16(port)
}

func TestKISSTCPServerPHY_ClientSendsFrame(t *testing.T) {
	rxCh := make(chan *ax25.Frame, 4)
	port := freePort(t)

	srv, err := phy.NewKISSTCPServerPHY(phy.KISSTCPServerConfig{
		Port: port,
		OnRxFrame: func(_ *phy.KISSTCPServerConn, f *ax25.Frame) {
			rxCh <- f
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	frame := makeUIFramePHY("N0CALL-2", "TEST")
	raw, _ := frame.Encode()
	kissed := ax25.KISSEncode(0, 0, raw)
	_, _ = conn.Write(kissed)

	select {
	case f := <-rxCh:
		if f.Source.String() != "N0CALL-2" {
			t.Errorf("source = %s, want N0CALL-2", f.Source.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for RX frame")
	}
}

func TestKISSTCPServerPHY_ServerSendsFrame(t *testing.T) {
	connCh := make(chan *phy.KISSTCPServerConn, 1)
	port := freePort(t)

	srv, err := phy.NewKISSTCPServerPHY(phy.KISSTCPServerConfig{
		Port: port,
		OnConnected: func(c *phy.KISSTCPServerConn) {
			connCh <- c
		},
		OnRxFrame: func(_ *phy.KISSTCPServerConn, _ *ax25.Frame) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var srvConn *phy.KISSTCPServerConn
	select {
	case srvConn = <-connCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for connection")
	}

	frame := makeUIFramePHY("N0CALL-3", "DEST")
	if err := srvConn.Send(frame); err != nil {
		t.Fatalf("Send: %v", err)
	}

	buf := make([]byte, 512)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		t.Fatalf("client read: n=%d err=%v", n, err)
	}
	if srvConn.RemoteAddr() == nil {
		t.Fatal("RemoteAddr() should not be nil")
	}
	if err := srvConn.Send(nil); err == nil {
		t.Fatal("expected error for nil frame")
	}
}

func TestKISSTCPServerPHY_DisconnectCallback(t *testing.T) {
	connectedCh := make(chan *phy.KISSTCPServerConn, 1)
	disconnectedCh := make(chan *phy.KISSTCPServerConn, 1)
	port := freePort(t)

	srv, err := phy.NewKISSTCPServerPHY(phy.KISSTCPServerConfig{
		Port:           port,
		OnConnected:    func(c *phy.KISSTCPServerConn) { connectedCh <- c },
		OnDisconnected: func(c *phy.KISSTCPServerConn) { disconnectedCh <- c },
		OnRxFrame:      func(_ *phy.KISSTCPServerConn, _ *ax25.Frame) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}

	var connected *phy.KISSTCPServerConn
	select {
	case connected = <-connectedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for OnConnected")
	}

	_ = conn.Close()

	select {
	case disconnected := <-disconnectedCh:
		if disconnected != connected {
			t.Fatal("OnDisconnected should reference same connection object")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for OnDisconnected")
	}
}

func TestKISSTCPServerPHY_InvalidConfig(t *testing.T) {
	_, err := phy.NewKISSTCPServerPHY(phy.KISSTCPServerConfig{Port: 0, OnRxFrame: func(*phy.KISSTCPServerConn, *ax25.Frame) {}})
	if err == nil {
		t.Error("expected error for port 0")
	}
	_, err = phy.NewKISSTCPServerPHY(phy.KISSTCPServerConfig{Port: 9999})
	if err == nil {
		t.Error("expected error for nil OnRxFrame")
	}
}
func TestNewKISSTCPServerConfigFromConfig_Defaults(t *testing.T) {
	cfg := ax25.NewConfig(nil)
	c := phy.NewKISSTCPServerConfigFromConfig(cfg)
	if c.Port != 8100 {
		t.Errorf("Port: got %d, want 8100", c.Port)
	}
	if c.TXQueueDepth != 8 {
		t.Errorf("TXQueueDepth: got %d, want 8", c.TXQueueDepth)
	}
	if c.ReadBufSize != 4096 {
		t.Errorf("ReadBufSize: got %d, want 4096", c.ReadBufSize)
	}
}

func TestNewKISSTCPServerConfigFromConfig_Override(t *testing.T) {
	cfg := ax25.NewConfig(nil)
	cfg.Set("kiss.server.port", "9200")
	cfg.Set("kiss.server.tx_queue_depth", "32")
	c := phy.NewKISSTCPServerConfigFromConfig(cfg)
	if c.Port != 9200 {
		t.Errorf("Port: got %d, want 9200", c.Port)
	}
	if c.TXQueueDepth != 32 {
		t.Errorf("TXQueueDepth: got %d, want 32", c.TXQueueDepth)
	}
}
