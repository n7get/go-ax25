// tcp_server.go — TCP listener that creates an AGWPE Server per connection.
package agwpe

import (
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/n7get/go-ax25/ax25"
)

// TCPServerConfig configures the AGWPE TCP server.
type TCPServerConfig struct {
	// Addr is the TCP listen address, e.g. ":8000".
	Addr string
	// MaxClients is the maximum concurrent AGWPE TCP clients (0 = unlimited).
	MaxClients int
	// ServerConfig is the per-connection AGWPE server configuration.
	ServerConfig ServerConfig
}

// TCPServer listens for AGWPE TCP clients and spawns a Server per connection.
type TCPServer struct {
	cfg    TCPServerConfig
	router *ax25.Router

	mu       sync.Mutex
	listener net.Listener
	clients  int
	done     chan struct{}
}

// NewTCPServer creates a new AGWPE TCP server.  Call Start to begin accepting.
func NewTCPServer(cfg TCPServerConfig, router *ax25.Router) *TCPServer {
	return &TCPServer{
		cfg:    cfg,
		router: router,
		done:   make(chan struct{}),
	}
}

// Start begins listening for connections.  It returns immediately; connections
// are handled in background goroutines.
func (t *TCPServer) Start() error {
	if t.cfg.Addr == "" {
		return fmt.Errorf("agwpe TCPServer: Addr must not be empty")
	}
	if t.cfg.MaxClients < 0 {
		return fmt.Errorf("agwpe TCPServer: MaxClients must be >= 0 (0 = unlimited)")
	}
	ln, err := net.Listen("tcp", t.cfg.Addr)
	if err != nil {
		return fmt.Errorf("agwpe TCPServer: listen %s: %w", t.cfg.Addr, err)
	}
	t.mu.Lock()
	t.listener = ln
	t.mu.Unlock()

	go t.acceptLoop(ln)
	return nil
}

// Stop closes the listener and waits for the accept loop to exit.
func (t *TCPServer) Stop() {
	t.mu.Lock()
	ln := t.listener
	t.mu.Unlock()
	if ln != nil {
		ln.Close()
	}
	<-t.done
}

func (t *TCPServer) acceptLoop(ln net.Listener) {
	defer close(t.done)
	for {
		conn, err := ln.Accept()
		if err != nil {
			slog.Info("agwpe TCPServer: accept loop exiting", "err", err)
			return
		}
		if !t.acquireClientSlot() {
			slog.Warn("agwpe TCPServer: reject client at capacity", "addr", conn.RemoteAddr(), "max_clients", t.cfg.MaxClients)
			_ = conn.Close()
			continue
		}
		go func(c net.Conn) {
			defer t.releaseClientSlot()
			srv := NewServer(t.cfg.ServerConfig, t.router)
			srv.HandleConn(c)
			c.Close()
		}(conn)
	}
}

func (t *TCPServer) acquireClientSlot() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cfg.MaxClients > 0 && t.clients >= t.cfg.MaxClients {
		return false
	}
	t.clients++
	return true
}

func (t *TCPServer) releaseClientSlot() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.clients > 0 {
		t.clients--
	}
}
