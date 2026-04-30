package agwpe

import (
	"testing"
	"time"

	"github.com/n7get/go-ax25/ax25"
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

func TestNewClientConfigFromConfig_Defaults(t *testing.T) {
	cfg := ax25.NewConfig(nil)
	c := NewClientConfigFromConfig(cfg)
	if c.Host != "localhost" {
		t.Errorf("Host: got %q, want localhost", c.Host)
	}
	if c.Port != 8000 {
		t.Errorf("Port: got %d, want 8000", c.Port)
	}
	if c.TXQueueDepth != 8 {
		t.Errorf("TXQueueDepth: got %d, want 8", c.TXQueueDepth)
	}
	if c.ReadBufSize != 4132 {
		t.Errorf("ReadBufSize: got %d, want 4132", c.ReadBufSize)
	}
}

func TestNewClientConfigFromConfig_Override(t *testing.T) {
	cfg := ax25.NewConfig(nil)
	cfg.Set(ax25.KeyAgwpeClientHost, "192.168.1.20")
	cfg.Set(ax25.KeyAgwpeClientPort, "9000")
	cfg.Set(ax25.KeyAgwpeClientTxQueueDepth, "32")
	cfg.Set(ax25.KeyAgwpeClientReadBuf, "8192")

	c := NewClientConfigFromConfig(cfg)
	if c.Host != "192.168.1.20" {
		t.Errorf("Host: got %q, want 192.168.1.20", c.Host)
	}
	if c.Port != 9000 {
		t.Errorf("Port: got %d, want 9000", c.Port)
	}
	if c.TXQueueDepth != 32 {
		t.Errorf("TXQueueDepth: got %d, want 32", c.TXQueueDepth)
	}
	if c.ReadBufSize != 8192 {
		t.Errorf("ReadBufSize: got %d, want 8192", c.ReadBufSize)
	}
}

func TestClientSendFrameErrors(t *testing.T) {
	c, err := NewClient(ClientConfig{Host: "127.0.0.1", Port: 9001, OnRxFrame: func(*Frame) {}})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	if err := c.SendFrame(nil); err == nil {
		t.Fatal("expected error for nil frame")
	}
	if err := c.SendFrame(BuildVersionReq()); err != ax25.ErrNotConnected {
		t.Fatalf("expected ErrNotConnected, got %v", err)
	}
	if err := c.RequestVersion(); err != ax25.ErrNotConnected {
		t.Fatalf("RequestVersion: expected ErrNotConnected, got %v", err)
	}
	if err := c.RequestPortInfo(); err != ax25.ErrNotConnected {
		t.Fatalf("RequestPortInfo: expected ErrNotConnected, got %v", err)
	}
	if err := c.RequestPortCap(1); err != ax25.ErrNotConnected {
		t.Fatalf("RequestPortCap: expected ErrNotConnected, got %v", err)
	}
	if err := c.Login("u", "p"); err != ax25.ErrNotConnected {
		t.Fatalf("Login: expected ErrNotConnected, got %v", err)
	}
}
