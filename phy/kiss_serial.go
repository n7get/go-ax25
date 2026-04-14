package phy

import (
	"fmt"
	"io"

	"github.com/n7get/go-ax25/ax25"
)

// KISSSerialConfig holds configuration for a serial-port KISS PHY.
// The caller is responsible for opening the serial port and providing an
// io.ReadWriter (e.g. via go.bug.st/serial).
type KISSSerialConfig struct {
	Port         io.ReadWriter
	TXQueueDepth int
	OnRxFrame    ax25.FrameCallback
	OnError      ax25.ErrorCallback
}

// NewKISSSerialPHY creates a KISSSerialPHY backed by the provided io.ReadWriter.
// This is a thin wrapper around ax25.NewKISSSerialPHY so that the phy package
// can be imported without pulling serial-port dependencies into the ax25 core.
func NewKISSSerialPHY(cfg KISSSerialConfig) (*ax25.KISSSerialPHY, error) {
	if cfg.Port == nil {
		return nil, fmt.Errorf("phy: KISSSerialPHY: Port must not be nil")
	}
	return ax25.NewKISSSerialPHY(cfg.Port, ax25.KISSSerialPHYConfig{
		TxQueueDepth: cfg.TXQueueDepth,
	}), nil
}
