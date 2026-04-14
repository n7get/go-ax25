package agwpe_test

import (
	"net"
	"testing"
	"time"

	"github.com/n7get/go-ax25/agwpe"
	"github.com/n7get/go-ax25/ax25"
)

func TestTCPServerStartStop(t *testing.T) {
	router := ax25.NewRouter()
	srv := agwpe.NewTCPServer(agwpe.TCPServerConfig{
		Addr:         "127.0.0.1:0",
		ServerConfig: agwpe.ServerConfig{Port: 0},
	}, router)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	srv.Stop()
}

func TestTCPServerClientConnect(t *testing.T) {
	router := ax25.NewRouter()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	srv := agwpe.NewTCPServer(agwpe.TCPServerConfig{
		Addr:         addr,
		ServerConfig: agwpe.ServerConfig{Port: 0},
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
	router := ax25.NewRouter()
	srv := agwpe.NewTCPServer(agwpe.TCPServerConfig{
		Addr:         "256.256.256.256:99999",
		ServerConfig: agwpe.ServerConfig{Port: 0},
	}, router)
	if err := srv.Start(); err == nil {
		t.Fatal("expected error for invalid addr")
	}
}
