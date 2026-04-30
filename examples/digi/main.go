// digi - AX.25 digipeater over a serial KISS TNC.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/n7get/go-ax25/ax25"
	"github.com/n7get/go-ax25/phy"
	"go.bug.st/serial"
)

func main() {
	cfgPath := flag.String("config", "ax25.ini", "INI config file")
	debug := flag.Bool("debug", false, "enable debug logging")
	flag.Parse()

	if *debug {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}

	cfg := ax25.NewConfig(nil)
	if err := cfg.LoadINI(*cfgPath); err != nil && !os.IsNotExist(err) {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	digiCall := cfg.GetStr(ax25.KeyDigiCallsign)
	if digiCall == "" {
		slog.Error("digi.callsign not set in config")
		os.Exit(1)
	}

	serialRW, err := serial.Open(cfg.GetStr(ax25.KeyKissSerialDevice), &serial.Mode{BaudRate: cfg.GetInt(ax25.KeyKissSerialBaud)})
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

	router := ax25.NewRouter(nil)

	// phyPort is the source port for all frames arriving from the radio.
	phyPort := &ax25.Port{
		Mode: ax25.PortModeDefault,
		OnRxFrame: func(frame *ax25.Frame) {
			if err := kissPHY.SendFrame(frame); err != nil {
				slog.Error("digi: PHY send error", "err", err)
			}
		},
	}
	if err := router.RegisterPort(phyPort); err != nil {
		slog.Error("register PHY port", "err", err)
		os.Exit(1)
	}

	digi, err := ax25.NewDigipeater(ax25.DigiConfig{Callsign: digiCall}, router, kissPHY.SendFrame)
	if err != nil {
		slog.Error("digipeater", "err", err)
		os.Exit(1)
	}
	defer digi.Close()

	go func() {
		for f := range kissPHY.RxFrames() {
			if err := router.Send(f, phyPort); err != nil {
				slog.Error("router send", "err", err)
			}
		}
	}()

	slog.Info("Digipeater running", "callsign", digiCall)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	cancel()
	slog.Info("Shutting down")
}
