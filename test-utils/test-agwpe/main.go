package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/n7get/go-ax25/agwpe"
)

const rxQueueSize = 64

// appCtx holds shared state across tests.
type appCtx struct {
	client     *agwpe.Client
	localCall  string
	remoteCall string
	agwpePort  uint8

	mu   sync.Mutex
	rxCh chan []byte

	versionCh         chan *agwpe.Frame
	portInfoCh        chan *agwpe.Frame
	portCapCh         chan *agwpe.Frame
	heardCh           chan *agwpe.Frame
	outstandingPortCh chan *agwpe.Frame
	outstandingConnCh chan *agwpe.Frame
	registerCh        chan *agwpe.Frame
	connectedCh       chan *agwpe.Frame
	disconnCh         chan *agwpe.Frame
	monitorCh         chan *agwpe.Frame

	testsRun     int
	testsFailed  int
	testsSkipped int
	connected    bool
	monitorOn    bool
}

func newAppCtx() *appCtx {
	return &appCtx{
		versionCh:         make(chan *agwpe.Frame, 1),
		portInfoCh:        make(chan *agwpe.Frame, 1),
		portCapCh:         make(chan *agwpe.Frame, 1),
		heardCh:           make(chan *agwpe.Frame, 20),
		outstandingPortCh: make(chan *agwpe.Frame, 1),
		outstandingConnCh: make(chan *agwpe.Frame, 1),
		registerCh:        make(chan *agwpe.Frame, 1),
		connectedCh:       make(chan *agwpe.Frame, 1),
		disconnCh:         make(chan *agwpe.Frame, 1),
		monitorCh:         make(chan *agwpe.Frame, 8),
	}
}

// ── RX channel management ─────────────────────────────────────────────────────

func (a *appCtx) enqueue(data []byte) {
	a.mu.Lock()
	ch := a.rxCh
	a.mu.Unlock()
	if ch == nil {
		return
	}
	b := make([]byte, len(data))
	copy(b, data)
	select {
	case ch <- b:
	default:
		slog.Warn("rx queue full, dropping bytes", "len", len(data))
	}
}

func (a *appCtx) openRxCh() {
	a.mu.Lock()
	a.rxCh = make(chan []byte, rxQueueSize)
	a.mu.Unlock()
}

func (a *appCtx) closeRxCh() {
	a.mu.Lock()
	ch := a.rxCh
	a.rxCh = nil
	a.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

func (a *appCtx) getRxCh() chan []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rxCh
}

// ── Frame dispatcher ──────────────────────────────────────────────────────────

func nonBlocking(ch chan *agwpe.Frame, f *agwpe.Frame) {
	select {
	case ch <- f:
	default:
	}
}

func (a *appCtx) handleFrame(f *agwpe.Frame) {
	if f == nil {
		return
	}
	switch f.Kind {
	case 'R': // version response
		nonBlocking(a.versionCh, f)
	case 'G': // port info response
		nonBlocking(a.portInfoCh, f)
	case 'g': // port capabilities response
		nonBlocking(a.portCapCh, f)
	case 'H': // heard stations
		select {
		case a.heardCh <- f:
		default:
		}
	case 'y': // outstanding frames (port)
		nonBlocking(a.outstandingPortCh, f)
	case 'Y': // outstanding frames (connection)
		nonBlocking(a.outstandingConnCh, f)
	case 'X': // register callsign response
		nonBlocking(a.registerCh, f)
	case 'C': // connected response
		nonBlocking(a.connectedCh, f)
	case 'D': // connected data
		a.enqueue(f.Data)
	case 'd': // disconnected
		a.closeRxCh()
		nonBlocking(a.disconnCh, f)
	case 'U', 'I', 'S', 'T', 'K': // monitor / raw frames
		slog.Debug("monitor frame", "kind", string([]byte{f.Kind}), "from", f.CallFrom, "to", f.CallTo)
		select {
		case a.monitorCh <- f:
		default:
		}
	default:
		slog.Debug("unhandled frame", "kind", string([]byte{f.Kind}))
	}
}

// ── Test helpers ──────────────────────────────────────────────────────────────

func (a *appCtx) recordResult(name string, pass bool, detail string) {
	a.testsRun++
	if !pass {
		a.testsFailed++
	}
	status := "PASS"
	if !pass {
		status = "FAIL"
	}
	fmt.Printf("[%s] %s - %s\n", status, name, detail)
}

func (a *appCtx) recordSkip(name, reason string) {
	a.testsSkipped++
	fmt.Printf("[SKIP] %s - %s\n", name, reason)
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// testLogin sends a 'P' login frame. The spec says the server sends no
// confirmation, so a successful send is all we can check.
func testLogin(_ context.Context, a *appCtx, user, pass string) {
	if user == "" {
		a.recordSkip("login", "no -login-user provided")
		return
	}
	if err := a.client.Login(user, pass); err != nil {
		a.recordResult("login", false, fmt.Sprintf("send error: %v", err))
	} else {
		a.recordResult("login", true, "sent (no confirmation expected)")
	}
}

// testVersion sends 'R' and waits for the version response.
func testVersion(ctx context.Context, a *appCtx, stepTimeout time.Duration) {
	if err := a.client.RequestVersion(); err != nil {
		a.recordResult("version", false, fmt.Sprintf("send error: %v", err))
		return
	}
	select {
	case f := <-a.versionCh:
		major, minor, err := agwpe.ParseVersionResp(f)
		if err != nil {
			a.recordResult("version", false, fmt.Sprintf("parse error: %v", err))
			return
		}
		a.recordResult("version", true, fmt.Sprintf("v%d.%d", major, minor))
	case <-time.After(stepTimeout):
		a.recordResult("version", false, "timeout waiting for 'R' response")
	case <-ctx.Done():
	}
}

// testPortInfo sends 'G' and waits for the port-info response.
// It validates that the payload follows the AGWPE spec format:
//
//	"{numPorts};Port1 description;Port2 description;\x00"
//
// The first field must be a decimal port count, and the count must match the
// number of subsequent "PortN ..." fields.
func testPortInfo(ctx context.Context, a *appCtx, stepTimeout time.Duration) {
	if err := a.client.RequestPortInfo(); err != nil {
		a.recordResult("port info", false, fmt.Sprintf("send error: %v", err))
		return
	}
	select {
	case f := <-a.portInfoCh:
		if len(f.Data) == 0 {
			a.recordResult("port info", false, "empty response data")
			return
		}
		// Strip trailing NUL and split on semicolons.
		raw := strings.TrimRight(string(f.Data), "\x00")
		fields := strings.Split(raw, ";")
		// Remove empty trailing field produced by trailing ";".
		for len(fields) > 0 && fields[len(fields)-1] == "" {
			fields = fields[:len(fields)-1]
		}
		// First field must be a decimal count ≥ 1.
		if len(fields) < 2 {
			a.recordResult("port info", false,
				fmt.Sprintf("too few semicolon-delimited fields: %q", raw))
			return
		}
		var numPorts int
		if n, err := fmt.Sscanf(fields[0], "%d", &numPorts); n != 1 || err != nil {
			a.recordResult("port info", false,
				fmt.Sprintf("first field is not a port count: %q", fields[0]))
			return
		}
		if numPorts < 1 {
			a.recordResult("port info", false,
				fmt.Sprintf("port count must be ≥ 1, got %d", numPorts))
			return
		}
		portDescs := fields[1:]
		if len(portDescs) != numPorts {
			a.recordResult("port info", false,
				fmt.Sprintf("port count says %d but found %d port descriptions", numPorts, len(portDescs)))
			return
		}
		// Each description must start with "PortN " where N matches the 1-based index.
		for i, desc := range portDescs {
			expected := fmt.Sprintf("Port%d ", i+1)
			if !strings.HasPrefix(desc, expected) {
				a.recordResult("port info", false,
					fmt.Sprintf("port %d description %q does not start with %q", i+1, desc, expected))
				return
			}
		}
		a.recordResult("port info", true,
			fmt.Sprintf("%d port(s): %s", numPorts, strings.Join(portDescs, "; ")))
	case <-time.After(stepTimeout):
		a.recordResult("port info", false, "timeout waiting for 'G' response")
	case <-ctx.Done():
	}
}

// portCapSummary decodes the 12-byte 'g' port capabilities payload into a
// concise human-readable string per the AGWPE spec:
//
//	+00 OnAirBaud (0=1200, 1=2400, 2=4800, 3=9600, 4=19200, …)
//	+01 TrafficLevel (0xFF = not in auto-update mode)
//	+02 TxDelay  (units of 10 ms)
//	+03 TxTail   (units of 10 ms)
//	+04 Persist
//	+05 SlotTime (units of 10 ms)
//	+06 MaxFrame
//	+07 Active connections
//	+08..+11 Bytes received in last 2 min (little-endian uint32)
func portCapSummary(data []byte) string {
	if len(data) < 8 {
		return fmt.Sprintf("short data (%d bytes)", len(data))
	}
	baudTable := []string{"1200", "2400", "4800", "9600", "19200", "38400", "76800", "153600"}
	baudIdx := int(data[0])
	baud := "unknown"
	if baudIdx < len(baudTable) {
		baud = baudTable[baudIdx] + "bps"
	} else {
		baud = fmt.Sprintf("baud-idx=%d", baudIdx)
	}
	traffic := ""
	if data[1] == 0xFF {
		traffic = "traffic=n/a"
	} else {
		traffic = fmt.Sprintf("traffic=%d%%", int(data[1])*100/255)
	}
	rxBytes := uint32(0)
	if len(data) >= 12 {
		rxBytes = binary.LittleEndian.Uint32(data[8:12])
	}
	return fmt.Sprintf("%s %s txd=%dms txtail=%dms persist=%d slot=%dms maxframe=%d conns=%d rx2min=%dB",
		baud, traffic,
		int(data[2])*10, int(data[3])*10,
		data[4], int(data[5])*10,
		data[6], data[7], rxBytes)
}

// testPortCap sends 'g' and waits for the port-capabilities response.
func testPortCap(ctx context.Context, a *appCtx, stepTimeout time.Duration) {
	if err := a.client.RequestPortCap(a.agwpePort); err != nil {
		a.recordResult("port capabilities", false, fmt.Sprintf("send error: %v", err))
		return
	}
	select {
	case f := <-a.portCapCh:
		a.recordResult("port capabilities", true, portCapSummary(f.Data))
	case <-time.After(stepTimeout):
		a.recordResult("port capabilities", false, "timeout waiting for 'g' response")
	case <-ctx.Done():
	}
}

// testHeard sends 'H' and waits for heard-station frames.
// Note: Direwolf does not implement 'H'; this test skips on timeout
// rather than failing.
func testHeard(ctx context.Context, a *appCtx, stepTimeout time.Duration) {
	if err := a.client.SendFrame(agwpe.BuildHeardReq(a.agwpePort)); err != nil {
		a.recordResult("heard stations", false, fmt.Sprintf("send error: %v", err))
		return
	}
	select {
	case <-a.heardCh:
		count := 1
		for i := 0; i < 19; i++ {
			select {
			case <-a.heardCh:
				count++
			default:
			}
		}
		a.recordResult("heard stations", true, fmt.Sprintf("received %d response frames", count))
	case <-time.After(stepTimeout):
		a.recordSkip("heard stations", "no response - server may not implement 'H' (e.g. Direwolf)")
	case <-ctx.Done():
	}
}

// testOutstandingPort sends 'y' (outstanding frames on a port) and waits for
// the reply.
func testOutstandingPort(ctx context.Context, a *appCtx, stepTimeout time.Duration) {
	if err := a.client.SendFrame(&agwpe.Frame{Port: a.agwpePort, Kind: 'y'}); err != nil {
		a.recordResult("outstanding frames (port)", false, fmt.Sprintf("send error: %v", err))
		return
	}
	select {
	case f := <-a.outstandingPortCh:
		count, err := agwpe.ParseOutstandingResp(f)
		if err != nil {
			a.recordResult("outstanding frames (port)", false, fmt.Sprintf("parse error: %v", err))
			return
		}
		a.recordResult("outstanding frames (port)", true, fmt.Sprintf("outstanding=%d", count))
	case <-time.After(stepTimeout):
		a.recordResult("outstanding frames (port)", false, "timeout waiting for 'y' response")
	case <-ctx.Done():
	}
}

// testToggleMonitor sends 'm' to enable monitor mode. The spec defines no
// response frame; we check for RF activity arriving within one second.
func testToggleMonitor(ctx context.Context, a *appCtx, stepTimeout time.Duration) {
	if err := a.client.ToggleMonitor(); err != nil {
		a.recordResult("toggle monitor", false, fmt.Sprintf("send error: %v", err))
		return
	}
	a.monitorOn = true
	select {
	case f := <-a.monitorCh:
		a.recordResult("toggle monitor", true,
			fmt.Sprintf("monitoring active (first frame kind='%c' from=%s)", f.Kind, f.CallFrom))
	case <-time.After(time.Second):
		a.recordResult("toggle monitor", true, "sent successfully (no RF activity observed)")
	case <-ctx.Done():
	}
}

// testRegisterCall sends 'X' to register the local callsign and waits for the
// response. Direwolf always returns success (Data[0]==1).
func testRegisterCall(ctx context.Context, a *appCtx, stepTimeout time.Duration) {
	if err := a.client.SendFrame(agwpe.BuildRegisterCall(a.agwpePort, a.localCall)); err != nil {
		a.recordResult("register callsign", false, fmt.Sprintf("send error: %v", err))
		return
	}
	select {
	case f := <-a.registerCh:
		if len(f.Data) == 0 {
			a.recordResult("register callsign", false, "empty response data")
			return
		}
		if f.Data[0] == 1 {
			a.recordResult("register callsign", true,
				fmt.Sprintf("registered %s on port %d", f.CallFrom, a.agwpePort))
		} else {
			a.recordResult("register callsign", false,
				fmt.Sprintf("registration rejected for %s (already in use?)", f.CallFrom))
		}
	case <-time.After(stepTimeout):
		a.recordResult("register callsign", false, "timeout waiting for 'X' response")
	case <-ctx.Done():
	}
}

// testConnect sends 'C' to establish an AX.25 connection to -remote and waits
// for the 'C' connected response. Opens the rx channel before connecting so
// that incoming 'D' frames are buffered immediately.
func testConnect(ctx context.Context, a *appCtx, connectTimeout time.Duration) {
	a.openRxCh()
	if err := a.client.SendFrame(agwpe.BuildConnectReq(a.agwpePort, a.localCall, a.remoteCall)); err != nil {
		a.recordResult("connect", false, fmt.Sprintf("send error: %v", err))
		a.closeRxCh()
		return
	}
	select {
	case f := <-a.connectedCh:
		msg := strings.TrimRight(string(f.Data), "\r\x00")
		a.recordResult("connect", true, msg)
		a.connected = true
	case <-time.After(connectTimeout):
		a.recordResult("connect", false, "timeout waiting for 'C' connected response")
		a.closeRxCh()
	case <-ctx.Done():
		a.closeRxCh()
	}
}

// testOutstandingConn sends 'Y' to query outstanding frames on the active
// connection.
func testOutstandingConn(ctx context.Context, a *appCtx, stepTimeout time.Duration) {
	if err := a.client.SendFrame(&agwpe.Frame{
		Port:     a.agwpePort,
		Kind:     'Y',
		CallFrom: a.localCall,
		CallTo:   a.remoteCall,
	}); err != nil {
		a.recordResult("outstanding frames (conn)", false, fmt.Sprintf("send error: %v", err))
		return
	}
	select {
	case f := <-a.outstandingConnCh:
		if len(f.Data) < 4 {
			a.recordResult("outstanding frames (conn)", false,
				fmt.Sprintf("data too short: %d bytes", len(f.Data)))
			return
		}
		count := binary.LittleEndian.Uint32(f.Data[0:4])
		a.recordResult("outstanding frames (conn)", true, fmt.Sprintf("outstanding=%d", count))
	case <-time.After(stepTimeout):
		a.recordResult("outstanding frames (conn)", false, "timeout waiting for 'Y' response")
	case <-ctx.Done():
	}
}

// testSendData sends a single 'D' frame and optionally reads back an echo.
// A timeout is treated as PASS since the remote may not echo.
func testSendData(ctx context.Context, a *appCtx, stepTimeout time.Duration) {
	if err := a.client.SendFrame(
		agwpe.BuildSendData(a.agwpePort, a.localCall, a.remoteCall, 0xF0, []byte("\r")),
	); err != nil {
		a.recordResult("send data", false, fmt.Sprintf("send error: %v", err))
		return
	}
	ch := a.getRxCh()
	if ch == nil {
		a.recordResult("send data", false, "no rx channel")
		return
	}
	select {
	case data := <-ch:
		a.recordResult("send data", true, fmt.Sprintf("echo received (%d bytes)", len(data)))
	case <-time.After(stepTimeout):
		a.recordResult("send data", true, "sent OK (no echo from remote)")
	case <-ctx.Done():
	}
}

// testDisconnect sends 'd' to tear down the AX.25 connection.
func testDisconnect(ctx context.Context, a *appCtx, connectTimeout time.Duration) {
	if err := a.client.SendFrame(agwpe.BuildDisconnectReq(a.agwpePort, a.localCall, a.remoteCall)); err != nil {
		a.recordResult("disconnect", false, fmt.Sprintf("send error: %v", err))
		return
	}
	select {
	case f := <-a.disconnCh:
		msg := strings.TrimRight(string(f.Data), "\r\x00")
		a.recordResult("disconnect", true, msg)
		a.connected = false
	case <-time.After(connectTimeout):
		a.recordResult("disconnect", false, "timeout waiting for 'd' disconnected response")
	case <-ctx.Done():
	}
}

// ── Teardown ──────────────────────────────────────────────────────────────────

func teardown(a *appCtx) {
	if a.monitorOn {
		_ = a.client.ToggleMonitor() // toggle monitor back off
	}
	_ = a.client.SendFrame(agwpe.BuildUnregisterCall(a.agwpePort, a.localCall))
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	var (
		localCall      = flag.String("local", "", "local callsign, e.g. N7GET-9 (required)")
		remoteCall     = flag.String("remote", "", "remote callsign for connection tests (optional)")
		agwpeAddr      = flag.String("agwpe", "localhost:8000", "AGWPE server host:port")
		agwpePort      = flag.Int("port", 0, "AGWPE radio port number (0 = port 1)")
		connectTimeout = flag.Int("connect-timeout", 15000, "connection timeout in ms")
		stepTimeout    = flag.Int("step-timeout", 5000, "per-test timeout in ms")
		loginUser      = flag.String("login-user", "", "AGWPE login username (empty = skip login)")
		loginPass      = flag.String("login-pass", "", "AGWPE login password")
		heard          = flag.Bool("heard", false, "enable heard stations test (default: skip)")
		debug          = flag.Bool("debug", false, "enable debug logging")
	)
	flag.Parse()

	if *localCall == "" {
		fmt.Fprintln(os.Stderr, "error: -local is required")
		flag.Usage()
		os.Exit(1)
	}

	if *debug {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}

	host, portStr, ok := strings.Cut(*agwpeAddr, ":")
	if !ok {
		fmt.Fprintf(os.Stderr, "error: invalid -agwpe address %q, expected host:port\n", *agwpeAddr)
		os.Exit(1)
	}
	serverPort, err := parsePort(portStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid port in -agwpe: %v\n", err)
		os.Exit(1)
	}

	a := newAppCtx()
	a.localCall = strings.ToUpper(*localCall)
	a.remoteCall = strings.ToUpper(*remoteCall)
	a.agwpePort = uint8(*agwpePort)

	cfg := agwpe.ClientConfig{
		Host:      host,
		Port:      uint16(serverPort),
		OnRxFrame: a.handleFrame,
		OnError: func(e error) {
			slog.Error("AGWPE client error", "err", e)
		},
	}
	client, err := agwpe.NewClient(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating client: %v\n", err)
		os.Exit(1)
	}
	a.client = client

	client.Start()
	defer func() {
		teardown(a)
		client.Stop()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sig:
			cancel()
		case <-ctx.Done():
		}
	}()

	// allow TCP connection to establish
	select {
	case <-time.After(500 * time.Millisecond):
	case <-ctx.Done():
		return
	}

	stepDur := time.Duration(*stepTimeout) * time.Millisecond
	connDur := time.Duration(*connectTimeout) * time.Millisecond

	testLogin(ctx, a, *loginUser, *loginPass)
	testVersion(ctx, a, stepDur)
	testPortInfo(ctx, a, stepDur)
	testPortCap(ctx, a, stepDur)
	if *heard {
		testHeard(ctx, a, stepDur)
	} else {
		a.recordSkip("heard stations", "disabled (use -heard to enable)")
	}
	testOutstandingPort(ctx, a, stepDur)
	testToggleMonitor(ctx, a, stepDur)
	testRegisterCall(ctx, a, stepDur)

	if a.remoteCall != "" {
		testConnect(ctx, a, connDur)
		if a.connected {
			testOutstandingConn(ctx, a, stepDur)
			testSendData(ctx, a, stepDur)
			testDisconnect(ctx, a, connDur)
		} else {
			a.recordSkip("outstanding frames (conn)", "not connected")
			a.recordSkip("send data", "not connected")
			a.recordSkip("disconnect", "not connected")
		}
	} else {
		a.recordSkip("connect", "no -remote provided")
		a.recordSkip("outstanding frames (conn)", "no -remote provided")
		a.recordSkip("send data", "no -remote provided")
		a.recordSkip("disconnect", "no -remote provided")
	}

	fmt.Printf("\n%d tests run, %d failed, %d skipped\n", a.testsRun, a.testsFailed, a.testsSkipped)
	if a.testsFailed > 0 {
		os.Exit(1)
	}
}

func parsePort(s string) (int, error) {
	var p int
	_, err := fmt.Sscan(s, &p)
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("invalid port %q", s)
	}
	return p, nil
}
