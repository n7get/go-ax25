package ax25

import "errors"

// Sentinel errors shared across packages.
var (
	// ErrTXQueueFull is returned by Send when the TX queue is full.
	ErrTXQueueFull = errors.New("ax25: TX queue full")
	// ErrNotConnected is returned by Send when no connection is active.
	ErrNotConnected = errors.New("ax25: not connected")
)
