package phy_test

import (
	"bytes"
	"testing"

	"github.com/n7get/go-ax25/ax25"
	"github.com/n7get/go-ax25/phy"
)

func TestNewKISSSerialConfigFromConfig_Defaults(t *testing.T) {
	cfg := ax25.NewConfig(nil)
	c := phy.NewKISSSerialConfigFromConfig(cfg)
	if c.TXQueueDepth != 32 {
		t.Errorf("TXQueueDepth: got %d, want 32", c.TXQueueDepth)
	}
	if c.ReadBufSize != 1024 {
		t.Errorf("ReadBufSize: got %d, want 1024", c.ReadBufSize)
	}
}

func TestNewKISSSerialConfigFromConfig_Override(t *testing.T) {
	cfg := ax25.NewConfig(nil)
	cfg.Set(ax25.KeyKissSerialTxQueueDepth, "16")
	cfg.Set(ax25.KeyKissSerialReadBuf, "8192")
	c := phy.NewKISSSerialConfigFromConfig(cfg)
	if c.TXQueueDepth != 16 {
		t.Errorf("TXQueueDepth: got %d, want 16", c.TXQueueDepth)
	}
	if c.ReadBufSize != 8192 {
		t.Errorf("ReadBufSize: got %d, want 8192", c.ReadBufSize)
	}
}

func TestNewKISSSerialPHY_NilPort(t *testing.T) {
	_, err := phy.NewKISSSerialPHY(phy.KISSSerialConfig{})
	if err == nil {
		t.Fatal("expected error for nil Port")
	}
}

func TestNewKISSSerialPHY_Success(t *testing.T) {
	rw := bytes.NewBuffer(nil)
	p, err := phy.NewKISSSerialPHY(phy.KISSSerialConfig{Port: rw, TXQueueDepth: 4, ReadBufSize: 128})
	if err != nil {
		t.Fatalf("NewKISSSerialPHY: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil PHY")
	}
}
