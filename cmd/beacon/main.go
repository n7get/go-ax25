// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

// beacon periodically transmits AX.25 UI frames over a KISS TCP client PHY.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/n7get/go-ax25/ax25"
	"github.com/n7get/go-ax25/phy"
)

func main() {
	configPath := flag.String("config", "ax25.ini", "path to ax25.ini")
	debug := flag.Bool("debug", false, "enable debug logging")
	flag.Parse()

	logLevel := slog.LevelInfo
	if *debug {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	cfg := ax25.NewConfig(nil)
	if err := cfg.LoadINI(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to load config %q: %v\n", *configPath, err)
		os.Exit(1)
	}

	bcnCfg := ax25.BeaconConfigFromConfig(cfg)
	if strings.TrimSpace(bcnCfg.Source) == "" {
		fmt.Fprintln(os.Stderr, "error: beacon.source must be set")
		os.Exit(2)
	}
	if bcnCfg.Every <= 0 {
		fmt.Fprintln(os.Stderr, "error: beacon.every must be > 0")
		os.Exit(2)
	}

	kcfg, resolvedAddr, err := resolveKISSTCPClientConfig(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid KISS TCP config: %v\n", err)
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	errCh := make(chan error, 1)
	kcfg.OnRxFrame = func(*ax25.Frame) {}
	kcfg.OnError = func(err error) {
		select {
		case errCh <- err:
		default:
		}
	}

	kissPHY, err := phy.NewKISSTCPClientPHY(kcfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: create KISS TCP client: %v\n", err)
		os.Exit(1)
	}
	kissPHY.Start()
	defer kissPHY.Stop()

	bcn := ax25.NewBeacon(bcnCfg, func(f *ax25.Frame) error {
		if err := kissPHY.Send(f); err != nil {
			if errors.Is(err, ax25.ErrNotConnected) {
				slog.Warn("beacon send skipped: KISS TCP not connected yet")
				return nil
			}
			return err
		}
		return nil
	})
	bcn.Start(ctx)
	defer bcn.Stop()

	slog.Info("beacon started",
		"source", bcnCfg.Source,
		"destination", bcnCfg.Destination,
		"every", bcnCfg.Every,
		"kiss_addr", resolvedAddr,
	)

	select {
	case <-ctx.Done():
		slog.Info("beacon stopping")
	case err := <-errCh:
		fmt.Fprintf(os.Stderr, "error: KISS TCP client: %v\n", err)
		os.Exit(1)
	}
}

func resolveKISSTCPClientConfig(cfg *ax25.Config) (phy.KISSTCPClientConfig, string, error) {
	kcfg := phy.NewKISSTCPClientConfigFromConfig(cfg)

	beaconAddr := strings.TrimSpace(cfg.GetStr(ax25.KeyBeaconAddr))
	if beaconAddr != "" {
		host, port, err := splitHostPort(beaconAddr)
		if err != nil {
			return phy.KISSTCPClientConfig{}, "", fmt.Errorf("beacon.addr: %w", err)
		}
		kcfg.Host = host
		kcfg.Port = uint16(port)
		return kcfg, beaconAddr, nil
	}

	host := strings.TrimSpace(cfg.GetStr(ax25.KeyKissClientHost))
	if host == "" {
		return phy.KISSTCPClientConfig{}, "", fmt.Errorf("kiss.client.host must not be empty")
	}
	port := cfg.GetInt(ax25.KeyKissClientPort)
	if port <= 0 || port > 65535 {
		return phy.KISSTCPClientConfig{}, "", fmt.Errorf("kiss.client.port must be 1..65535")
	}

	resolvedAddr := net.JoinHostPort(host, strconv.Itoa(port))
	return kcfg, resolvedAddr, nil
}

func splitHostPort(addr string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return "", 0, fmt.Errorf("host must not be empty")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port %q", portStr)
	}
	if port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("port must be 1..65535")
	}
	return host, port, nil
}
