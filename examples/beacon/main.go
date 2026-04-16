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
	// Command-line flags for configuration
	var (
		debug        = flag.Bool("debug", false, "enable debug logging")
		serialDevice = flag.String("serial", "/dev/ttyUSB0", "Serial device for KISS TNC")
		baud         = flag.Int("baud", 9600, "Serial baud rate")
		callsign     = flag.String("callsign", "N0CALL", "Source callsign")
		dest         = flag.String("dest", "APRS", "Beacon dest callsign")
		via          = flag.String("via", "", "Comma-separated digipeater path")
		text         = flag.String("text", "go-ax25 beacon", "Beacon text")
		every        = flag.Int("every", 10, "Beacon interval in minutes")
	)

	flag.Parse()

	if *debug {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}

	slog.Info("Starting beacon", "serial", *serialDevice, "baud", *baud)
	serialRW, err := serial.Open(*serialDevice, &serial.Mode{BaudRate: *baud})
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
		Source:      *callsign,
		Destination: *dest,
		Via:         *via,
		Text:        *text,
		Every:       time.Duration(*every) * time.Minute,
	}

	// Start a goroutine to drain and discard all inbound frames from the KISS serial PHY
	go func(rxCh <-chan *ax25.Frame, ctx context.Context) {
		for {
			select {
			case <-ctx.Done():
				return
			case <-rxCh:
				// case f := <-rxCh:
				// 	slog.Debug("ax25: main: rx",
				// 		"src", f.Source.String(),
				// 		"dst", f.Destination.String(),
				// 		"type", f.Type,
				// 		"payload_len", len(f.Payload),
				// 	)
			}
		}
	}(kissPHY.RxFrames(), ctx)

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
