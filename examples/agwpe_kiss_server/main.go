// agwpe_kiss_server — combined AGWPE + KISS TCP server with serial KISS TNC.
//
// Usage:
//
//	agwpe_kiss_server -config ax25.ini
//
// INI keys used:
//
//	[serial]
//	port  = /dev/ttyUSB0
//	baud  = 9600
package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/n7get/go-ax25/agwpe"
	"github.com/n7get/go-ax25/ax25"
	"github.com/n7get/go-ax25/phy"
	"go.bug.st/serial"
	ini "gopkg.in/ini.v1"
)

func main() {
	cfgPath := flag.String("config", "ax25.ini", "path to INI config file")
	flag.Parse()

	cfg, err := ini.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	serialPort := cfg.Section("serial").Key("port").MustString("/dev/ttyUSB0")
	serialBaud := cfg.Section("serial").Key("baud").MustInt(9600)
	agwpeAddr := ":" + cfg.Section("net").Key("agwpe_port").MustString("8000")
	kissPort := uint16(cfg.Section("net").Key("kiss_port").MustInt(8001))

	slog.Info("starting agwpe_kiss_server", "serial", serialPort, "baud", serialBaud, "agwpe", agwpeAddr, "kissPort", kissPort)

	router := ax25.NewRouter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serialRW, err := serial.Open(serialPort, &serial.Mode{BaudRate: serialBaud})
	if err != nil {
		log.Fatalf("open serial port: %v", err)
	}
	defer serialRW.Close()

	kissPHY, err := phy.NewKISSSerialPHY(phy.KISSSerialConfig{Port: serialRW})
	if err != nil {
		log.Fatalf("serial PHY: %v", err)
	}
	if err := kissPHY.Start(ctx); err != nil {
		log.Fatalf("start serial PHY: %v", err)
	}
	defer kissPHY.Stop()

	serialRouterPort := &ax25.Port{
		Mode: ax25.PortModeDefault,
		OnRxFrame: func(f *ax25.Frame) {
			if err := kissPHY.SendFrame(f); err != nil {
				log.Printf("serial send: %v", err)
			}
		},
	}
	if err := router.RegisterPort(serialRouterPort); err != nil {
		log.Fatalf("register serial port: %v", err)
	}

	go func() {
		for f := range kissPHY.RxFrames() {
			if err := router.Send(f, serialRouterPort); err != nil {
				log.Printf("router rx: %v", err)
			}
		}
	}()

	var (
		clientsMu sync.Mutex
		clients   = make(map[*phy.KISSTCPServerConn]struct{})
	)
	kissFanoutPort := &ax25.Port{
		Mode: ax25.PortModePromiscuous,
		OnRxFrame: func(f *ax25.Frame) {
			clientsMu.Lock()
			conns := make([]*phy.KISSTCPServerConn, 0, len(clients))
			for conn := range clients {
				conns = append(conns, conn)
			}
			clientsMu.Unlock()
			for _, conn := range conns {
				if err := conn.Send(f); err != nil {
					log.Printf("kiss tcp send: %v", err)
				}
			}
		},
	}
	if err := router.RegisterPort(kissFanoutPort); err != nil {
		log.Fatalf("register KISS fanout port: %v", err)
	}

	kissTCPSrv, err := phy.NewKISSTCPServerPHY(phy.KISSTCPServerConfig{
		Port: kissPort,
		OnConnected: func(conn *phy.KISSTCPServerConn) {
			clientsMu.Lock()
			clients[conn] = struct{}{}
			clientsMu.Unlock()
		},
		OnDisconnected: func(conn *phy.KISSTCPServerConn) {
			clientsMu.Lock()
			delete(clients, conn)
			clientsMu.Unlock()
		},
		OnRxFrame: func(_ *phy.KISSTCPServerConn, f *ax25.Frame) {
			if err := router.Send(f, nil); err != nil {
				log.Printf("kiss tcp rx: %v", err)
			}
		},
	})
	if err != nil {
		log.Fatalf("create KISS TCP server: %v", err)
	}
	if err := kissTCPSrv.Start(); err != nil {
		log.Fatalf("start KISS TCP server: %v", err)
	}
	defer kissTCPSrv.Stop()

	agwpeSrv := agwpe.NewTCPServer(agwpe.TCPServerConfig{
		Addr: agwpeAddr,
		ServerConfig: agwpe.ServerConfig{
			Port:            0,
			PortDescription: "Serial KISS TNC",
		},
	}, router)
	if err := agwpeSrv.Start(); err != nil {
		log.Fatalf("start AGWPE server: %v", err)
	}
	defer agwpeSrv.Stop()

	slog.Info("combined server running - press Ctrl+C to stop")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	cancel()
	slog.Info("shutting down")
}
