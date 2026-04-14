// beacon - periodic APRS-style UI beacon over a serial KISS TNC.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

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

	bcnCfg := ax25.BeaconConfig{
		Source:      fmt.Sprintf("%s-%d", cfg.GetStr("station.callsign", "N0CALL"), cfg.GetInt("station.ssid", 0)),
		Destination: cfg.GetStr("beacon.destination", "APRS"),
		Via:         cfg.GetStr("beacon.via", ""),
		Text:        cfg.GetStr("beacon.text", "go-ax25 beacon"),
		Every:       time.Duration(cfg.GetInt("beacon.every", 10)) * time.Minute,
	}
	bcn := ax25.NewBeacon(bcnCfg, kissPHY.SendFrame)
	bcn.Start(ctx)
	defer bcn.Stop()

	log.Printf("Beacon started: %s > %s every %v", bcnCfg.Source, bcnCfg.Destination, bcnCfg.Every)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	cancel()
	log.Println("Shutting down")
}
