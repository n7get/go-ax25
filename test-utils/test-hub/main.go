package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/n7get/go-ax25/agwpe"
	"github.com/n7get/go-ax25/ax25"
	"github.com/n7get/go-ax25/cmd/ax25-bbs/bbs"
	"github.com/n7get/go-ax25/cmd/ax25-bbs/heard"
	"github.com/n7get/go-ax25/cmd/ax25-bbs/store"
)

const (
	testRadioPort = 0
	digiCallsign  = "RELAY"
	rxQueueSize   = 128
)

type rxCollector struct {
	mu   sync.Mutex
	rxCh chan []byte
}

func (r *rxCollector) open() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rxCh = make(chan []byte, rxQueueSize)
}

func (r *rxCollector) close() {
	r.mu.Lock()
	ch := r.rxCh
	r.rxCh = nil
	r.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

func (r *rxCollector) enqueue(data []byte) {
	r.mu.Lock()
	ch := r.rxCh
	r.mu.Unlock()
	if ch == nil {
		return
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	select {
	case ch <- cp:
	default:
		slog.Warn("test-hub: rx queue full, dropping bytes", "len", len(cp))
	}
}

func (r *rxCollector) channel() <-chan []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rxCh
}

type byteReader struct {
	ctx context.Context
	ch  <-chan []byte
	buf []byte
}

func (r *byteReader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 {
		select {
		case b, ok := <-r.ch:
			if !ok {
				return 0, io.EOF
			}
			r.buf = b
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		}
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

func readUntilPrompt(ctx context.Context, ch <-chan []byte, prompt string) (string, error) {
	if prompt == "" {
		return "", fmt.Errorf("empty prompt")
	}
	var capture strings.Builder
	window := make([]byte, 0, len(prompt))
	br := &byteReader{ctx: ctx, ch: ch}
	one := make([]byte, 1)

	for {
		n, err := br.Read(one)
		if n > 0 {
			b := one[0]
			capture.WriteByte(b)
			if len(window) < len(prompt) {
				window = append(window, b)
			} else {
				copy(window, window[1:])
				window[len(window)-1] = b
			}
			if len(window) == len(prompt) && string(window) == prompt {
				return capture.String(), nil
			}
		}
		if err != nil {
			return capture.String(), err
		}
	}
}

func parseHostPort(addr string) (string, uint16, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	p, err := strconv.Atoi(portStr)
	if err != nil || p < 1 || p > 65535 {
		return "", 0, fmt.Errorf("invalid port in %q", addr)
	}
	return host, uint16(p), nil
}

func handleBBSFrame(mgr *bbs.SessionManager, hl *heard.List, bbsCall string, f *agwpe.Frame) {
	if f == nil {
		return
	}
	switch f.Kind {
	case agwpe.KindConnectResp:
		mgr.OnConnect(f.CallFrom, f.CallTo, f.Port)
	case agwpe.KindRecvData:
		mgr.OnData(f.CallFrom, f.CallTo, f.Data)
	case agwpe.KindDisconnectResp:
		mgr.OnDisconnect(f.CallFrom, f.CallTo)
	case agwpe.KindRecvUnproto, agwpe.KindRecvIFrame, agwpe.KindRecvSupervisory:
		if f.CallFrom != "" && f.CallFrom != bbsCall {
			hl.Add(f.CallFrom)
		}
	}
}

func main() {
	agwpeAddr := flag.String("agwpe", "127.0.0.1:18000", "AGWPE listen address for embedded server")
	localCall := flag.String("local", "N7GET-9", "test client callsign")
	remoteCall := flag.String("remote", "N7GET-2", "BBS callsign")
	withDigi := flag.Bool("with-digi", false, "enable ConnectVia with fixed digi RELAY")
	connectTimeoutMs := flag.Int("connect-timeout", 15000, "connect timeout in ms")
	stepTimeoutMs := flag.Int("step-timeout", 10000, "step timeout in ms")
	debug := flag.Bool("debug", false, "enable debug logging")
	trace := flag.Bool("trace", false, "print per-hop forwarding counters at end")
	flag.Parse()

	logLevel := slog.LevelInfo
	if *debug {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	host, port, err := parseHostPort(*agwpeAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid -agwpe value: %v\n", err)
		os.Exit(2)
	}

	connectTimeout := time.Duration(*connectTimeoutMs) * time.Millisecond
	stepTimeout := time.Duration(*stepTimeoutMs) * time.Millisecond

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Router is always Hub mode for this test utility.
	hub := ax25.RouterModeHub
	router := ax25.NewRouter(&hub)
	defer router.Close()

	// Always configure digipeater with RELAY, regardless of -with-digi.
	digi, err := ax25.NewDigipeater(ax25.DigiConfig{Callsign: digiCallsign}, router, func(*ax25.Frame) error {
		// No external RF in this self-contained utility.
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to configure digipeater %s: %v\n", digiCallsign, err)
		os.Exit(1)
	}
	defer digi.Close()

	tcpServer := agwpe.NewTCPServer(agwpe.TCPServerConfig{
		Addr: *agwpeAddr,
		ServerConfig: agwpe.ServerConfig{
			Port:            int(port),
			RadioPort:       testRadioPort,
			PortDescription: "Port1 test-hub",
			TXQueueDepth:    64,
			MaxConns:        8,
			ReadBufSize:     agwpe.MaxFrameSize,
		},
	}, router)
	if err := tcpServer.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start AGWPE server on %s: %v\n", *agwpeAddr, err)
		os.Exit(1)
	}
	defer tcpServer.Stop()

	tempDir, err := os.MkdirTemp("", "test-hub-bbs-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tempDir)

	msgStore, err := store.NewSQLiteStore(filepath.Join(tempDir, "bbs.db"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open bbs store: %v\n", err)
		os.Exit(1)
	}
	defer msgStore.Close()

	heardList := heard.New(20)
	heardList.Add(*localCall) // Ensure J command has deterministic non-empty heard output.

	bbsCfg := bbs.BBSConfig{
		Callsign:              *remoteCall,
		Greeting:              "Welcome to test-hub BBS",
		Prompt:                "BBS> ",
		SysopName:             "SYSOP",
		Version:               "test-hub",
		MaxMessages:           500,
		MaxBodyLen:            102400,
		SysopSecret:           "",
		SysopChallengeTimeout: 300,
		SysopSessionTimeout:   600,
		SysopLockout:          900,
		SysopMaxAttempts:      3,
		DBPath:                filepath.Join(tempDir, "bbs.db"),
		AGWPEHost:             host,
		AGWPEPort:             port,
	}

	mgr := bbs.NewSessionManager(bbsCfg, msgStore, heardList)

	var cntToBBS, cntToClient atomic.Int64

	bbsClient, err := agwpe.NewClient(agwpe.ClientConfig{
		Host:           host,
		Port:           port,
		ConnectTimeout: 3 * time.Second,
		ReconnectDelay: 500 * time.Millisecond,
		TXQueueDepth:   16,
		ReadBufSize:    agwpe.MaxFrameSize,
		OnRxFrame: func(f *agwpe.Frame) {
			if *trace && f != nil && f.Kind == agwpe.KindRecvData {
				cntToBBS.Add(1)
			}
			handleBBSFrame(mgr, heardList, *remoteCall, f)
		},
		OnError: func(err error) {
			slog.Error("test-hub: bbs agwpe client", "err", err)
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create bbs agwpe client: %v\n", err)
		os.Exit(1)
	}
	mgr.SetAGWPEClient(bbsClient)
	bbsClient.Start()
	defer bbsClient.Stop()

	// Register BBS callsign once connected.
	regDeadline := time.Now().Add(connectTimeout)
	registered := false
	for time.Now().Before(regDeadline) {
		err = bbsClient.SendFrame(agwpe.BuildRegisterCall(testRadioPort, *remoteCall))
		if err == nil {
			registered = true
			break
		}
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "cancelled while waiting to register BBS callsign")
			os.Exit(1)
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !registered {
		fmt.Fprintln(os.Stderr, "timeout waiting to register BBS callsign")
		os.Exit(1)
	}
	_ = bbsClient.ToggleMonitor()

	testRx := &rxCollector{}
	testRx.open()
	defer testRx.close()
	connectedCh := make(chan struct{}, 1)
	disconnCh := make(chan struct{}, 1)

	testClient, err := agwpe.NewClient(agwpe.ClientConfig{
		Host:           host,
		Port:           port,
		ConnectTimeout: 3 * time.Second,
		ReconnectDelay: 500 * time.Millisecond,
		TXQueueDepth:   16,
		ReadBufSize:    agwpe.MaxFrameSize,
		OnRxFrame: func(f *agwpe.Frame) {
			if f == nil {
				return
			}
			switch f.Kind {
			case agwpe.KindConnectResp:
				select {
				case connectedCh <- struct{}{}:
				default:
				}
			case agwpe.KindRecvData:
				if *trace {
					cntToClient.Add(1)
				}
				testRx.enqueue(f.Data)
			case agwpe.KindDisconnectResp:
				testRx.close()
				select {
				case disconnCh <- struct{}{}:
				default:
				}
			}
		},
		OnError: func(err error) {
			slog.Error("test-hub: test agwpe client", "err", err)
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create test agwpe client: %v\n", err)
		os.Exit(1)
	}
	testClient.Start()
	defer testClient.Stop()

	var connectFrame *agwpe.Frame
	if *withDigi {
		connectFrame = agwpe.BuildConnectViaReq(testRadioPort, *localCall, *remoteCall, []string{digiCallsign})
	} else {
		connectFrame = agwpe.BuildConnectReq(testRadioPort, *localCall, *remoteCall)
	}

	connectSent := false
	connectSendDeadline := time.Now().Add(connectTimeout)
	for time.Now().Before(connectSendDeadline) {
		err = testClient.SendFrame(connectFrame)
		if err == nil {
			connectSent = true
			break
		}
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "[FAIL] connect - cancelled before request sent")
			os.Exit(1)
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !connectSent {
		fmt.Fprintf(os.Stderr, "connect send failed: %v\n", err)
		os.Exit(1)
	}

	select {
	case <-connectedCh:
		fmt.Printf("[PASS] connect - local=%s remote=%s with_digi=%v digi=%s\n", *localCall, *remoteCall, *withDigi, digiCallsign)
	case <-time.After(connectTimeout):
		fmt.Fprintf(os.Stderr, "[FAIL] connect - timeout (%v)\n", connectTimeout)
		os.Exit(1)
	case <-ctx.Done():
		fmt.Fprintln(os.Stderr, "[FAIL] connect - cancelled")
		os.Exit(1)
	}

	// Synchronize on initial BBS banner/prompt so subsequent capture is the J response.
	initialCtx, initialCancel := context.WithTimeout(ctx, stepTimeout)
	_, err = readUntilPrompt(initialCtx, testRx.channel(), bbsCfg.Prompt)
	initialCancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[FAIL] initial prompt - read error: %v\n", err)
		os.Exit(1)
	}

	if err := testClient.SendFrame(agwpe.BuildSendData(testRadioPort, *localCall, *remoteCall, 0xF0, []byte("J\r"))); err != nil {
		fmt.Fprintf(os.Stderr, "[FAIL] J command send failed: %v\n", err)
		os.Exit(1)
	}

	readCtx, readCancel := context.WithTimeout(ctx, stepTimeout)
	defer readCancel()
	captured, err := readUntilPrompt(readCtx, testRx.channel(), bbsCfg.Prompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[FAIL] J command - read error: %v\n", err)
		os.Exit(1)
	}

	if !strings.Contains(captured, "Heard:") {
		fmt.Fprintln(os.Stderr, "[FAIL] J command - missing Heard: output")
		fmt.Fprintln(os.Stderr, "----- captured output begin -----")
		fmt.Fprintln(os.Stderr, captured)
		fmt.Fprintln(os.Stderr, "----- captured output end -----")
		os.Exit(1)
	}
	fmt.Println("[PASS] J command - Heard output returned")

	_ = testClient.SendFrame(agwpe.BuildDisconnectReq(testRadioPort, *localCall, *remoteCall))
	select {
	case <-disconnCh:
		fmt.Println("[PASS] disconnect")
	case <-time.After(2 * time.Second):
		fmt.Println("[WARN] disconnect confirmation timeout")
	}

	if *trace {
		fmt.Printf("[TRACE] hub: to_client=%d to_bbs=%d\n", cntToClient.Load(), cntToBBS.Load())
	}
	fmt.Printf("Summary: PASS test-hub (router_mode=hub with_digi=%v digi=%s)\n", *withDigi, digiCallsign)
}
