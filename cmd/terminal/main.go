// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

// terminal is a connected-mode terminal over AGWPE, KISS TCP, or serial KISS.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/n7get/go-ax25/agwpe"
	"github.com/n7get/go-ax25/ax25"
	"github.com/n7get/go-ax25/phy"
	"go.bug.st/serial"
)

type ifaceMode int

const (
	modeAGWPE ifaceMode = iota
	modeKISS
	modeSerial
)

func (m ifaceMode) String() string {
	switch m {
	case modeAGWPE:
		return "agwpe"
	case modeKISS:
		return "kiss"
	default:
		return "serial"
	}
}

type cliArgs struct {
	configPath  string
	debug       bool
	info        bool
	help        bool
	interfaces  bool
	agwpe       bool
	kiss        bool
	serial      bool
	server      string
	port        int
	device      string
	local       string
	destination string
	digis       []string
}

func main() {
	args := parseFlags()
	if args.help {
		flag.Usage()
		return
	}

	logLevel := slog.LevelWarn
	if args.info {
		logLevel = slog.LevelInfo
	}
	if args.debug {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	cfg := ax25.NewConfig(nil)
	if err := cfg.LoadINI(args.configPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to load config %q: %v\n", args.configPath, err)
		os.Exit(1)
	}

	if args.interfaces {
		printInterfaces(cfg)
		return
	}

	mode, err := selectInterface(args, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}

	local, err := resolveLocalAddress(args.local, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid local callsign: %v\n", err)
		os.Exit(2)
	}

	var remote *ax25.Address
	if args.destination != "" {
		r, err := ax25.ParseAddress(args.destination)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: invalid destination %q: %v\n", args.destination, err)
			os.Exit(2)
		}
		remote = &r
	}

	via := make([]ax25.Address, 0, len(args.digis))
	for _, d := range args.digis {
		a, err := ax25.ParseAddress(d)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: invalid digipeater %q: %v\n", d, err)
			os.Exit(2)
		}
		via = append(via, a)
	}

	slog.Info("terminal starting",
		"mode", mode.String(),
		"local", local.String(),
		"destination", args.destination,
		"digis", len(via),
	)

	switch mode {
	case modeAGWPE:
		if err := runAGWPETerminal(cfg, args, local, remote, via); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case modeKISS:
		if remote == nil {
			fmt.Fprintln(os.Stderr, "error: passive mode is not supported with -kiss")
			os.Exit(2)
		}
		if err := runKISSTCPTerminal(cfg, args, local, *remote, via); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case modeSerial:
		if err := runSerialTerminal(cfg, args, local, remote, via); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}
}

func parseFlags() cliArgs {
	var args cliArgs
	flag.StringVar(&args.configPath, "config", "ax25.ini", "path to ax25.ini")
	flag.BoolVar(&args.debug, "debug", false, "enable debug logging")
	flag.BoolVar(&args.info, "info", false, "enable info logging")
	flag.BoolVar(&args.help, "help", false, "print help")
	flag.BoolVar(&args.interfaces, "interfaces", false, "print enabled interface modes and exit")

	flag.BoolVar(&args.agwpe, "agwpe", false, "use AGWPE interface")
	flag.BoolVar(&args.kiss, "kiss", false, "use KISS TCP interface")
	flag.BoolVar(&args.serial, "serial", false, "use serial KISS interface")

	flag.StringVar(&args.server, "server", "", "override server host for -agwpe/-kiss")
	flag.IntVar(&args.port, "port", 0, "override server port for -agwpe/-kiss")
	flag.StringVar(&args.device, "device", "", "override serial device for -serial")
	flag.StringVar(&args.local, "local", "", "local callsign override")

	flag.Parse()
	if extras := flag.Args(); len(extras) > 0 {
		args.destination = extras[0]
	}
	if extras := flag.Args(); len(extras) > 1 {
		args.digis = append(args.digis, extras[1:]...)
	}
	return args
}

func printInterfaces(cfg *ax25.Config) {
	kissEnabled := cfg.GetBool(ax25.KeyKissClientEnabled)
	serialEnabled := cfg.GetBool(ax25.KeyKissSerialEnabled)

	fmt.Fprintf(os.Stdout, "agwpe: enabled\n")
	fmt.Fprintf(os.Stdout, "kiss: %s\n", enabledLabel(kissEnabled))
	fmt.Fprintf(os.Stdout, "serial: %s\n", enabledLabel(serialEnabled))
}

func enabledLabel(v bool) string {
	if v {
		return "enabled"
	}
	return "disabled"
}

func selectInterface(args cliArgs, cfg *ax25.Config) (ifaceMode, error) {
	count := 0
	if args.agwpe {
		count++
	}
	if args.kiss {
		count++
	}
	if args.serial {
		count++
	}
	if count > 1 {
		return 0, fmt.Errorf("flags -agwpe, -kiss, -serial are mutually exclusive")
	}
	if args.agwpe {
		return modeAGWPE, nil
	}
	if args.kiss {
		return modeKISS, nil
	}
	if args.serial {
		return modeSerial, nil
	}

	// No flag: AGWPE is the default; KISS TCP and serial require explicit opt-in via config.
	if cfg.GetBool(ax25.KeyKissSerialEnabled) {
		return modeSerial, nil
	}
	if cfg.GetBool(ax25.KeyKissClientEnabled) {
		return modeKISS, nil
	}
	return modeAGWPE, nil
}

func resolveLocalAddress(localOverride string, cfg *ax25.Config) (ax25.Address, error) {
	if localOverride != "" {
		return ax25.ParseAddress(localOverride)
	}

	terminalCall := strings.TrimSpace(cfg.GetStr(ax25.KeyTerminalCallsign))
	if terminalCall != "" {
		return ax25.ParseAddress(terminalCall)
	}

	return ax25.Address{}, fmt.Errorf("no local callsign configured: use -local flag or set terminal.callsign in ax25.ini")
}

func connConfigFromCfg(cfg *ax25.Config) *ax25.ConnConfig {
	return &ax25.ConnConfig{
		T1:     time.Duration(cfg.GetInt(ax25.KeyConnT1Ms)) * time.Millisecond,
		T2:     time.Duration(cfg.GetInt(ax25.KeyConnT2Ms)) * time.Millisecond,
		T3:     time.Duration(cfg.GetInt(ax25.KeyConnT3Ms)) * time.Millisecond,
		N2:     cfg.GetInt(ax25.KeyConnN2Retries),
		Window: cfg.GetInt(ax25.KeyConnWindowSize),
	}
}

// processLine handles line-level and inline escape sequences:
//   - ~.           whole-line: disconnect signal
//   - ~~           whole-line: send a literal tilde
//   - ~!<file>     whole-line: read file, send each line (inline escapes expanded)
//   - \<a-z>       inline: control character (\a=\x01 … \z=\x1A)
//   - \<digits>    inline: byte value 0-255 in decimal
//   - \\           inline: literal backslash
//   - other        appended with \r as-is
func processLine(line string) ([][]byte, bool, bool, error) {
	// Whole-line escapes
	if line == "~." {
		return nil, true, false, nil
	}
	if line == "~~" {
		return [][]byte{[]byte("~\r")}, false, false, nil
	}
	if strings.HasPrefix(line, "~!") {
		filename := line[2:]
		if filename == "" {
			return nil, false, false, fmt.Errorf("~! requires a filename")
		}
		content, err := os.ReadFile(filename)
		if err != nil {
			return nil, false, false, err
		}
		fileLines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
		result := make([][]byte, 0, len(fileLines))
		for _, fl := range fileLines {
			expanded := expandInline(fl)
			result = append(result, expanded)
		}
		return result, false, true, nil
	}

	expanded := expandInline(line)
	return [][]byte{expanded}, false, false, nil
}

// expandInline expands inline backslash escapes within a single line and appends \r.
func expandInline(line string) []byte {
	var out []byte
	i := 0
	for i < len(line) {
		if line[i] != '\\' || i+1 >= len(line) {
			out = append(out, line[i])
			i++
			continue
		}
		next := line[i+1]
		switch {
		case next >= 'a' && next <= 'z':
			// \a -> \x01 … \z -> \x1A
			out = append(out, next-'a'+1)
			i += 2
		case next >= '0' && next <= '9':
			// \<digits> -> decimal byte value
			j := i + 1
			for j < len(line) && line[j] >= '0' && line[j] <= '9' {
				j++
			}
			num, err := strconv.ParseInt(line[i+1:j], 10, 16)
			if err != nil || num < 0 || num > 255 {
				// Invalid numeric escape: pass through literally so normal typing is never dropped.
				out = append(out, line[i:j]...)
				i = j
				continue
			}
			out = append(out, byte(num))
			i = j
		case next == '\\':
			out = append(out, '\\')
			i += 2
		default:
			// Unknown escape: pass through literally
			out = append(out, '\\', next)
			i += 2
		}
	}
	return append(out, '\r')
}

const (
	fileBurstLineDelay   = 120 * time.Millisecond
	connSendRetryDelay   = 75 * time.Millisecond
	agwOutstandingPoll   = 200 * time.Millisecond
	agwOutstandingWait   = 2 * time.Second
	agwOutstandingSettle = 100 * time.Millisecond
	disconnectWait       = 5 * time.Second
)

func waitForDisconnectAck(done <-chan struct{}) {
	select {
	case <-done:
	case <-time.After(disconnectWait):
	}
}

func waitForConnectedOrDone(connected, done <-chan struct{}) bool {
	select {
	case <-connected:
		return true
	case <-done:
		return false
	}
}

func shouldPaceStdin() bool {
	return !stdinIsTTY()
}

func splitPayload(data []byte, maxChunk int) [][]byte {
	if len(data) <= maxChunk {
		return [][]byte{data}
	}
	chunks := make([][]byte, 0, (len(data)+maxChunk-1)/maxChunk)
	for start := 0; start < len(data); start += maxChunk {
		end := start + maxChunk
		if end > len(data) {
			end = len(data)
		}
		chunk := make([]byte, end-start)
		copy(chunk, data[start:end])
		chunks = append(chunks, chunk)
	}
	return chunks
}

func sendConnPayloads(conn *ax25.Conn, payloads [][]byte) error {
	for i, data := range payloads {
		for _, chunk := range splitPayload(data, ax25.MaxInfoLen) {
			for {
				err := conn.SendData(chunk)
				if err == nil {
					break
				}
				if !errors.Is(err, ax25.ErrConnSendBufFull) {
					return err
				}
				time.Sleep(connSendRetryDelay)
			}
		}
		if len(payloads) > 1 && i < len(payloads)-1 {
			time.Sleep(fileBurstLineDelay)
		}
	}
	return nil
}

func echoPayloads(payloads [][]byte) {
	for _, data := range payloads {
		line := data
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
		_, _ = os.Stdout.Write(line)
		_, _ = os.Stdout.Write([]byte(nativeLineEnding()))
	}
}

type connSession struct {
	conn          *ax25.Conn
	router        *ax25.Router
	appPort       *ax25.Port
	doneCh        chan struct{}
	onceDone      sync.Once
	connectedCh   chan struct{}
	onceConnected sync.Once
	connected     atomic.Bool
	remote        atomic.Value // string
	disconnectCh  chan struct{}
}

func newConnSession(local ax25.Address, router *ax25.Router, appPort *ax25.Port, connCfg *ax25.ConnConfig) (*connSession, error) {
	s := &connSession{
		router:       router,
		appPort:      appPort,
		doneCh:       make(chan struct{}),
		connectedCh:  make(chan struct{}),
		disconnectCh: make(chan struct{}),
	}
	conn, err := ax25.NewConn(local, ax25.ConnCallbacks{
		OnConnect: func(remote ax25.Address, _ bool) {
			s.connected.Store(true)
			s.remote.Store(remote.String())
			s.onceConnected.Do(func() { close(s.connectedCh) })
			fmt.Fprintf(os.Stderr, "connected: %s\n", remote.String())
		},
		OnDisconnect: func() {
			if s.connected.Load() {
				fmt.Fprintln(os.Stderr, "remote disconnected")
			}
			s.connected.Store(false)
			s.onceDone.Do(func() { close(s.doneCh) })
		},
		OnError: func(err *ax25.ConnError) {
			if err != nil {
				fmt.Fprintf(os.Stderr, "conn error: %v\n", err)
			}
		},
		OnData: func(data []byte) {
			_, _ = os.Stdout.Write(normalizeInbound(data))
		},
		OnTxFrame: func(f *ax25.Frame) {
			if err := router.Send(f, appPort); err != nil {
				fmt.Fprintf(os.Stderr, "router send error: %v\n", err)
			}
		},
	}, connCfg)
	if err != nil {
		return nil, err
	}
	s.conn = conn
	return s, nil
}

func (s *connSession) runStdin() {
	if !waitForConnectedOrDone(s.connectedCh, s.doneCh) {
		return
	}
	paced := shouldPaceStdin()
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := sc.Text()
		payloads, shouldDisconnect, echoLocally, err := processLine(line)
		if shouldDisconnect {
			_ = s.conn.Shutdown()
			close(s.disconnectCh)
			return
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "escape error: %v\n", err)
			continue
		}
		if !s.connected.Load() {
			continue
		}
		if echoLocally {
			echoPayloads(payloads)
		}
		if err := sendConnPayloads(s.conn, payloads); err != nil {
			fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		}
		if paced {
			time.Sleep(fileBurstLineDelay)
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "stdin error: %v\n", err)
		return
	}
	slog.Info("stdin EOF, disconnecting")
	_ = s.conn.Shutdown()
	close(s.disconnectCh)
}

func runConnTerminalCore(s *connSession, remote *ax25.Address, via []ax25.Address) error {
	if remote != nil {
		if err := s.conn.Connect(*remote, via...); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(os.Stderr, "listening for incoming connection")
	}

	go s.runStdin()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case <-s.doneCh:
		return nil
	case <-s.disconnectCh:
		waitForDisconnectAck(s.doneCh)
		return nil
	case sig := <-sigCh:
		fmt.Fprintf(os.Stderr, "signal received: %s\n", sig.String())
		_ = s.conn.Shutdown()
		waitForDisconnectAck(s.doneCh)
		return nil
	}
}

func runSerialTerminal(cfg *ax25.Config, args cliArgs, local ax25.Address, remote *ax25.Address, via []ax25.Address) error {
	device := cfg.GetStr(ax25.KeyKissSerialDevice)
	if args.device != "" {
		device = args.device
	}
	baud := cfg.GetInt(ax25.KeyKissSerialBaud)

	rw, err := serial.Open(device, &serial.Mode{BaudRate: baud})
	if err != nil {
		return fmt.Errorf("open serial %q: %w", device, err)
	}

	kissPHY := ax25.NewKISSSerialPHY(rw, ax25.KISSSerialConfigFromConfig(cfg))
	router := ax25.NewRouter(ax25.RouterModeFromConfig(cfg))
	defer router.Close()

	var session *connSession
	appPort := &ax25.Port{Mode: ax25.PortModeStatic, Destination: local}
	appPort.OnRxFrame = func(frame *ax25.Frame) {
		if session != nil {
			_ = session.conn.OnFrame(frame)
		}
	}
	if err := router.RegisterPort(appPort); err != nil {
		return err
	}

	phyPort := &ax25.Port{
		Mode: ax25.PortModeDefault,
		OnRxFrame: func(frame *ax25.Frame) {
			if err := kissPHY.SendFrame(frame); err != nil {
				fmt.Fprintf(os.Stderr, "phy send error: %v\n", err)
			}
		},
	}
	if err := router.RegisterPort(phyPort); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := kissPHY.Start(ctx); err != nil {
		return err
	}
	defer kissPHY.Stop()

	go func() {
		for f := range kissPHY.RxFrames() {
			if err := router.Send(f, phyPort); err != nil {
				fmt.Fprintf(os.Stderr, "router rx error: %v\n", err)
			}
		}
	}()

	session, err = newConnSession(local, router, appPort, connConfigFromCfg(cfg))
	if err != nil {
		return err
	}
	defer session.conn.Close()

	return runConnTerminalCore(session, remote, via)
}

func runKISSTCPTerminal(cfg *ax25.Config, args cliArgs, local, remote ax25.Address, via []ax25.Address) error {
	router := ax25.NewRouter(ax25.RouterModeFromConfig(cfg))
	defer router.Close()

	var session *connSession
	appPort := &ax25.Port{Mode: ax25.PortModeStatic, Destination: local}
	appPort.OnRxFrame = func(frame *ax25.Frame) {
		if session != nil {
			_ = session.conn.OnFrame(frame)
		}
	}
	if err := router.RegisterPort(appPort); err != nil {
		return err
	}

	errCh := make(chan error, 1)
	var kissPHY *phy.KISSTCPClientPHY
	phyPort := &ax25.Port{
		Mode: ax25.PortModeDefault,
		OnRxFrame: func(frame *ax25.Frame) {
			if kissPHY == nil {
				return
			}
			if err := kissPHY.Send(frame); err != nil {
				if errors.Is(err, ax25.ErrNotConnected) {
					// Allow AX.25 timers to retry while transport is still coming up.
					return
				}
				select {
				case errCh <- err:
				default:
				}
			}
		},
	}
	if err := router.RegisterPort(phyPort); err != nil {
		return err
	}

	kcfg := phy.NewKISSTCPClientConfigFromConfig(cfg)
	if args.server != "" {
		kcfg.Host = args.server
	}
	if args.port > 0 {
		kcfg.Port = uint16(args.port)
	}
	kcfg.ReconnectDelay = 24 * time.Hour
	kcfg.OnRxFrame = func(f *ax25.Frame) {
		if err := router.Send(f, phyPort); err != nil {
			select {
			case errCh <- err:
			default:
			}
		}
	}
	kcfg.OnError = func(err error) {
		select {
		case errCh <- err:
		default:
		}
	}

	var err error
	kissPHY, err = phy.NewKISSTCPClientPHY(kcfg)
	if err != nil {
		return err
	}
	kissPHY.Start()
	defer kissPHY.Stop()
	if err := waitForKISSTCPConnected(kissPHY, 6*time.Second); err != nil {
		return err
	}

	session, err = newConnSession(local, router, appPort, connConfigFromCfg(cfg))
	if err != nil {
		return err
	}
	defer session.conn.Close()

	go func() {
		err := runConnTerminalCore(session, &remote, via)
		if err != nil {
			select {
			case errCh <- err:
			default:
			}
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-session.doneCh:
		return nil
	case <-session.disconnectCh:
		waitForDisconnectAck(session.doneCh)
		return nil
	}
}

func waitForKISSTCPConnected(kissPHY *phy.KISSTCPClientPHY, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if kissPHY.IsConnected() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("kiss tcp transport not connected after %s", timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type agwSession struct {
	client        *agwpe.Client
	local         string
	remote        atomic.Value // string
	connected     atomic.Bool
	doneCh        chan struct{}
	onceDone      sync.Once
	connectedCh   chan struct{}
	onceConnected sync.Once
	outCh         chan uint32
}

func runAGWPETerminal(cfg *ax25.Config, args cliArgs, local ax25.Address, remote *ax25.Address, via []ax25.Address) error {
	agwCfg := agwpe.NewClientConfigFromConfig(cfg)
	if args.server != "" {
		agwCfg.Host = args.server
	}
	if args.port > 0 {
		agwCfg.Port = uint16(args.port)
	}
	agwCfg.ReconnectDelay = 24 * time.Hour

	s := &agwSession{
		local:       local.String(),
		doneCh:      make(chan struct{}),
		connectedCh: make(chan struct{}),
		outCh:       make(chan uint32, 1),
	}
	agwCfg.OnRxFrame = func(f *agwpe.Frame) { s.onFrame(f) }
	agwCfg.OnError = func(err error) {
		fmt.Fprintf(os.Stderr, "agwpe transport error: %v\n", err)
		s.onceDone.Do(func() { close(s.doneCh) })
	}

	client, err := agwpe.NewClient(agwCfg)
	if err != nil {
		return err
	}
	s.client = client
	client.Start()
	defer client.Stop()

	if err := retrySend(client, agwpe.BuildRegisterCall(0, s.local), 6*time.Second); err != nil {
		return fmt.Errorf("register callsign: %w", err)
	}

	if remote != nil {
		if len(via) > 0 {
			digis := make([]string, 0, len(via))
			for _, a := range via {
				digis = append(digis, a.String())
			}
			if err := retrySend(client, agwpe.BuildConnectViaReq(0, s.local, remote.String(), digis), 6*time.Second); err != nil {
				return fmt.Errorf("connect via: %w", err)
			}
		} else {
			if err := retrySend(client, agwpe.BuildConnectReq(0, s.local, remote.String()), 6*time.Second); err != nil {
				return fmt.Errorf("connect: %w", err)
			}
		}
	} else {
		fmt.Fprintln(os.Stderr, "listening for incoming connection")
	}

	go s.stdinLoop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case <-s.doneCh:
		return nil
	case sig := <-sigCh:
		fmt.Fprintf(os.Stderr, "signal received: %s\n", sig.String())
		if s.connected.Load() {
			_ = s.disconnect()
		}
		waitForDisconnectAck(s.doneCh)
		return nil
	}
}

func (s *agwSession) onFrame(f *agwpe.Frame) {
	if f == nil {
		return
	}
	switch f.Kind {
	case agwpe.KindConnectResp:
		incoming := f.CallFrom
		if !s.connected.Load() {
			s.remote.Store(incoming)
			s.connected.Store(true)
			s.onceConnected.Do(func() { close(s.connectedCh) })
			fmt.Fprintf(os.Stderr, "connected: %s\n", incoming)
			return
		}
		current, _ := s.remote.Load().(string)
		if current != "" && incoming != current {
			_ = s.client.SendFrame(agwpe.BuildDisconnectReq(f.Port, s.local, incoming))
			fmt.Fprintf(os.Stderr, "refused second incoming session from %s\n", incoming)
		}
	case agwpe.KindRecvData:
		if !s.connected.Load() {
			return
		}
		current, _ := s.remote.Load().(string)
		if current != "" && f.CallFrom != current {
			return
		}
		_, _ = os.Stdout.Write(normalizeInbound(f.Data))
	case agwpe.KindOutstandingResp, agwpe.KindOutstandingReq:
		count, err := parseOutstandingCount(f)
		if err != nil {
			return
		}
		select {
		case s.outCh <- count:
		default:
			select {
			case <-s.outCh:
			default:
			}
			s.outCh <- count
		}
	case agwpe.KindDisconnectResp:
		if s.connected.Load() {
			fmt.Fprintln(os.Stderr, "remote disconnected")
		}
		s.connected.Store(false)
		s.onceDone.Do(func() { close(s.doneCh) })
	}
}

func (s *agwSession) stdinLoop() {
	if !waitForConnectedOrDone(s.connectedCh, s.doneCh) {
		return
	}
	paced := shouldPaceStdin()
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := sc.Text()
		payloads, shouldDisconnect, echoLocally, err := processLine(line)
		if shouldDisconnect {
			_ = s.disconnect()
			s.onceDone.Do(func() { close(s.doneCh) })
			return
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "escape error: %v\n", err)
			continue
		}
		if !s.connected.Load() {
			continue
		}
		remote, _ := s.remote.Load().(string)
		if remote == "" {
			continue
		}
		if echoLocally {
			echoPayloads(payloads)
		}
		if err := s.sendPayloads(remote, payloads); err != nil {
			fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		}
		if paced {
			time.Sleep(fileBurstLineDelay)
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "stdin error: %v\n", err)
		return
	}
	slog.Info("stdin EOF, disconnecting")
	_ = s.disconnect()
	s.onceDone.Do(func() { close(s.doneCh) })
}

func (s *agwSession) disconnect() error {
	if !s.connected.Load() {
		return nil
	}
	remote, _ := s.remote.Load().(string)
	if remote == "" {
		return nil
	}
	return s.client.SendFrame(agwpe.BuildDisconnectReq(0, s.local, remote))
}

func (s *agwSession) sendPayloads(remote string, payloads [][]byte) error {
	for _, data := range payloads {
		chunks := splitPayload(data, ax25.MaxInfoLen)
		for _, chunk := range chunks {
			if err := retrySend(s.client, agwpe.BuildSendData(0, s.local, remote, 0xF0, chunk), 6*time.Second); err != nil {
				return err
			}
			if err := s.waitForOutstanding(remote); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *agwSession) waitForOutstanding(remote string) error {
	// Let the server account for the just-sent payload before polling.
	time.Sleep(agwOutstandingSettle)
	zeroCount := 0
	for {
		count, err := s.queryOutstanding(remote)
		if err != nil {
			return err
		}
		if count == 0 {
			zeroCount++
			if zeroCount >= 2 {
				return nil
			}
		} else {
			zeroCount = 0
		}
		time.Sleep(agwOutstandingPoll)
	}
}

func (s *agwSession) queryOutstanding(remote string) (uint32, error) {
	for {
		select {
		case <-s.outCh:
		default:
			goto drained
		}
	}

drained:
	// Query per-connection outstanding count. Some AGWPE servers answer with 'Y',
	// others with 'y', so onFrame accepts either.
	if err := retrySend(s.client, &agwpe.Frame{Port: 0, Kind: agwpe.KindOutstandingReq, CallFrom: s.local, CallTo: remote}, 6*time.Second); err != nil {
		return 0, err
	}
	select {
	case count := <-s.outCh:
		return count, nil
	case <-time.After(agwOutstandingWait):
		return 0, fmt.Errorf("timeout waiting for agwpe outstanding response")
	}
}

func parseOutstandingCount(f *agwpe.Frame) (uint32, error) {
	if f == nil {
		return 0, fmt.Errorf("nil outstanding frame")
	}
	if f.Kind != agwpe.KindOutstandingResp && f.Kind != agwpe.KindOutstandingReq {
		return 0, fmt.Errorf("wrong outstanding kind %q", f.Kind)
	}
	if len(f.Data) < 4 {
		return 0, fmt.Errorf("short outstanding frame")
	}
	return uint32(f.Data[0]) | uint32(f.Data[1])<<8 | uint32(f.Data[2])<<16 | uint32(f.Data[3])<<24, nil
}

func retrySend(client *agwpe.Client, frame *agwpe.Frame, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		err := client.SendFrame(frame)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ax25.ErrNotConnected) && !errors.Is(err, ax25.ErrTXQueueFull) {
			return err
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func normalizeInbound(data []byte) []byte {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	if nativeLineEnding() != "\n" {
		s = strings.ReplaceAll(s, "\n", nativeLineEnding())
	}
	return []byte(s)
}

func stdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return true
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func nativeLineEnding() string {
	if runtime.GOOS == "windows" {
		return "\r\n"
	}
	return "\n"
}

func _openTCP(addr string) (io.ReadWriteCloser, error) {
	return net.Dial("tcp", addr)
}
