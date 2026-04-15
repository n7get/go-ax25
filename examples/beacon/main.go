// beacon - periodic APRS-style UI beacon over a serial KISS TNC.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/n7get/go-ax25/ax25"
	"github.com/n7get/go-ax25/phy"
	"go.bug.st/serial"
)

func main() {
	// Set up slog for structured logging at Debug level
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	cfgPath := flag.String("config", "ax25.ini", "INI config file")
	flag.Parse()

	if cfgPath != nil {
		if _, err := os.Stat(*cfgPath); os.IsNotExist(err) {
			slog.Error("Config file not found", "path", *cfgPath)
			os.Exit(1)
		}
	}

	cfg := ax25.NewConfig(nil)
	if err := cfg.LoadINI(*cfgPath); err != nil && !os.IsNotExist(err) {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}

	serialDevice := cfg.GetStr("serial.device", "/dev/ttyUSB0")
	serialBaud := cfg.GetInt("serial.baud", 9600)

	slog.Info("Starting beacon", "serial.device", serialDevice, "serial.baud", serialBaud)
	serialRW, err := serial.Open(serialDevice, &serial.Mode{BaudRate: serialBaud})
	if err != nil {
		slog.Error("open serial port", "err", err)
		os.Exit(1)
	}
	defer serialRW.Close()

	kissPHY, err := phy.NewKISSSerialPHY(phy.KISSSerialConfig{Port: serialRW})
	if err != nil {
		slog.Error("serial PHY", "err", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := kissPHY.Start(ctx); err != nil {
		slog.Error("start PHY", "err", err)
		os.Exit(1)
	}
	defer kissPHY.Stop()

	bcnCfg := ax25.BeaconConfig{
		Source:      cfg.GetStr("station.callsign", "N0CALL"),
		Destination: cfg.GetStr("beacon.destination", "APRS"),
		Via:         cfg.GetStr("beacon.via", ""),
		Text:        cfg.GetStr("beacon.text", "go-ax25 beacon"),
		Every:       time.Duration(cfg.GetInt("beacon.every", 10)) * time.Minute,
	}
	bcn := ax25.NewBeacon(bcnCfg, kissPHY.SendFrame)
	bcn.Start(ctx)
	defer bcn.Stop()

	slog.Info("Beacon started", "src", bcnCfg.Source, "dst", bcnCfg.Destination, "every", bcnCfg.Every)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	cancel()
	slog.Info("Shutting down")
}
