// ax25-router — combines a KISS TCP server and an AGWPE TCP server, both
// bridged to a single serial KISS TNC.
//
// Architecture:
//   - Serial KISS PHY registered as the DEFAULT router port
//   - TCP KISS server: each accepted client gets a DYNAMIC router port
//   - AGWPE TCP server: connected to the same router
//   - The router forwards frames between all ports
//
// Usage:
//
//	ax25-router [-config ax25-router.ini] [-debug]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/n7get/go-ax25/agwpe"
	"github.com/n7get/go-ax25/ax25"
	"github.com/n7get/go-ax25/phy"
	"go.bug.st/serial"
)

// ── TCP KISS client pool ─────────────────────────────────────────────────────

type tcpClient struct {
	conn net.Conn
	port *ax25.Port
	phy  ax25.PHY
}

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

	kissPHY := ax25.NewKISSSerialPHY(conn, ax25.KISSSerialPHYConfig{})

	port := &ax25.Port{
		Mode: ax25.PortModeDynamic,
		OnRxFrame: func(frame *ax25.Frame) {
			if err := kissPHY.SendFrame(frame); err != nil {
				slog.Error("KISS TCP: send error", "err", err)
			}
		},
	}

	if err := p.router.RegisterPort(port); err != nil {
		conn.Close()
		return fmt.Errorf("register dynamic port: %w", err)
	}

	client := &tcpClient{conn: conn, port: port, phy: kissPHY}

	p.mu.Lock()
	p.clients[conn] = client
	p.mu.Unlock()

	phyCtx, phyCancel := context.WithCancel(context.Background())
	_ = phyCancel
	kissPHY.Start(phyCtx)
	go func() {
		for frame := range kissPHY.RxFrames() {
			p.router.Send(frame, port)
		}
		p.remove(conn)
	}()

	slog.Info("KISS TCP: client connected", "addr", conn.RemoteAddr())
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
	slog.Info("KISS TCP: client disconnected", "addr", conn.RemoteAddr())
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

func serveTCPKISS(ln net.Listener, pool *tcpPool) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		if err := pool.add(conn); err != nil {
			slog.Warn("KISS TCP: rejected client", "addr", conn.RemoteAddr(), "err", err)
		}
	}
}

// ── main ─────────────────────────────────────────────────────────────────────

func main() {
	cfgPath := flag.String("config", "ax25.ini", "path to INI config file")
	debug := flag.Bool("debug", false, "enable debug logging")
	flag.Parse()

	logLevel := slog.LevelInfo
	if *debug {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	cfg := ax25.NewConfig(nil)
	if err := cfg.LoadINI(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "error: failed to load config:", err)
		os.Exit(2)
	}

	// Serial KISS TNC
	serialDevice := cfg.GetStr("kiss.serial.device", "/dev/ttyUSB0")
	serialBaud := cfg.GetInt("kiss.serial.baud", 57600)

	// TCP KISS server
	kissServerEnabled := cfg.GetBool("kiss.server.enabled", true)
	kissListenAddr := cfg.GetStr("kiss.server.addr", ":8100")
	kissMaxClients := cfg.GetInt("kiss.server.max_clients", 8)

	// AGWPE TCP server
	agwpeEnabled := cfg.GetBool("agwpe.server.enabled", true)
	agwpePort := cfg.GetInt("agwpe.server.port", 8000)

	slog.Info("ax25-router starting",
		"serial", serialDevice,
		"baud", serialBaud,
		"kiss_server", kissListenAddr,
		"kiss_enabled", kissServerEnabled,
		"agwpe_port", agwpePort,
		"agwpe_enabled", agwpeEnabled,
	)

	// ── serial PHY ──
	sp, err := serial.Open(serialDevice, &serial.Mode{BaudRate: serialBaud})
	if err != nil {
		log.Fatalf("open serial port %s: %v", serialDevice, err)
	}
	defer sp.Close()

	kissCfg := phy.NewKISSSerialConfigFromConfig(cfg)
	kissCfg.Port = sp
	serialPHY, err := phy.NewKISSSerialPHY(kissCfg)
	if err != nil {
		log.Fatalf("create serial PHY: %v", err)
	}

	// ── router ──
	router := ax25.NewRouter()

	serialPort := &ax25.Port{
		Mode: ax25.PortModeDefault,
		OnRxFrame: func(f *ax25.Frame) {
			slog.Debug("serial tx", "frame", f)
			if err := serialPHY.SendFrame(f); err != nil {
				log.Printf("serial send: %v", err)
			}
		},
	}
	if err := router.RegisterPort(serialPort); err != nil {
		log.Fatalf("register serial port: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := serialPHY.Start(ctx); err != nil {
		log.Fatalf("start serial PHY: %v", err)
	}
	defer serialPHY.Stop()

	go func() {
		for f := range serialPHY.RxFrames() {
			slog.Debug("serial rx", "frame", f)
			if err := router.Send(f, serialPort); err != nil {
				log.Printf("router rx: %v", err)
			}
		}
	}()
	slog.Info("serial KISS PHY started", "device", serialDevice, "baud", serialBaud)

	// ── TCP KISS server ──
	var kissListener net.Listener
	var pool *tcpPool
	if kissServerEnabled {
		kissListener, err = net.Listen("tcp", kissListenAddr)
		if err != nil {
			log.Fatalf("listen KISS TCP %s: %v", kissListenAddr, err)
		}
		pool = newTCPPool(router, kissMaxClients)
		go serveTCPKISS(kissListener, pool)
		slog.Info("KISS TCP server listening", "addr", kissListenAddr, "max_clients", kissMaxClients)
	}

	// ── AGWPE server ──
	var agwpeSrv *agwpe.TCPServer
	if agwpeEnabled {
		agwpeSrv = agwpe.NewTCPServer(agwpe.TCPServerConfig{
			Addr:         fmt.Sprintf(":%d", agwpePort),
			ServerConfig: agwpe.NewServerConfigFromConfig(cfg),
		}, router)
		if err := agwpeSrv.Start(); err != nil {
			log.Fatalf("start AGWPE server: %v", err)
		}
		defer agwpeSrv.Stop()
		slog.Info("AGWPE TCP server listening", "port", agwpePort)
	}

	slog.Info("ax25-router running — press Ctrl+C to stop")

	// ── wait for signal ──
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	slog.Info("ax25-router shutting down")
	cancel()
	if kissListener != nil {
		kissListener.Close()
	}
	if pool != nil {
		pool.stopAll()
	}
}
