package main

import (
	"testing"

	"github.com/n7get/go-ax25/ax25"
)

func TestResolveKISSTCPClientConfig_PrefersBeaconAddr(t *testing.T) {
	cfg := ax25.NewConfig(nil)
	cfg.Set(ax25.KeyBeaconAddr, "beacon-host:9901")
	cfg.Set(ax25.KeyKissClientHost, "kiss-host")
	cfg.Set(ax25.KeyKissClientPort, "8100")

	kcfg, resolved, err := resolveKISSTCPClientConfig(cfg)
	if err != nil {
		t.Fatalf("resolveKISSTCPClientConfig() error = %v", err)
	}
	if kcfg.Host != "beacon-host" {
		t.Fatalf("Host = %q, want beacon-host", kcfg.Host)
	}
	if kcfg.Port != 9901 {
		t.Fatalf("Port = %d, want 9901", kcfg.Port)
	}
	if resolved != "beacon-host:9901" {
		t.Fatalf("resolved = %q, want beacon-host:9901", resolved)
	}
}

func TestResolveKISSTCPClientConfig_FallsBackToKissClient(t *testing.T) {
	cfg := ax25.NewConfig(nil)
	cfg.Set(ax25.KeyKissClientHost, "kiss-host")
	cfg.Set(ax25.KeyKissClientPort, "8200")

	kcfg, resolved, err := resolveKISSTCPClientConfig(cfg)
	if err != nil {
		t.Fatalf("resolveKISSTCPClientConfig() error = %v", err)
	}
	if kcfg.Host != "kiss-host" {
		t.Fatalf("Host = %q, want kiss-host", kcfg.Host)
	}
	if kcfg.Port != 8200 {
		t.Fatalf("Port = %d, want 8200", kcfg.Port)
	}
	if resolved != "kiss-host:8200" {
		t.Fatalf("resolved = %q, want kiss-host:8200", resolved)
	}
}

func TestResolveKISSTCPClientConfig_InvalidBeaconAddr(t *testing.T) {
	cfg := ax25.NewConfig(nil)
	cfg.Set(ax25.KeyBeaconAddr, "not-an-addr")

	_, _, err := resolveKISSTCPClientConfig(cfg)
	if err == nil {
		t.Fatal("resolveKISSTCPClientConfig() error = nil, want error")
	}
}
