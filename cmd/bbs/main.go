// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

// bbs is a packet-radio BBS that connects as an AGWPE client.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/n7get/go-ax25/agwpe"
	"github.com/n7get/go-ax25/ax25"
	"github.com/n7get/go-ax25/cmd/bbs/bbs"
	"github.com/n7get/go-ax25/cmd/bbs/heard"
	"github.com/n7get/go-ax25/cmd/bbs/store"
)

func main() {
	configPath := flag.String("config", "ax25.ini", "path to ax25.ini config file")
	debug := flag.Bool("debug", false, "enable debug logging")
	flag.Parse()

	// -- logging --
	logLevel := slog.LevelInfo
	if *debug {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	// -- config --
	cfg := ax25.NewConfig(bbs.BBSConfigSchema)
	if err := cfg.LoadINI(*configPath); err != nil {
		slog.Error("failed to load config", "path", *configPath, "err", err)
		os.Exit(1)
	}

	bbsCfg := bbs.LoadBBSConfig(cfg)
	slog.Info("BBS config loaded",
		"callsign", bbsCfg.Callsign,
		"host", bbsCfg.AGWPEHost,
		"port", bbsCfg.AGWPEPort,
		"db_path", bbsCfg.DBPath,
	)

	// -- data store --
	msgStore, err := store.NewSQLiteStore(bbsCfg.DBPath)
	if err != nil {
		slog.Error("failed to open message store", "err", err)
		os.Exit(1)
	}
	defer msgStore.Close()
	slog.Info("message store opened", "path", bbsCfg.DBPath)

	// -- heard list --
	heardList := heard.New(20)

	// -- session manager --
	mgr := bbs.NewSessionManager(bbsCfg, msgStore, heardList)

	// -- AGWPE client --
	agwCfg := agwpe.ClientConfig{
		Host:           bbsCfg.AGWPEHost,
		Port:           bbsCfg.AGWPEPort,
		ConnectTimeout: 6 * time.Second,
		ReconnectDelay: 5 * time.Second,
		TXQueueDepth:   cfg.GetInt(ax25.KeyAgwpeClientTxQueueDepth),
		ReadBufSize:    cfg.GetInt(ax25.KeyAgwpeClientReadBuf),
		OnRxFrame:      func(f *agwpe.Frame) { handleAGWPEFrame(f, mgr, heardList, bbsCfg.Callsign) },
		OnError:        func(err error) { slog.Error("AGWPE client error", "err", err) },
	}

	client, err := agwpe.NewClient(agwCfg)
	if err != nil {
		slog.Error("failed to create AGWPE client", "err", err)
		os.Exit(1)
	}

	mgr.SetAGWPEClient(client)
	client.Start()
	slog.Info("AGWPE client started", "host", bbsCfg.AGWPEHost, "port", bbsCfg.AGWPEPort)

	// Register our callsign once connected, retrying until the AGWPE
	// connection is established.
	go func() {
		for {
			time.Sleep(500 * time.Millisecond)
			if err := client.SendFrame(agwpe.BuildRegisterCall(0, bbsCfg.Callsign)); err != nil {
				slog.Debug("waiting for AGWPE connection", "err", err)
				continue
			}
			slog.Info("registered callsign", "callsign", bbsCfg.Callsign)
			// Enable monitoring for heard list.
			if err := client.ToggleMonitor(); err != nil {
				slog.Debug("failed to toggle monitor", "err", err)
			}
			return
		}
	}()

	// -- wait for signal --
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	sig := <-sigCh
	slog.Info("received signal, shutting down", "signal", sig)

	client.Stop()
	mgr.DisconnectAll()
	slog.Info("bbs stopped")
}

// handleAGWPEFrame dispatches incoming AGWPE frames to the session manager.
func handleAGWPEFrame(f *agwpe.Frame, mgr *bbs.SessionManager, hl *heard.List, myCall string) {
	if f == nil {
		return
	}

	slog.Debug("AGWPE rx",
		"kind", fmt.Sprintf("%c", f.Kind),
		"from", f.CallFrom,
		"to", f.CallTo,
		"data_len", len(f.Data),
	)

	switch f.Kind {
	case agwpe.KindConnectResp:
		// Incoming connection from a remote station.
		mgr.OnConnect(f.CallFrom, f.CallTo, f.Port)

	case agwpe.KindRecvData:
		// Data from a connected station.
		mgr.OnData(f.CallFrom, f.CallTo, f.Data)

	case agwpe.KindDisconnectResp:
		// Station disconnected.
		mgr.OnDisconnect(f.CallFrom, f.CallTo)

	case agwpe.KindRecvUnproto, agwpe.KindRecvIFrame, agwpe.KindRecvSupervisory:
		// Monitored traffic -- update heard list.
		if f.CallFrom != "" && f.CallFrom != myCall {
			hl.Add(f.CallFrom)
		}
	}
}
