package agwpe_test

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/n7get/go-ax25/agwpe"
	"github.com/n7get/go-ax25/ax25"
)

func TestTCPServerStartStop(t *testing.T) {
	router := ax25.NewRouter(nil)
	srv := agwpe.NewTCPServer(agwpe.TCPServerConfig{
		Addr:         "127.0.0.1:0",
		ServerConfig: agwpe.ServerConfig{},
	}, router)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	srv.Stop()
}

func TestTCPServerClientConnect(t *testing.T) {
	router := ax25.NewRouter(nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	srv := agwpe.NewTCPServer(agwpe.TCPServerConfig{
		Addr:         addr,
		ServerConfig: agwpe.ServerConfig{},
	}, router)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop()

	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	f := agwpe.Frame{Kind: 'R'}
	b, _ := f.Encode()
	_, _ = conn.Write(b)

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp, err := agwpe.ReadFrame(conn)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if resp.Kind != 'R' {
		t.Fatalf("expected 'R', got %q", string(resp.Kind))
	}
}

func TestTCPServerInvalidAddr(t *testing.T) {
	router := ax25.NewRouter(nil)
	srv := agwpe.NewTCPServer(agwpe.TCPServerConfig{
		Addr:         "256.256.256.256:99999",
		ServerConfig: agwpe.ServerConfig{},
	}, router)
	if err := srv.Start(); err == nil {
		t.Fatal("expected error for invalid addr")
	}
}

func TestTCPServerNegativeMaxClientsRejected(t *testing.T) {
	router := ax25.NewRouter(nil)
	srv := agwpe.NewTCPServer(agwpe.TCPServerConfig{
		Addr:         "127.0.0.1:0",
		MaxClients:   -1,
		ServerConfig: agwpe.ServerConfig{},
	}, router)
	if err := srv.Start(); err == nil {
		t.Fatal("expected error for negative MaxClients")
	}
}

func TestTCPServerMaxClientsLimit(t *testing.T) {
	router := ax25.NewRouter(nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	srv := agwpe.NewTCPServer(agwpe.TCPServerConfig{
		Addr:         addr,
		MaxClients:   1,
		ServerConfig: agwpe.ServerConfig{},
	}, router)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop()

	conn1, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("dial conn1: %v", err)
	}
	defer conn1.Close()

	conn2, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("dial conn2: %v", err)
	}
	defer conn2.Close()

	if err := waitUntilClosed(conn2, 2*time.Second); err != nil {
		t.Fatalf("conn2 should be closed at capacity: %v", err)
	}
}

func TestTCPServerMaxClientsUnlimitedWhenZero(t *testing.T) {
	router := ax25.NewRouter(nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	srv := agwpe.NewTCPServer(agwpe.TCPServerConfig{
		Addr:         addr,
		MaxClients:   0,
		ServerConfig: agwpe.ServerConfig{},
	}, router)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop()

	conns := make([]net.Conn, 0, 3)
	for i := 0; i < 3; i++ {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			t.Fatalf("dial conn[%d]: %v", i, err)
		}
		conns = append(conns, conn)
		defer conn.Close()
	}

	f := agwpe.Frame{Kind: 'R'}
	b, _ := f.Encode()
	for i, conn := range conns {
		if _, err := conn.Write(b); err != nil {
			t.Fatalf("write conn[%d]: %v", i, err)
		}
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		resp, err := agwpe.ReadFrame(conn)
		if err != nil {
			t.Fatalf("ReadFrame conn[%d]: %v", i, err)
		}
		if resp.Kind != 'R' {
			t.Fatalf("conn[%d] expected 'R', got %q", i, string(resp.Kind))
		}
	}
}

func waitUntilClosed(conn net.Conn, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	buf := make([]byte, 1)
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		_, err := conn.Read(buf)
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			continue
		}
		return nil
	}
	return errors.New("timeout waiting for connection close")
}
