// connect - interactive AX.25 connected-mode chat over a serial KISS TNC.
package main

import (
	"bufio"
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
	remoteStr := flag.String("remote", "", "Remote callsign (e.g. W7ABC-1)")
	flag.Parse()

	cfg := ax25.NewConfig(nil)
	if err := cfg.LoadINI(*cfgPath); err != nil && !os.IsNotExist(err) {
		log.Fatalf("config: %v", err)
	}

	localAddr, err := ax25.ParseAddress(fmt.Sprintf("%s-%d", cfg.GetStr("station.callsign", "N0CALL"), cfg.GetInt("station.ssid", 0)))
	if err != nil {
		log.Fatalf("invalid local callsign: %v", err)
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

	var conn *ax25.Conn
	cbs := ax25.ConnCallbacks{
		OnConnect: func(remote ax25.Address, isLocal bool) {
			if isLocal {
				fmt.Printf("*** Connected to %s ***\n", remote)
			} else {
				fmt.Printf("*** Incoming connection from %s ***\n", remote)
			}
		},
		OnDisconnect: func() { fmt.Println("*** Disconnected ***") },
		OnError:      func(e *ax25.ConnError) { fmt.Printf("*** Error: %s ***\n", e.Message) },
		OnData:       func(d []byte) { fmt.Printf("[REMOTE]: %s", d) },
		OnTxFrame: func(f *ax25.Frame) {
			if err := kissPHY.SendFrame(f); err != nil {
				log.Printf("send frame: %v", err)
			}
		},
	}

	conn, err = ax25.NewConn(localAddr, cbs, &ax25.ConnConfig{
		T1:     time.Duration(cfg.GetInt("conn.t1_ms", 10000)) * time.Millisecond,
		T2:     time.Duration(cfg.GetInt("conn.t2_ms", 1000)) * time.Millisecond,
		T3:     time.Duration(cfg.GetInt("conn.t3_ms", 180000)) * time.Millisecond,
		N2:     cfg.GetInt("conn.n2_retries", 10),
		Window: cfg.GetInt("conn.window_size", 4),
	})
	if err != nil {
		log.Fatalf("new conn: %v", err)
	}
	defer conn.Close()

	go func() {
		for f := range kissPHY.RxFrames() {
			if err := conn.OnFrame(f); err != nil {
				log.Printf("rx frame: %v", err)
			}
		}
	}()

	if *remoteStr != "" {
		remote, err := ax25.ParseAddress(*remoteStr)
		if err != nil {
			log.Fatalf("invalid remote: %v", err)
		}
		if err := conn.Connect(remote); err != nil {
			log.Fatalf("connect: %v", err)
		}
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("Type messages and press Enter. Ctrl-C to quit.")
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			if line == "/disconnect" {
				_ = conn.Shutdown()
				continue
			}
			if err := conn.SendData([]byte(line + "\n")); err != nil {
				fmt.Printf("send error: %v\n", err)
			}
		}
	}()

	<-sig
	cancel()
	log.Println("Shutting down")
}
