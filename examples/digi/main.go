// digi - AX.25 digipeater over a serial KISS TNC.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/n7get/go-ax25/ax25"
	"github.com/n7get/go-ax25/phy"
	"go.bug.st/serial"
)

func main() {
	cfgPath := flag.String("config", "ax25.ini", "INI config file")
	flag.Parse()

	cfg := ax25.NewConfig(nil)
	if err := cfg.LoadINI(*cfgPath); err != nil && !os.IsNotExist(err) {
		log.Fatalf("config: %v", err)
	}

	digiCall := cfg.GetStr("digi.callsign", "")
	if digiCall == "" {
		log.Fatal("digi.callsign not set in config")
	}

	serialRW, err := serial.Open(cfg.GetStr("serial.device", "/dev/ttyUSB0"), &serial.Mode{BaudRate: cfg.GetInt("serial.baud", 9600)})
	if err != nil {
		log.Fatalf("open serial port: %v", err)
	}
	defer serialRW.Close()

	kissPHY, err := phy.NewKISSSerialPHY(phy.KISSSerialConfig{Port: serialRW})
	if err != nil {
		log.Fatalf("serial PHY: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := kissPHY.Start(ctx); err != nil {
		log.Fatalf("start PHY: %v", err)
	}
	defer kissPHY.Stop()

	router := ax25.NewRouter()
	digi, err := ax25.NewDigipeater(ax25.DigiConfig{Callsign: digiCall}, router, kissPHY.SendFrame)
	if err != nil {
		log.Fatalf("digipeater: %v", err)
	}
	defer digi.Close()

	go func() {
		for f := range kissPHY.RxFrames() {
			if err := router.Send(f, nil); err != nil {
				log.Printf("router send: %v", err)
			}
		}
	}()

	log.Printf("Digipeater running as %s", digiCall)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	cancel()
	log.Println("Shutting down")
}
