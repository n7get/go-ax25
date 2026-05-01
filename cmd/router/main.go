// router — combines a KISS TCP server and an AGWPE TCP server, bridged
// to a configurable uplink PHY.
//
// Architecture:
//   - One optional DEFAULT port: either a serial KISS PHY or a KISS TCP client
//     (mutually exclusive; both disabled by default)
//   - TCP KISS server: each accepted client gets a DYNAMIC router port by
//     default, or PROMISCUOUS when kiss.server.promiscuous=true
//   - AGWPE TCP server: connected to the same router
//   - The router forwards frames between all ports
//
// Usage:
//
//	router [-config ax25.ini] [-debug]
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
	mode    ax25.PortMode
}

func newTCPPool(router *ax25.Router, maxConn int, mode ax25.PortMode) *tcpPool {
	return &tcpPool{
		clients: make(map[net.Conn]*tcpClient),
		router:  router,
		maxConn: maxConn,
		mode:    mode,
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
		Mode: p.mode,
		OnRxFrame: func(frame *ax25.Frame) {
			if err := kissPHY.SendFrame(frame); err != nil {
				slog.Error("KISS TCP: send error", "err", err)
			}
		},
	}

	if err := p.router.RegisterPort(port); err != nil {
		conn.Close()
		return fmt.Errorf("register KISS TCP server port: %w", err)
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
	cfg.Set(ax25.KeyKissSerialBaud, "57600") // router default; overridden by config file
	if err := cfg.LoadINI(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "error: failed to load config:", err)
		os.Exit(2)
	}

	// Uplink PHY selection (mutually exclusive)
	kissSerialEnabled := cfg.GetBool(ax25.KeyKissSerialEnabled)
	kissClientEnabled := cfg.GetBool(ax25.KeyKissClientEnabled)
	if kissSerialEnabled && kissClientEnabled {
		log.Fatal("config error: kiss.serial.enabled and kiss.client.enabled cannot both be true")
	}

	// Serial KISS TNC
	serialDevice := cfg.GetStr(ax25.KeyKissSerialDevice)
	serialBaud := cfg.GetInt(ax25.KeyKissSerialBaud)

	// KISS TCP client
	kissClientHost := cfg.GetStr(ax25.KeyKissClientHost)
	kissClientPort := cfg.GetInt(ax25.KeyKissClientPort)

	// TCP KISS server
	kissServerEnabled := cfg.GetBool(ax25.KeyKissServerEnabled)
	kissListenAddr := cfg.GetStr(ax25.KeyKissServerAddr)
	kissMaxClients := cfg.GetInt(ax25.KeyKissServerMaxClients)
	kissServerPromiscuous := cfg.GetBool(ax25.KeyKissServerPromiscuous)

	routerMode := ax25.RouterModeFromConfig(cfg)
	if kissServerEnabled && kissServerPromiscuous && *routerMode == ax25.RouterModeBridge {
		log.Fatal("config error: kiss.server.promiscuous is not supported in bridge mode")
	}

	// AGWPE TCP server
	agwpeEnabled := cfg.GetBool(ax25.KeyAgwpeServerEnabled)
	agwpePort := cfg.GetInt(ax25.KeyAgwpeServerPort)

	slog.Info("router starting",
		"serial_enabled", kissSerialEnabled,
		"serial_device", serialDevice,
		"serial_baud", serialBaud,
		"kiss_client_enabled", kissClientEnabled,
		"kiss_client_host", kissClientHost,
		"kiss_client_port", kissClientPort,
		"kiss_server_enabled", kissServerEnabled,
		"kiss_server_addr", kissListenAddr,
		"kiss_server_promiscuous", kissServerPromiscuous,
		"agwpe_enabled", agwpeEnabled,
		"agwpe_port", agwpePort,
	)

	// ── router ──
	router := ax25.NewRouter(routerMode)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ── serial KISS PHY (optional default port) ──
	if kissSerialEnabled {
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
	}

	// ── KISS TCP client PHY (optional default port) ──
	if kissClientEnabled {
		tcpClientPort := &ax25.Port{Mode: ax25.PortModeDefault}

		kissClientCfg := phy.NewKISSTCPClientConfigFromConfig(cfg)
		kissClientCfg.OnRxFrame = func(f *ax25.Frame) {
			if err := router.Send(f, tcpClientPort); err != nil {
				log.Printf("kiss client rx: %v", err)
			}
		}

		tcpPHY, err := phy.NewKISSTCPClientPHY(kissClientCfg)
		if err != nil {
			log.Fatalf("create KISS TCP client PHY: %v", err)
		}

		tcpClientPort.OnRxFrame = func(f *ax25.Frame) {
			if err := tcpPHY.Send(f); err != nil {
				log.Printf("kiss client tx: %v", err)
			}
		}

		if err := router.RegisterPort(tcpClientPort); err != nil {
			log.Fatalf("register KISS TCP client port: %v", err)
		}

		tcpPHY.Start()
		defer tcpPHY.Stop()
		slog.Info("KISS TCP client PHY started", "host", kissClientHost, "port", kissClientPort)
	}

	// ── TCP KISS server ──
	var kissListener net.Listener
	var pool *tcpPool
	if kissServerEnabled {
		var err error
		kissListener, err = net.Listen("tcp", kissListenAddr)
		if err != nil {
			log.Fatalf("listen KISS TCP %s: %v", kissListenAddr, err)
		}
		kissServerPortMode := ax25.PortModeDynamic
		if kissServerPromiscuous {
			kissServerPortMode = ax25.PortModePromiscuous
		}
		pool = newTCPPool(router, kissMaxClients, kissServerPortMode)
		go serveTCPKISS(kissListener, pool)
		slog.Info("KISS TCP server listening", "addr", kissListenAddr, "max_clients", kissMaxClients, "promiscuous", kissServerPromiscuous)
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

	slog.Info("router running — press Ctrl+C to stop")

	// ── wait for signal ──
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	slog.Info("router shutting down")
	cancel()
	if kissListener != nil {
		kissListener.Close()
	}
	if pool != nil {
		pool.stopAll()
	}
}
