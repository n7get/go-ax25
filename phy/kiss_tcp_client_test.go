package phy_test

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/n7get/go-ax25/ax25"
	"github.com/n7get/go-ax25/phy"
)

func makeUIFramePHY(src, dst string) *ax25.Frame {
	srcAddr, _ := ax25.ParseAddress(src)
	dstAddr, _ := ax25.ParseAddress(dst)
	return &ax25.Frame{
		Source:      srcAddr,
		Destination: dstAddr,
		Control:     ax25.CtrlUI,
		PID:         ax25.PIDNone,
		Payload:     []byte("hello"),
	}
}

func TestKISSTCPClientPHY_ReceivesFrame(t *testing.T) {
	rxCh := make(chan *ax25.Frame, 4)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	frame := makeUIFramePHY("N0CALL-1", "APRS")
	raw, _ := frame.Encode()
	kissed := ax25.KISSEncode(0, 0, raw)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write(kissed)
		buf := make([]byte, 4096)
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		conn.Read(buf)
	}()

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	var portNum uint16
	fmt.Sscanf(portStr, "%d", &portNum)

	p, err := phy.NewKISSTCPClientPHY(phy.KISSTCPClientConfig{
		Host:           host,
		Port:           portNum,
		ConnectTimeout: time.Second,
		ReconnectDelay: 100 * time.Millisecond,
		OnRxFrame: func(f *ax25.Frame) {
			rxCh <- f
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	p.Start()
	defer p.Stop()

	select {
	case f := <-rxCh:
		if f.Source.String() != "N0CALL-1" {
			t.Errorf("source = %s, want N0CALL-1", f.Source.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for RX frame")
	}
}

func TestKISSTCPClientPHY_InvalidConfig(t *testing.T) {
	_, err := phy.NewKISSTCPClientPHY(phy.KISSTCPClientConfig{})
	if err == nil {
		t.Error("expected error for empty config")
	}
	_, err = phy.NewKISSTCPClientPHY(phy.KISSTCPClientConfig{Host: "localhost", Port: 8001})
	if err == nil {
		t.Error("expected error for nil OnRxFrame")
	}
}

func TestKISSTCPClientPHY_SendNotConnected(t *testing.T) {
	p, err := phy.NewKISSTCPClientPHY(phy.KISSTCPClientConfig{
		Host:      "127.0.0.1",
		Port:      19999,
		OnRxFrame: func(*ax25.Frame) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = p.Send(makeUIFramePHY("A", "B"))
	if err != ax25.ErrNotConnected {
		t.Errorf("expected ErrNotConnected, got %v", err)
	}
}

func TestKISSTCPClientPHY_TXQueueFull(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	connCh := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		close(connCh)
		buf := make([]byte, 4096)
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		conn.Read(buf)
		conn.Close()
	}()

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	var portNum uint16
	fmt.Sscanf(portStr, "%d", &portNum)

	p, err := phy.NewKISSTCPClientPHY(phy.KISSTCPClientConfig{
		Host:           host,
		Port:           portNum,
		ConnectTimeout: time.Second,
		ReconnectDelay: 100 * time.Millisecond,
		TXQueueDepth:   1,
		OnRxFrame:      func(*ax25.Frame) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	p.Start()
	defer p.Stop()

	select {
	case <-connCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for connection")
	}
	time.Sleep(50 * time.Millisecond)

	// Fill the queue.
	_ = p.Send(makeUIFramePHY("A", "B"))
	_ = p.Send(makeUIFramePHY("A", "B"))
	err = p.Send(makeUIFramePHY("A", "B"))
	if err != ax25.ErrTXQueueFull {
		t.Errorf("expected ErrTXQueueFull, got %v", err)
	}
}
func TestNewKISSTCPClientConfigFromConfig_Defaults(t *testing.T) {
	cfg := ax25.NewConfig(nil)
	c := phy.NewKISSTCPClientConfigFromConfig(cfg)
	if c.Host != "localhost" {
		t.Errorf("Host: got %q, want \"localhost\"", c.Host)
	}
	if c.Port != 8001 {
		t.Errorf("Port: got %d, want 8001", c.Port)
	}
	if c.TXQueueDepth != 8 {
		t.Errorf("TXQueueDepth: got %d, want 8", c.TXQueueDepth)
	}
	if c.ReadBufSize != 4096 {
		t.Errorf("ReadBufSize: got %d, want 4096", c.ReadBufSize)
	}
}

func TestNewKISSTCPClientConfigFromConfig_Override(t *testing.T) {
	cfg := ax25.NewConfig(nil)
	cfg.Set("kiss.client.host", "192.168.1.50")
	cfg.Set("kiss.client.port", "8200")
	c := phy.NewKISSTCPClientConfigFromConfig(cfg)
	if c.Host != "192.168.1.50" {
		t.Errorf("Host: got %q, want \"192.168.1.50\"", c.Host)
	}
	if c.Port != 8200 {
		t.Errorf("Port: got %d, want 8200", c.Port)
	}
}
