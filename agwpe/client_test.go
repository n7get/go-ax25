package agwpe

import (
	"testing"
	"time"
)

func TestNewClientValidation(t *testing.T) {
	// Missing host.
	_, err := NewClient(ClientConfig{Port: 8000, OnRxFrame: func(*Frame) {}})
	if err == nil {
		t.Error("expected error for empty host")
	}

	// Missing port.
	_, err = NewClient(ClientConfig{Host: "localhost", OnRxFrame: func(*Frame) {}})
	if err == nil {
		t.Error("expected error for zero port")
	}

	// Missing callback.
	_, err = NewClient(ClientConfig{Host: "localhost", Port: 8000})
	if err == nil {
		t.Error("expected error for nil OnRxFrame")
	}

	// Valid config.
	c, err := NewClient(ClientConfig{
		Host:      "localhost",
		Port:      8000,
		OnRxFrame: func(*Frame) {},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.cfg.ConnectTimeout != 6*time.Second {
		t.Errorf("default ConnectTimeout: got %v", c.cfg.ConnectTimeout)
	}
}

func TestClientToggleAliases(t *testing.T) {
	// Verify the deprecated aliases compile and point to the same functions.
	f1 := BuildEnableMonitor()
	f2 := BuildToggleMonitor()
	if f1.Kind != f2.Kind {
		t.Errorf("BuildEnableMonitor kind %c != BuildToggleMonitor kind %c", f1.Kind, f2.Kind)
	}

	f3 := BuildEnableRaw()
	f4 := BuildToggleRaw()
	if f3.Kind != f4.Kind {
		t.Errorf("BuildEnableRaw kind %c != BuildToggleRaw kind %c", f3.Kind, f4.Kind)
	}
}
