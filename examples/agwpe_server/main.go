// agwpe_server — AGWPE TCP server bridging to a serial KISS TNC.
//
// Usage:
//
//	 agwpe_server -config ax25.ini
//
// INI keys used:
//
//	 [serial]
//	 port  = /dev/ttyUSB0
//	 baud  = 9600
//	
//	 [net]
//	 agwpe_port = 8000
//	
//	 [station]
//	 callsign = N0CALL
package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"os"
	"os/signal"
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

	slog.Info("starting agwpe_server", "serial", serialPort, "baud", serialBaud, "agwpe", agwpeAddr)

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

	slog.Info("agwpe_server running - press Ctrl+C to stop")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	cancel()
	slog.Info("shutting down")
}
