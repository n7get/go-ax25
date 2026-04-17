// agwpe_server — AGWPE TCP server bridging to a serial KISS TNC.
//
// Usage:
//
//	agwpe_server [flags]
//
// Flags:
//
//	-port  /dev/ttyUSB0   serial port device
//	-baud  9600           serial baud rate
//	-addr  :8000          AGWPE TCP listen address
//	-debug               enable debug logging
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/n7get/go-ax25/agwpe"
	"github.com/n7get/go-ax25/ax25"
	"github.com/n7get/go-ax25/phy"
	"go.bug.st/serial"
)

func main() {
	cfgPath := flag.String("config", "ax25.ini", "INI config file")
	debug := flag.Bool("debug", false, "enable debug logging")
	flag.Parse()

	logLevel := slog.LevelInfo
	if *debug {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	cfg := ax25.NewConfig(nil)
	if err := cfg.LoadINI(*cfgPath); err != nil {
		log.Fatalf("load config: %v", err)
	}

	serialPort := cfg.GetStr("kiss.serial.device", "/dev/ttyUSB0")
	serialBaud := cfg.GetInt("kiss.serial.baud", 9600)
	agwpePort := cfg.GetInt("agwpe.server.port", 8000)

	slog.Info("starting agwpe_server", "serial", serialPort, "baud", serialBaud, "agwpe_port", agwpePort)

	router := ax25.NewRouter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serialRW, err := serial.Open(serialPort, &serial.Mode{BaudRate: serialBaud})
	if err != nil {
		log.Fatalf("open serial port: %v", err)
	}
	defer serialRW.Close()
	slog.Debug("serial port opened", "port", serialPort, "baud", serialBaud)

	kissCfg := phy.NewKISSSerialConfigFromConfig(cfg)
	kissCfg.Port = serialRW
	kissPHY, err := phy.NewKISSSerialPHY(kissCfg)
	if err != nil {
		log.Fatalf("serial PHY: %v", err)
	}
	if err := kissPHY.Start(ctx); err != nil {
		log.Fatalf("start serial PHY: %v", err)
	}
	defer kissPHY.Stop()
	slog.Debug("KISS serial PHY started")

	serialRouterPort := &ax25.Port{
		Mode: ax25.PortModeDefault,
		OnRxFrame: func(f *ax25.Frame) {
			slog.Debug("serial tx", "frame", f)
			if err := kissPHY.SendFrame(f); err != nil {
				log.Printf("serial send: %v", err)
			}
		},
	}
	if err := router.RegisterPort(serialRouterPort); err != nil {
		log.Fatalf("register serial port: %v", err)
	}
	slog.Debug("serial router port registered")

	go func() {
		for f := range kissPHY.RxFrames() {
			slog.Debug("serial rx", "frame", f)
			if err := router.Send(f, serialRouterPort); err != nil {
				log.Printf("router rx: %v", err)
			}
		}
	}()

	agwpeSrv := agwpe.NewTCPServer(agwpe.TCPServerConfig{
		Addr:         fmt.Sprintf(":%d", agwpePort),
		ServerConfig: agwpe.NewServerConfigFromConfig(cfg),
	}, router)
	if err := agwpeSrv.Start(); err != nil {
		log.Fatalf("start AGWPE server: %v", err)
	}
	defer agwpeSrv.Stop()
	slog.Debug("AGWPE TCP server started", "addr", agwpePort)

	slog.Info("agwpe_server running - press Ctrl+C to stop")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	cancel()
	slog.Info("shutting down")
}
