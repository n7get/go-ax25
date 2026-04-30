package main

// AX.25 scripted client example over KISS serial (or TCP).
//
// Flow:
//  1) Connect to a remote AX.25 station via a KISS TNC
//  2) Log incoming text until a line starts with "ENTER COMMAND:"
//  3) Send "j\r"
//  4) Log incoming text until a line starts with "ENTER COMMAND:"
//  5) Send "b\r"
//  6) Disconnect cleanly
//
// Architecture
// ============
// main() sets up the router, PHY, and Conn, then calls conn.Connect().
// When the connection is established, OnConnect creates a receive channel
// and spawns appTask as a goroutine.  appTask reads bytes line-by-line and
// runs the BBS interaction script.  OnData enqueues received chunks;
// OnDisconnect closes the channel so appTask exits naturally.

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/n7get/go-ax25/ax25"
	"go.bug.st/serial"
)

// ── constants ────────────────────────────────────────────────────────────────

const (
	prompt      = "ENTER COMMAND:"
	heardCmd    = "j\r"
	byeCmd      = "b\r"
	rxQueueSize = 32
	readTimeout = 60 * time.Second
)

// ── appCtx ───────────────────────────────────────────────────────────────────

// appCtx is shared between the ax25.Conn callbacks and appTask.
// All fields are set before Connect() is called, except rxCh which is
// created in OnConnect and closed in OnDisconnect.
type appCtx struct {
	conn    *ax25.Conn
	router  *ax25.Router
	appPort *ax25.Port

	mu     sync.Mutex
	rxCh   chan []byte   // nil when disconnected
	doneCh chan struct{} // closed when the session ends
}

// enqueue delivers a received data chunk to appTask without blocking.
// Called from OnData (must not block).
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
		log.Printf("CLIENT: RX queue full, dropping %d bytes", len(data))
	}
}

// ── Conn callbacks ────────────────────────────────────────────────────────────

func (a *appCtx) onConnect(remote ax25.Address, localInitiated bool) {
	slog.Debug("CLIENT: connected", "remote", remote.String(), "local_initiated", localInitiated)

	ch := make(chan []byte, rxQueueSize)
	a.mu.Lock()
	a.rxCh = ch
	a.mu.Unlock()

	go appTask(a, ch)
}

func (a *appCtx) onDisconnect() {
	slog.Debug("CLIENT: disconnected")

	a.mu.Lock()
	ch := a.rxCh
	a.rxCh = nil
	a.mu.Unlock()

	if ch != nil {
		close(ch)
	}
	if a.doneCh != nil {
		close(a.doneCh)
	}
}

func (a *appCtx) onError(err error) {
	slog.Error("CLIENT: error", "err", err)
}

func (a *appCtx) onData(data []byte) {
	slog.Debug("CLIENT: received data", "len", len(data), "data", string(data))
	a.enqueue(data)
}

func (a *appCtx) onTxFrame(frame ax25.Frame) {
	ax25.LogFrame(slog.LevelDebug, "CLIENT: onTxFrame", &frame)
	if err := a.router.Send(&frame, a.appPort); err != nil {
		slog.Error("CLIENT: tx error", "err", err)
	}
}

// ── line reader ───────────────────────────────────────────────────────────────

// byteReader wraps a channel of []byte chunks and implements io.Reader so
// that bufio.Scanner can read lines across chunk boundaries.
type byteReader struct {
	ch  <-chan []byte
	buf []byte
	ctx context.Context
}

func (r *byteReader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 {
		select {
		case chunk, ok := <-r.ch:
			if !ok {
				return 0, io.EOF
			}
			r.buf = chunk
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		}
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

// waitForPrompt reads lines from ch until one starts with prompt or the
// deadline is exceeded.  Lines are printed as they arrive.
func waitForPrompt(ctx context.Context, ch <-chan []byte, want string) error {
	br := &byteReader{ch: ch, ctx: ctx}
	scanner := bufio.NewScanner(br)
	scanner.Split(scanLinesLenient)

	for scanner.Scan() {
		line := scanner.Text()
		slog.Debug("BBS: line", "line", line)
		if strings.HasPrefix(line, want) {
			slog.Debug("BBS: matched prompt", "prompt", want)
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("readline: %w", err)
	}
	return fmt.Errorf("connection closed before prompt %q", want)
}

// scanLinesLenient is like bufio.ScanLines but also splits on bare '\r'.
func scanLinesLenient(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for i, b := range data {
		if b == '\n' {
			// strip trailing \r if present
			tok := data[:i]
			if len(tok) > 0 && tok[len(tok)-1] == '\r' {
				tok = tok[:len(tok)-1]
			}
			return i + 1, tok, nil
		}
		if b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// ── application goroutine ─────────────────────────────────────────────────────

func appTask(a *appCtx, ch <-chan []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout*2)
	defer cancel()

	// ── first prompt ──
	log.Printf("CLIENT: waiting for %q ...", prompt)
	promptCtx, promptCancel := context.WithTimeout(ctx, readTimeout)
	err := waitForPrompt(promptCtx, ch, prompt)
	promptCancel()
	if err != nil {
		log.Printf("CLIENT: timed out waiting for first prompt: %v", err)
		goto shutdown
	}

	log.Println("CLIENT: sending: j")
	if err := a.conn.SendData([]byte(heardCmd)); err != nil {
		log.Printf("CLIENT: SendData(heardCmd) failed: %v", err)
	}

	// ── second prompt ──
	log.Printf("CLIENT: waiting for %q ...", prompt)
	promptCtx, promptCancel = context.WithTimeout(ctx, readTimeout)
	err = waitForPrompt(promptCtx, ch, prompt)
	promptCancel()
	if err != nil {
		log.Printf("CLIENT: timed out waiting for second prompt: %v", err)
		goto shutdown
	}

	log.Println("CLIENT: sending: b")
	if err := a.conn.SendData([]byte(byeCmd)); err != nil {
		log.Printf("CLIENT: SendData(byeCmd) failed: %v", err)
	}

shutdown:
	log.Println("CLIENT: initiating disconnect")
	if err := a.conn.Shutdown(); err != nil {
		log.Printf("CLIENT: Shutdown error: %v", err)
	}
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	var (
		debug      = flag.Bool("debug", false, "enable debug logging")
		localCall  = flag.String("local", "", "local callsign, e.g. N0CALL-1 (required)")
		remoteCall = flag.String("remote", "", "remote callsign, e.g. W1AW-1 (required)")
		kissTCP    = flag.String("kiss-tcp", "", "KISS TCP host:port, e.g. 127.0.0.1:8001")
		kissSerial = flag.String("kiss-serial", "", "KISS serial device, e.g. /dev/ttyUSB0")
		serialBaud = flag.Int("baud", 9600, "serial baud rate (used with -kiss-serial)")
	)
	flag.Parse()

	if *debug {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}

	if *localCall == "" || *remoteCall == "" {
		slog.Error("Missing required flags: -local and -remote")
		fmt.Fprintln(os.Stderr, "error: -local and -remote are required")
		flag.Usage()
		os.Exit(2)
	}
	if *kissTCP == "" && *kissSerial == "" {
		slog.Error("Missing required flag: one of -kiss-tcp or -kiss-serial")
		fmt.Fprintln(os.Stderr, "error: one of -kiss-tcp or -kiss-serial is required")
		flag.Usage()
		os.Exit(2)
	}

	local, err := ax25.ParseAddress(*localCall)
	if err != nil {
		slog.Error("Invalid local callsign", "callsign", *localCall, "err", err)
		log.Fatalf("invalid local callsign %q: %v", *localCall, err)
	}
	remote, err := ax25.ParseAddress(*remoteCall)
	if err != nil {
		slog.Error("Invalid remote callsign", "callsign", *remoteCall, "err", err)
		log.Fatalf("invalid remote callsign %q: %v", *remoteCall, err)
	}

	// ── build PHY ──
	var phy ax25.PHY
	if *kissTCP != "" {
		slog.Debug("Connecting to KISS TCP", "addr", *kissTCP)
		conn, err := net.Dial("tcp", *kissTCP)
		if err != nil {
			slog.Error("Failed to dial KISS TCP", "addr", *kissTCP, "err", err)
			log.Fatalf("dial KISS TCP %q: %v", *kissTCP, err)
		}
		phy = ax25.NewKISSSerialPHY(conn, ax25.KISSSerialPHYConfig{})
		slog.Info("CLIENT: KISS TCP connected", "addr", *kissTCP)
	} else {
		slog.Debug("Opening KISS serial", "device", *kissSerial, "baud", *serialBaud)
		sp, err := openSerial(*kissSerial, *serialBaud)
		if err != nil {
			slog.Error("Failed to open serial", "device", *kissSerial, "baud", *serialBaud, "err", err)
			log.Fatalf("open serial %q: %v", *kissSerial, err)
		}
		phy = ax25.NewKISSSerialPHY(sp, ax25.KISSSerialPHYConfig{})
		slog.Info("CLIENT: KISS serial opened", "device", *kissSerial, "baud", *serialBaud)
	}

	// ── build router ──
	slog.Debug("Creating AX.25 router")
	router := ax25.NewRouter(nil)

	app := &appCtx{router: router, doneCh: make(chan struct{})}
	slog.Debug("App context initialized")

	// App port: static port matching our local address.
	// The router delivers incoming frames here; we pass them to conn.
	appPort := &ax25.Port{
		Destination: local,
		Mode:        ax25.PortModeStatic,
		OnRxFrame: func(frame *ax25.Frame) {
			ax25.LogFrame(slog.LevelDebug, "AppPort: received frame", frame)
			if app.conn != nil {
				app.conn.OnFrame(frame)
			}
		},
	}
	app.appPort = appPort
	if err := router.RegisterPort(appPort); err != nil {
		slog.Error("Failed to register app port", "err", err)
		log.Fatalf("register app port: %v", err)
	}

	// PHY port: default port that forwards outgoing frames from the router to the TNC.
	phyPort := &ax25.Port{
		Mode: ax25.PortModeDefault,
		OnRxFrame: func(frame *ax25.Frame) {
			ax25.LogFrame(slog.LevelDebug, "PHYPort: sending frame to PHY", frame)
			if err := phy.SendFrame(frame); err != nil {
				slog.Error("PHYPort: SendFrame error", "err", err)
			}
		},
	}
	if err := router.RegisterPort(phyPort); err != nil {
		slog.Error("Failed to register PHY port", "err", err)
		log.Fatalf("register PHY port: %v", err)
	}

	// Start the PHY read/write goroutines.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := phy.Start(ctx); err != nil {
		slog.Error("Failed to start PHY", "err", err)
		log.Fatalf("phy.Start: %v", err)
	}
	slog.Debug("PHY started")

	// Bridge PHY → router: forward frames received from the TNC into the router.
	go func() {
		for frame := range phy.RxFrames() {
			ax25.LogFrame(slog.LevelDebug, "PHY: received frame", frame)
			router.Send(frame, phyPort)
		}
	}()

	// ── build conn ──
	conn, err := ax25.NewConn(local, ax25.ConnCallbacks{
		OnConnect:    app.onConnect,
		OnDisconnect: app.onDisconnect,
		OnError:      func(err *ax25.ConnError) { app.onError(err) },
		OnData:       app.onData,
		OnTxFrame:    func(f *ax25.Frame) { app.onTxFrame(*f) },
	}, nil)
	if err != nil {
		slog.Error("Failed to create AX.25 connection", "err", err)
		log.Fatalf("NewConn: %v", err)
	}
	app.conn = conn
	slog.Debug("AX.25 connection created")

	// ── connect ──
	slog.Info("CLIENT: connecting", "remote", remote.String())
	if err := conn.Connect(remote); err != nil {
		slog.Error("Failed to connect", "remote", remote.String(), "err", err)
		log.Fatalf("Connect: %v", err)
	}

	// ── wait for Ctrl+C or natural completion ──
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case <-app.doneCh:
		slog.Info("CLIENT: session complete")
	case <-sigCh:
		slog.Info("CLIENT: interrupted, shutting down")
		conn.Shutdown()
	}

	slog.Debug("Stopping PHY and closing connection")
	phy.Stop()
	conn.Close()
}

// ── serial helper ─────────────────────────────────────────────────────────────

// openSerial opens a serial port using go.bug.st/serial.
func openSerial(device string, baud int) (io.ReadWriter, error) {
	return serial.Open(device, &serial.Mode{BaudRate: baud})
}
