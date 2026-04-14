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
	// ServerConfig is the per-connection AGWPE server configuration.
	ServerConfig ServerConfig
}

// TCPServer listens for AGWPE TCP clients and spawns a Server per connection.
type TCPServer struct {
	cfg    TCPServerConfig
	router *ax25.Router

	mu       sync.Mutex
	listener net.Listener
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
		go func(c net.Conn) {
			srv := NewServer(t.cfg.ServerConfig, t.router)
			srv.HandleConn(c)
			c.Close()
		}(conn)
	}
}
