package agwpe_test

import (
	"net"
	"testing"
	"time"

	"github.com/n7get/go-ax25/agwpe"
	"github.com/n7get/go-ax25/ax25"
)

func serverPipe(t *testing.T, cfg agwpe.ServerConfig, router *ax25.Router) (srv *agwpe.Server, client net.Conn) {
	t.Helper()
	srv = agwpe.NewServer(cfg, router)
	serverConn, clientConn := net.Pipe()
	go func() {
		srv.HandleConn(serverConn)
	}()
	return srv, clientConn
}

func readAGWPEFrame(t *testing.T, c net.Conn) *agwpe.Frame {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	f, err := agwpe.ReadFrame(c)
	if err != nil {
		t.Fatalf("readAGWPEFrame: %v", err)
	}
	return f
}

func sendAGWPEFrame(t *testing.T, c net.Conn, f *agwpe.Frame) {
	t.Helper()
	b, err := f.Encode()
	if err != nil {
		t.Fatalf("sendAGWPEFrame encode: %v", err)
	}
	if _, err := c.Write(b); err != nil {
		t.Fatalf("sendAGWPEFrame write: %v", err)
	}
}

func TestServerVersionRequest(t *testing.T) {
	router := ax25.NewRouter()
	_, client := serverPipe(t, agwpe.ServerConfig{Port: 0}, router)
	defer client.Close()

	sendAGWPEFrame(t, client, &agwpe.Frame{Kind: 'R'})
	f := readAGWPEFrame(t, client)
	if f.Kind != 'R' {
		t.Fatalf("expected version response 'R', got %q", string(f.Kind))
	}
	major, minor, err := agwpe.ParseVersionResp(f)
	if err != nil {
		t.Fatalf("ParseVersionResp: %v", err)
	}
	if major != 2005 || minor != 127 {
		t.Fatalf("expected 2005.127, got %d.%d", major, minor)
	}
}

func TestServerPortInfoRequest(t *testing.T) {
	router := ax25.NewRouter()
	_, client := serverPipe(t, agwpe.ServerConfig{Port: 0, PortDescription: "Test TNC"}, router)
	defer client.Close()

	sendAGWPEFrame(t, client, &agwpe.Frame{Kind: 'G'})
	f := readAGWPEFrame(t, client)
	if f.Kind != 'G' {
		t.Fatalf("expected port info response 'G', got %q", string(f.Kind))
	}
}

func TestServerPortCapRequest(t *testing.T) {
	router := ax25.NewRouter()
	_, client := serverPipe(t, agwpe.ServerConfig{Port: 0}, router)
	defer client.Close()

	sendAGWPEFrame(t, client, &agwpe.Frame{Kind: 'g'})
	f := readAGWPEFrame(t, client)
	if f.Kind != 'g' {
		t.Fatalf("expected port cap response 'g', got %q", string(f.Kind))
	}
}

func TestServerRegisterCallsign(t *testing.T) {
	router := ax25.NewRouter()
	_, client := serverPipe(t, agwpe.ServerConfig{Port: 0}, router)
	defer client.Close()

	sendAGWPEFrame(t, client, &agwpe.Frame{Kind: 'X', CallFrom: "N7GET"})
	f := readAGWPEFrame(t, client)
	if f.Kind != 'X' {
		t.Fatalf("expected register response 'X', got %q", string(f.Kind))
	}
	if len(f.Data) == 0 || f.Data[0] != 1 {
		t.Fatalf("expected success byte=1, got %v", f.Data)
	}
}

func TestServerRawToggle(t *testing.T) {
	router := ax25.NewRouter()
	_, client := serverPipe(t, agwpe.ServerConfig{Port: 0}, router)
	defer client.Close()

	sendAGWPEFrame(t, client, &agwpe.Frame{Kind: 'k'})
	time.Sleep(50 * time.Millisecond)
}

func TestServerMonitorToggle(t *testing.T) {
	router := ax25.NewRouter()
	_, client := serverPipe(t, agwpe.ServerConfig{Port: 0}, router)
	defer client.Close()

	sendAGWPEFrame(t, client, &agwpe.Frame{Kind: 'm'})
	time.Sleep(50 * time.Millisecond)
}

func TestServerOutstandingPort(t *testing.T) {
	router := ax25.NewRouter()
	_, client := serverPipe(t, agwpe.ServerConfig{Port: 0}, router)
	defer client.Close()

	sendAGWPEFrame(t, client, &agwpe.Frame{Kind: 'y'})
	f := readAGWPEFrame(t, client)
	if f.Kind != 'y' {
		t.Fatalf("expected outstanding port response 'y', got %q", string(f.Kind))
	}
}

func TestServerLoginSilent(t *testing.T) {
	router := ax25.NewRouter()
	_, client := serverPipe(t, agwpe.ServerConfig{Port: 0}, router)
	defer client.Close()

	sendAGWPEFrame(t, client, &agwpe.Frame{Kind: 'P'})
	sendAGWPEFrame(t, client, &agwpe.Frame{Kind: 'R'})
	f := readAGWPEFrame(t, client)
	if f.Kind != 'R' {
		t.Fatalf("expected version response after login, got %q", string(f.Kind))
	}
}

func TestServerOnConnectedCallback(t *testing.T) {
	router := ax25.NewRouter()
	connected := make(chan struct{}, 1)
	cfg := agwpe.ServerConfig{
		Port:        0,
		OnConnected: func(_ *agwpe.Server) { connected <- struct{}{} },
	}
	_, client := serverPipe(t, cfg, router)
	defer client.Close()

	select {
	case <-connected:
	case <-time.After(time.Second):
		t.Fatal("OnConnected not called")
	}
}

func TestServerOnDisconnectedCallback(t *testing.T) {
	router := ax25.NewRouter()
	disconnected := make(chan struct{}, 1)
	cfg := agwpe.ServerConfig{
		Port:           0,
		OnDisconnected: func(_ *agwpe.Server) { disconnected <- struct{}{} },
	}
	_, client := serverPipe(t, cfg, router)
	client.Close()

	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("OnDisconnected not called")
	}
}

func TestServerTXQueueFull(t *testing.T) {
	router := ax25.NewRouter()
	cfg := agwpe.ServerConfig{Port: 0, TXQueueDepth: 2}
	_, client := serverPipe(t, cfg, router)
	defer client.Close()

	for i := 0; i < 20; i++ {
		sendAGWPEFrame(t, client, &agwpe.Frame{Kind: 'R'})
	}
	client.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	for {
		_, err := agwpe.ReadFrame(client)
		if err != nil {
			break
		}
	}
}
func TestNewServerConfigFromConfig_Defaults(t *testing.T) {
	cfg := ax25.NewConfig(nil)
	c := agwpe.NewServerConfigFromConfig(cfg)
	if c.Port != 8000 {
		t.Errorf("Port: got %d, want 8000", c.Port)
	}
	if c.TXQueueDepth != 64 {
		t.Errorf("TXQueueDepth: got %d, want 64", c.TXQueueDepth)
	}
	if c.MaxConns != 4 {
		t.Errorf("MaxConns: got %d, want 4", c.MaxConns)
	}
	if c.ReadBufSize != 4132 {
		t.Errorf("ReadBufSize: got %d, want 4132", c.ReadBufSize)
	}
}

func TestNewServerConfigFromConfig_Override(t *testing.T) {
	cfg := ax25.NewConfig(nil)
	cfg.Set("agwpe.server.port", "9100")
	cfg.Set("agwpe.server.max_conns", "8")
	c := agwpe.NewServerConfigFromConfig(cfg)
	if c.Port != 9100 {
		t.Errorf("Port: got %d, want 9100", c.Port)
	}
	if c.MaxConns != 8 {
		t.Errorf("MaxConns: got %d, want 8", c.MaxConns)
	}
}
