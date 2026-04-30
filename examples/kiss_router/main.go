package main

// AX.25 KISS Router — bridges a KISS serial (or TCP) TNC with a TCP KISS
// server so that multiple network clients can share one radio.
//
// Architecture:
//   - UART/serial KISS PHY registered as the DEFAULT router port
//   - TCP KISS server: each accepted client gets a DYNAMIC router port
//   - The router forwards frames between all ports (never looping back
//     to the source port)
//
// Usage:
//   go run ./examples/kiss_router \
//       -kiss-serial /dev/ttyUSB0 -baud 57600 \
//       -listen :8100

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/n7get/go-ax25/ax25"
	"github.com/n7get/go-ax25/phy"
	"go.bug.st/serial"
)

// ── TCP client slot ──────────────────────────────────────────────────────────

// tcpClient represents one connected KISS-over-TCP client.
type tcpClient struct {
	conn net.Conn
	port *ax25.Port
	phy  ax25.PHY
}

// tcpPool manages the set of active TCP client ports.
type tcpPool struct {
	mu      sync.Mutex
	clients map[net.Conn]*tcpClient
	router  *ax25.Router
	maxConn int
}

func newTCPPool(router *ax25.Router, maxConn int) *tcpPool {
	return &tcpPool{
		clients: make(map[net.Conn]*tcpClient),
		router:  router,
		maxConn: maxConn,
	}
}

func (p *tcpPool) add(conn net.Conn) error {
	p.mu.Lock()
	if len(p.clients) >= p.maxConn {
		p.mu.Unlock()
		conn.Close()
		return fmt.Errorf("max TCP clients reached (%d)", p.maxConn)
	}
	p.mu.Unlock()

	phy := ax25.NewKISSSerialPHY(conn, ax25.KISSSerialPHYConfig{})

	port := &ax25.Port{
		Mode: ax25.PortModeDynamic,
		OnRxFrame: func(frame *ax25.Frame) {
			if err := phy.SendFrame(frame); err != nil {
				slog.Error("ROUTER: TCP client send error", "err", err)
			}
		},
	}

	if err := p.router.RegisterPort(port); err != nil {
		conn.Close()
		return fmt.Errorf("register dynamic port: %w", err)
	}

	client := &tcpClient{conn: conn, port: port, phy: phy}

	p.mu.Lock()
	p.clients[conn] = client
	p.mu.Unlock()

	// Start the PHY; received frames are injected into the router.
	// When the TCP connection drops, rxLoop exits and closes RxFrames,
	// which causes the goroutine below to call pool.remove().
	phyCtx, phyCancel := context.WithCancel(context.Background())
	_ = phyCancel // cancelled when phy.Stop() is called
	phy.Start(phyCtx)
	go func() {
		for frame := range phy.RxFrames() {
			p.router.Send(frame, port)
		}
		// RxFrames closed: connection dropped or PHY stopped.
		p.remove(conn)
	}()

	slog.Info("ROUTER: TCP client connected", "addr", conn.RemoteAddr(), "mode", "dynamic")
	return nil
}

func (p *tcpPool) remove(conn net.Conn) {
	p.mu.Lock()
	client, ok := p.clients[conn]
	if ok {
		delete(p.clients, conn)
	}
	p.mu.Unlock()

	if !ok {
		return
	}

	client.phy.Stop()
	p.router.UnregisterPort(client.port)
	client.conn.Close()
	slog.Info("ROUTER: TCP client disconnected", "addr", conn.RemoteAddr())
}

func (p *tcpPool) stopAll() {
	p.mu.Lock()
	conns := make([]net.Conn, 0, len(p.clients))
	for c := range p.clients {
		conns = append(conns, c)
	}
	p.mu.Unlock()

	for _, c := range conns {
		p.remove(c)
	}
}

// ── TCP KISS server ──────────────────────────────────────────────────────────

func serveTCP(ln net.Listener, pool *tcpPool) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			// listener closed
			return
		}
		if err := pool.add(conn); err != nil {
			slog.Warn("ROUTER: reject TCP client", "addr", conn.RemoteAddr(), "err", err)
			continue
		}
		// Disconnection is detected inside pool.add via the PHY RxFrames close.
	}
}

// ── main ─────────────────────────────────────────────────────────────────────

func main() {
	cfgPath := flag.String("config", "ax25.ini", "INI config file")
	debug := flag.Bool("debug", false, "enable debug logging")
	flag.Parse()

	if *debug {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}

	cfg := ax25.NewConfig(nil)
	cfg.Set(ax25.KeyKissSerialBaud, "57600") // router default; overridden by config file
	if err := cfg.LoadINI(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "error: failed to load config:", err)
		os.Exit(2)
	}

	serialPort := cfg.GetStr(ax25.KeyKissSerialDevice)
	serialBaud := cfg.GetInt(ax25.KeyKissSerialBaud)
	listenAddr := cfg.GetStr(ax25.KeyKissServerAddr)
	maxClients := cfg.GetInt(ax25.KeyKissServerMaxClients)

	slog.Info("ROUTER: ESP-AX25 KISS Router (Go)")

	// ── build upstream PHY ──
	sp, err := openSerial(serialPort, serialBaud)
	if err != nil {
		slog.Error("open serial", "device", serialPort, "err", err)
		os.Exit(1)
	}
	kissCfg := phy.NewKISSSerialConfigFromConfig(cfg)
	kissCfg.Port = sp
	uartPHY, err := phy.NewKISSSerialPHY(kissCfg)
	if err != nil {
		slog.Error("create KISS serial PHY", "err", err)
		os.Exit(1)
	}

	// ── build router ──
	router := ax25.NewRouter(nil)

	// UART port: default — any frame without a more specific destination
	// goes out to the radio.
	uartPort := &ax25.Port{
		Mode: ax25.PortModeDefault,
		OnRxFrame: func(frame *ax25.Frame) {
			if err := uartPHY.SendFrame(frame); err != nil {
				slog.Error("ROUTER: UART send error", "err", err)
			}
		},
	}
	if err := router.RegisterPort(uartPort); err != nil {
		slog.Error("register UART port", "err", err)
		os.Exit(1)
	}

	// Start UART PHY; received frames enter the router from uartPort.
	uartCtx, uartCancel := context.WithCancel(context.Background())
	defer uartCancel()
	if err := uartPHY.Start(uartCtx); err != nil {
		slog.Error("start UART PHY", "err", err)
		os.Exit(1)
	}
	go func() {
		for frame := range uartPHY.RxFrames() {
			router.Send(frame, uartPort)
		}
	}()
	slog.Info("ROUTER: UART PHY started", "mode", "default")

	// ── TCP KISS server ──
	pool := newTCPPool(router, maxClients)

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		slog.Error("listen", "addr", listenAddr, "err", err)
		os.Exit(1)
	}
	slog.Info("ROUTER: TCP KISS server listening", "addr", listenAddr, "max_clients", maxClients)
	go serveTCP(ln, pool)

	slog.Info("ROUTER: running")

	// ── wait for signal ──
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	slog.Info("ROUTER: shutting down")
	ln.Close()
	pool.stopAll()
	uartPHY.Stop()
}

// ── serial helper ────────────────────────────────────────────────────────────

func openSerial(device string, baud int) (io.ReadWriter, error) {
	return serial.Open(device, &serial.Mode{BaudRate: baud})
}
