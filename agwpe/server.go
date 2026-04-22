// Package agwpe implements the AGWPE (AGW Packet Engine) protocol.
// server.go - AGWPE server: bridges one TCP client to the AX.25 stack.
package agwpe

import (
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/n7get/go-ax25/ax25"
)

// ServerConfig configures an AGWPE server instance.
type ServerConfig struct {
	Port            int // TCP listen port
	RadioPort       int // AGWPE radio port index (0-based); almost always 0
	PortDescription string
	TXQueueDepth    int
	MaxConns        int
	ReadBufSize     int
	OnConnected     func(srv *Server)
	OnDisconnected  func(srv *Server)
}

// NewServerConfigFromConfig populates ServerConfig from ax25.Config.
func NewServerConfigFromConfig(cfg *ax25.Config) ServerConfig {
	return ServerConfig{
		Port:         cfg.GetInt(ax25.KeyAgwpeServerPort),
		TXQueueDepth: cfg.GetInt(ax25.KeyAgwpeServerTxQueueDepth),
		MaxConns:     cfg.GetInt(ax25.KeyAgwpeServerMaxConns),
		ReadBufSize:  cfg.GetInt(ax25.KeyAgwpeServerReadBuf),
	}
}

func (c *ServerConfig) portDesc() string {
	if c.PortDescription != "" {
		return c.PortDescription
	}
	return fmt.Sprintf("Port%d go-ax25 radio", c.RadioPort+1)
}

type connSlot struct {
	mu         sync.Mutex
	inUse      bool
	localCall  string
	remoteCall string
	conn       *ax25.Conn
	routerPort *ax25.Port
}

// listenerEntry holds a passive listener Conn for a registered AGWPE callsign.
// It stays registered with the router and accepts sequential incoming connections.
type listenerEntry struct {
	mu         sync.Mutex
	call       string
	remoteCall string // set to remote callsign while a session is active
	conn       *ax25.Conn
	routerPort *ax25.Port
}

// Server is an AGWPE server that bridges one TCP client to the AX.25 stack.
type Server struct {
	cfg    ServerConfig
	router *ax25.Router

	txCh chan Frame

	routerPort ax25.Port

	mu    sync.Mutex
	slots []*connSlot

	rawEnabled     atomic.Bool
	monitorEnabled atomic.Bool

	callsMu sync.Mutex
	calls   []string

	listenersMu sync.Mutex
	listeners   map[string]*listenerEntry

	pendingFrames []atomic.Int64

	netConnMu sync.Mutex
	netConn   net.Conn
}

// NewServer creates a new AGWPE server.
func NewServer(cfg ServerConfig, router *ax25.Router) *Server {
	if cfg.MaxConns <= 0 {
		cfg.MaxConns = 4
	}
	if cfg.TXQueueDepth <= 0 {
		cfg.TXQueueDepth = 64
	}
	if cfg.ReadBufSize <= 0 {
		cfg.ReadBufSize = MaxFrameSize
	}
	n := cfg.MaxConns
	s := &Server{
		cfg:           cfg,
		router:        router,
		txCh:          make(chan Frame, cfg.TXQueueDepth),
		slots:         make([]*connSlot, n),
		listeners:     make(map[string]*listenerEntry),
		pendingFrames: make([]atomic.Int64, n),
	}
	for i := range s.slots {
		s.slots[i] = &connSlot{}
	}
	return s
}

// HandleConn handles a single AGWPE TCP client connection.
func (s *Server) HandleConn(c net.Conn) {
	s.netConnMu.Lock()
	s.netConn = c
	s.netConnMu.Unlock()

	s.routerPort = ax25.Port{
		Mode:      ax25.PortModePromiscuous,
		OnRxFrame: s.onRouterFrame,
	}
	if err := s.router.RegisterPort(&s.routerPort); err != nil {
		slog.Warn("agwpe server: could not register router port", "err", err)
	}

	if s.cfg.OnConnected != nil {
		s.cfg.OnConnected(s)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.txPump(c)
	}()

	s.rxLoop(c)

	_ = s.router.UnregisterPort(&s.routerPort)
	close(s.txCh)
	wg.Wait()
	s.txCh = make(chan Frame, s.cfg.TXQueueDepth)

	s.disconnectAllSlots()
	s.teardownAllListeners()

	// Reset toggle states for next connection.
	s.rawEnabled.Store(false)
	s.monitorEnabled.Store(false)

	s.netConnMu.Lock()
	s.netConn = nil
	s.netConnMu.Unlock()

	if s.cfg.OnDisconnected != nil {
		s.cfg.OnDisconnected(s)
	}
}

func (s *Server) rxLoop(c net.Conn) {
	dec := NewDecoder(func(f *Frame) {
		s.handleClientFrame(f)
	})
	buf := make([]byte, s.cfg.ReadBufSize)
	for {
		n, err := c.Read(buf)
		if n > 0 {
			_, _ = dec.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func (s *Server) txPump(c net.Conn) {
	for f := range s.txCh {
		slog.Debug("agwpe tx", "kind", string(f.Kind), "from", f.CallFrom, "to", f.CallTo, "data_len", len(f.Data))
		b, err := f.Encode()
		if err != nil {
			slog.Warn("agwpe server: encode error", "err", err)
			continue
		}
		if _, err := c.Write(b); err != nil {
			return
		}
	}
}

func (s *Server) enqueue(f Frame) {
	select {
	case s.txCh <- f:
	default:
		slog.Warn("agwpe server: TX queue full, dropping frame", "kind", string(f.Kind))
	}
}

func (s *Server) onRouterFrame(f *ax25.Frame) {
	if s.rawEnabled.Load() {
		s.sendRawToClient(f)
	}
	if s.monitorEnabled.Load() {
		s.sendMonitorToClient(f)
	}
}

func (s *Server) handleClientFrame(f *Frame) {
	if f == nil {
		return
	}
	slog.Debug("agwpe rx", "kind", string(f.Kind), "from", f.CallFrom, "to", f.CallTo, "data_len", len(f.Data))
	switch f.Kind {
	case KindLogin:
		// 'P' login — silently accept (no response per spec).
	case 'R':
		s.handleVersionReq()
	case 'G':
		s.handlePortInfoReq()
	case 'g':
		s.handlePortCapReq()
	case 'H':
		// Heard request — not yet implemented.
	case 'X':
		s.handleRegisterCall(f)
	case 'x':
		s.handleUnregisterCall(f)
	case 'k':
		s.rawEnabled.Store(!s.rawEnabled.Load())
	case 'm':
		s.monitorEnabled.Store(!s.monitorEnabled.Load())
	case 'K':
		s.handleSendRaw(f)
	case 'V':
		s.handleSendUnprotoVia(f)
	case 'M':
		s.handleSendUnproto(f)
	case 'C':
		s.handleConnect(f, nil)
	case 'v':
		s.handleConnectVia(f)
	case 'c':
		s.handleConnectPID(f)
	case 'D':
		s.handleSendData(f)
	case 'd':
		s.handleDisconnect(f)
	case 'y':
		s.handleOutstandingPort()
	case 'Y':
		s.handleOutstandingConn(f)
	default:
		slog.Debug("agwpe server: unknown command", "kind", string(f.Kind))
	}
}

func (s *Server) handleVersionReq() {
	s.enqueue(BuildVersionResp(2005, 127))
}

func (s *Server) handlePortInfoReq() {
	s.enqueue(BuildPortInfoResp(s.cfg.RadioPort, 1, s.cfg.portDesc()))
}

func (s *Server) handlePortCapReq() {
	s.enqueue(BuildPortCapResp(s.cfg.RadioPort))
}

func (s *Server) handleRegisterCall(f *Frame) {
	call := f.CallFrom
	s.callsMu.Lock()
	found := false
	for _, c := range s.calls {
		if c == call {
			found = true
			break
		}
	}
	if !found {
		s.calls = append(s.calls, call)
	}
	s.callsMu.Unlock()
	if !found {
		s.setupListener(call)
	}
	s.enqueue(BuildRegisterCallResp(call, true, uint8(s.cfg.RadioPort)))
}

func (s *Server) handleUnregisterCall(f *Frame) {
	call := f.CallFrom
	s.callsMu.Lock()
	for i, c := range s.calls {
		if c == call {
			s.calls = append(s.calls[:i], s.calls[i+1:]...)
			break
		}
	}
	s.callsMu.Unlock()
	s.teardownListener(call)
}

// setupListener creates a passive ax25.Conn for the registered callsign and
// registers a static router port so that incoming SABMs are delivered to it.
func (s *Server) setupListener(callsign string) {
	addr, err := ax25.ParseAddress(callsign)
	if err != nil {
		slog.Warn("agwpe server: invalid callsign for listener", "callsign", callsign, "err", err)
		return
	}

	entry := &listenerEntry{call: callsign}

	conn, err := ax25.NewConn(addr, ax25.ConnCallbacks{
		OnConnect: func(remote ax25.Address, localInitiated bool) {
			entry.mu.Lock()
			entry.remoteCall = remote.String()
			entry.mu.Unlock()
			s.enqueue(BuildConnectedResp(s.cfg.RadioPort, callsign, remote.String(), false))
		},
		OnDisconnect: func() {
			entry.mu.Lock()
			remoteCall := entry.remoteCall
			entry.remoteCall = ""
			entry.mu.Unlock()
			s.enqueue(BuildDisconnectedResp(s.cfg.RadioPort, callsign, remoteCall))
			// Don't unregister the port: keep the Conn in Disconnected state
			// so it can accept the next incoming SABM.
		},
		OnData: func(data []byte) {
			entry.mu.Lock()
			remoteCall := entry.remoteCall
			entry.mu.Unlock()
			s.enqueue(BuildConnectedData(s.cfg.RadioPort, callsign, remoteCall, data))
		},
		OnError: func(err *ax25.ConnError) {
			slog.Warn("agwpe server: listener conn error", "callsign", callsign, "err", err)
		},
		OnTxFrame: func(frame *ax25.Frame) {
			if s.monitorEnabled.Load() {
				s.sendOwnFrameToClient(frame)
			}
			if err := s.router.Send(frame, &s.routerPort); err != nil {
				slog.Warn("agwpe server: listener router send failed", "callsign", callsign, "err", err)
			}
		},
	}, nil)
	if err != nil {
		slog.Warn("agwpe server: NewConn failed for listener", "callsign", callsign, "err", err)
		return
	}

	entry.conn = conn
	entry.routerPort = &ax25.Port{
		Mode:        ax25.PortModeStatic,
		Destination: addr,
		OnRxFrame: func(f *ax25.Frame) {
			if err := conn.OnFrame(f); err != nil {
				slog.Warn("agwpe server: listener conn.OnFrame error", "callsign", callsign, "err", err)
			}
		},
	}

	if err := s.router.RegisterPort(entry.routerPort); err != nil {
		slog.Warn("agwpe server: register listener port failed", "callsign", callsign, "err", err)
		return
	}

	s.listenersMu.Lock()
	s.listeners[callsign] = entry
	s.listenersMu.Unlock()
}

// teardownListener unregisters and closes the passive listener for callsign.
func (s *Server) teardownListener(callsign string) {
	s.listenersMu.Lock()
	entry := s.listeners[callsign]
	delete(s.listeners, callsign)
	s.listenersMu.Unlock()

	if entry == nil {
		return
	}
	if entry.routerPort != nil {
		_ = s.router.UnregisterPort(entry.routerPort)
	}
	if entry.conn != nil {
		entry.conn.Close()
	}
}

// teardownAllListeners closes all passive listener Conns and unregisters their
// router ports. Called when the AGWPE TCP client disconnects.
func (s *Server) teardownAllListeners() {
	s.listenersMu.Lock()
	entries := make([]*listenerEntry, 0, len(s.listeners))
	for _, e := range s.listeners {
		entries = append(entries, e)
	}
	s.listeners = make(map[string]*listenerEntry)
	s.listenersMu.Unlock()

	s.callsMu.Lock()
	s.calls = nil
	s.callsMu.Unlock()

	for _, entry := range entries {
		if entry.routerPort != nil {
			_ = s.router.UnregisterPort(entry.routerPort)
		}
		if entry.conn != nil {
			entry.conn.Close()
		}
	}
}

func (s *Server) handleSendRaw(f *Frame) {
	axFrame, err := ToAX25(f)
	if err != nil {
		slog.Warn("agwpe server: bad raw frame", "err", err)
		return
	}
	if err := s.router.Send(axFrame, &s.routerPort); err != nil {
		slog.Warn("agwpe server: router send failed", "err", err)
	}
}

func (s *Server) handleSendUnproto(f *Frame) {
	frame, err := toAX25Unproto(f)
	if err != nil {
		slog.Warn("agwpe server: bad unproto frame", "err", err)
		return
	}
	if err := s.router.Send(frame, &s.routerPort); err != nil {
		slog.Warn("agwpe server: router send failed", "err", err)
	}
}

func (s *Server) handleSendUnprotoVia(f *Frame) {
	s.handleSendUnproto(f)
}

func (s *Server) handleConnect(f *Frame, digis []ax25.Address) {
	localCall := f.CallFrom
	remoteCall := f.CallTo

	slot := s.allocSlot(localCall, remoteCall)
	if slot == nil {
		slog.Warn("agwpe server: no free connection slots")
		return
	}
	slotIdx := s.slotIndex(slot)

	localAddr, err := ax25.ParseAddress(localCall)
	if err != nil {
		slog.Warn("agwpe server: invalid local address", "addr", localCall, "err", err)
		s.freeSlot(slot)
		return
	}
	remoteAddr, err := ax25.ParseAddress(remoteCall)
	if err != nil {
		slog.Warn("agwpe server: invalid remote address", "addr", remoteCall, "err", err)
		s.freeSlot(slot)
		return
	}

	conn, err := ax25.NewConn(localAddr, ax25.ConnCallbacks{
		OnConnect: func(remote ax25.Address, localInitiated bool) {
			s.enqueue(BuildConnectedResp(s.cfg.RadioPort, localCall, remoteCall, localInitiated))
		},
		OnDisconnect: func() {
			s.enqueue(BuildDisconnectedResp(s.cfg.RadioPort, localCall, remoteCall))
			slot.mu.Lock()
			cp := slot.routerPort
			slot.routerPort = nil
			slot.mu.Unlock()
			if cp != nil {
				_ = s.router.UnregisterPort(cp)
			}
			s.freeSlot(slot)
		},
		OnData: func(data []byte) {
			s.enqueue(BuildConnectedData(s.cfg.RadioPort, localCall, remoteCall, data))
			if slotIdx >= 0 {
				s.pendingFrames[slotIdx].Add(-1)
			}
		},
		OnError: func(err *ax25.ConnError) {
			slog.Warn("agwpe server: conn error", "err", err)
		},
		OnTxFrame: func(frame *ax25.Frame) {
			if s.monitorEnabled.Load() {
				s.sendOwnFrameToClient(frame)
			}
			if err := s.router.Send(frame, &s.routerPort); err != nil {
				slog.Warn("agwpe server: router send failed", "err", err)
			}
		},
	}, nil)
	if err != nil {
		slog.Warn("agwpe server: NewConn failed", "err", err)
		s.freeSlot(slot)
		return
	}

	slot.mu.Lock()
	slot.conn = conn
	slot.mu.Unlock()

	connPort := &ax25.Port{
		Mode:        ax25.PortModeStatic,
		Destination: localAddr,
		OnRxFrame: func(f *ax25.Frame) {
			if err := conn.OnFrame(f); err != nil {
				slog.Warn("agwpe server: conn.OnFrame error", "err", err)
			}
		},
	}
	if err := s.router.RegisterPort(connPort); err != nil {
		slog.Warn("agwpe server: register conn port", "err", err)
		s.freeSlot(slot)
		return
	}
	slot.mu.Lock()
	slot.routerPort = connPort
	slot.mu.Unlock()

	if err := conn.Connect(remoteAddr, digis...); err != nil {
		slog.Warn("agwpe server: Connect failed", "err", err)
		_ = s.router.UnregisterPort(connPort)
		s.freeSlot(slot)
	}
}

func (s *Server) handleConnectVia(f *Frame) {
	digis := parseDigiPath(f.Data)
	s.handleConnect(f, digis)
}

func (s *Server) handleConnectPID(f *Frame) {
	s.handleConnect(f, nil)
}

func (s *Server) handleSendData(f *Frame) {
	localCall := f.CallFrom
	remoteCall := f.CallTo

	// Check active slot first: an outgoing connection takes priority over a
	// passive listener registered under the same callsign.
	slot := s.findSlot(localCall, remoteCall)
	if slot != nil {
		slot.mu.Lock()
		conn := slot.conn
		slot.mu.Unlock()
		if conn != nil {
			if err := conn.SendData(f.Data); err != nil {
				slog.Warn("agwpe server: conn send failed", "err", err)
			}
			idx := s.slotIndex(slot)
			if idx >= 0 {
				s.pendingFrames[idx].Add(1)
			}
		}
		return
	}

	// Fall back to passive listener (for callsigns that only accept incoming).
	s.listenersMu.Lock()
	listenerEntry := s.listeners[localCall]
	s.listenersMu.Unlock()
	if listenerEntry != nil {
		if err := listenerEntry.conn.SendData(f.Data); err != nil {
			slog.Warn("agwpe server: listener send failed", "local", localCall, "err", err)
		}
		return
	}

	slog.Warn("agwpe server: send data: no matching slot", "local", localCall, "remote", remoteCall)
}

func (s *Server) handleDisconnect(f *Frame) {
	localCall := f.CallFrom
	remoteCall := f.CallTo

	// Check active slot first: an outgoing connection takes priority over a
	// passive listener registered under the same callsign.
	slot := s.findSlot(localCall, remoteCall)
	if slot != nil {
		slot.mu.Lock()
		conn := slot.conn
		slot.mu.Unlock()
		if conn != nil {
			_ = conn.Shutdown()
		}
		return
	}

	// Fall back to passive listener.
	s.listenersMu.Lock()
	listenerEntry := s.listeners[localCall]
	s.listenersMu.Unlock()
	if listenerEntry != nil {
		_ = listenerEntry.conn.Shutdown()
	}
}

func (s *Server) handleOutstandingPort() {
	s.enqueue(BuildOutstandingPort(s.cfg.RadioPort, 0))
}

func (s *Server) handleOutstandingConn(f *Frame) {
	localCall := f.CallTo
	remoteCall := f.CallFrom
	var pending int64
	s.mu.Lock()
	for i, slot := range s.slots {
		slot.mu.Lock()
		if slot.inUse && slot.localCall == localCall && slot.remoteCall == remoteCall {
			pending = s.pendingFrames[i].Load()
			slot.mu.Unlock()
			break
		}
		slot.mu.Unlock()
	}
	s.mu.Unlock()
	s.enqueue(BuildOutstandingConn(s.cfg.RadioPort, localCall, remoteCall, int(pending)))
}

func (s *Server) sendRawToClient(f *ax25.Frame) {
	agwpeFrame, err := FromAX25Raw(f, uint8(s.cfg.RadioPort))
	if err != nil {
		return
	}
	// Change kind to 'K' for received raw (same byte, but semantically
	// this is the server sending a received raw frame to the client).
	s.enqueue(*agwpeFrame)
}

func (s *Server) sendMonitorToClient(f *ax25.Frame) {
	agwpeFrame, err := FromAX25Monitor(f, s.cfg.RadioPort)
	if err != nil {
		return
	}
	s.enqueue(agwpeFrame)
}

// sendOwnFrameToClient enqueues a monitor frame for a frame we transmitted,
// using kind 'T' (KindRecvOwn) per the AGWPE spec.
func (s *Server) sendOwnFrameToClient(f *ax25.Frame) {
	agwpeFrame, err := FromAX25Monitor(f, s.cfg.RadioPort)
	if err != nil {
		return
	}
	agwpeFrame.Kind = KindRecvOwn
	s.enqueue(agwpeFrame)
}

func (s *Server) allocSlot(localCall, remoteCall string) *connSlot {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, slot := range s.slots {
		slot.mu.Lock()
		if !slot.inUse {
			slot.inUse = true
			slot.localCall = localCall
			slot.remoteCall = remoteCall
			slot.conn = nil
			slot.mu.Unlock()
			return slot
		}
		slot.mu.Unlock()
	}
	return nil
}

func (s *Server) freeSlot(slot *connSlot) {
	slot.mu.Lock()
	defer slot.mu.Unlock()
	slot.inUse = false
	slot.conn = nil
	slot.localCall = ""
	slot.remoteCall = ""
}

func (s *Server) findSlot(localCall, remoteCall string) *connSlot {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, slot := range s.slots {
		slot.mu.Lock()
		if slot.inUse && slot.localCall == localCall && slot.remoteCall == remoteCall {
			slot.mu.Unlock()
			return slot
		}
		slot.mu.Unlock()
	}
	return nil
}

func (s *Server) slotIndex(target *connSlot) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, slot := range s.slots {
		if slot == target {
			return i
		}
	}
	return -1
}

func (s *Server) disconnectAllSlots() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, slot := range s.slots {
		slot.mu.Lock()
		if slot.inUse && slot.conn != nil {
			slot.conn.Close()
		}
		slot.inUse = false
		slot.conn = nil
		slot.localCall = ""
		slot.remoteCall = ""
		slot.mu.Unlock()
	}
}

func toAX25Unproto(f *Frame) (*ax25.Frame, error) {
	if f == nil {
		return nil, fmt.Errorf("agwpe server: nil unproto frame")
	}
	src, err := ax25.ParseAddress(f.CallFrom)
	if err != nil {
		return nil, fmt.Errorf("agwpe server: parse source: %w", err)
	}
	dst, err := ax25.ParseAddress(f.CallTo)
	if err != nil {
		return nil, fmt.Errorf("agwpe server: parse destination: %w", err)
	}
	payload := append([]byte(nil), f.Data...)
	var digis []ax25.Address
	if f.Kind == KindSendUnprotoVia {
		// Binary layout from client: [1 byte ndigi][ndigi * 10-byte callsigns][info]
		if len(payload) >= 1 {
			ndigi := int(payload[0])
			if len(payload) >= 1+ndigi*CallsignLen {
				for i := 0; i < ndigi; i++ {
					call := getCallsign(payload[1+i*CallsignLen : 1+(i+1)*CallsignLen])
					addr, err := ax25.ParseAddress(call)
					if err != nil {
						return nil, fmt.Errorf("agwpe server: parse digi: %w", err)
					}
					digis = append(digis, addr)
				}
				payload = payload[1+ndigi*CallsignLen:]
			}
		}
	}
	pid := f.PID
	if pid == 0 {
		pid = ax25.PIDNone
	}
	return &ax25.Frame{
		Source:      src,
		Destination: dst,
		Digipeaters: digis,
		Type:        ax25.FrameUI,
		Control:     ax25.CtrlUI,
		PID:         pid,
		Payload:     payload,
	}, nil
}

// parseDigiPath parses the data field of a 'v' (Connect VIA) frame.
// Direwolf format: [1 byte ndigi][ndigi * 10-byte null-padded callsigns]
func parseDigiPath(data []byte) []ax25.Address {
	if len(data) < 1 {
		return nil
	}
	ndigi := int(data[0])
	if ndigi == 0 || len(data) < 1+ndigi*CallsignLen {
		return nil
	}
	digis := make([]ax25.Address, 0, ndigi)
	for i := 0; i < ndigi; i++ {
		call := strings.TrimRight(string(data[1+i*CallsignLen:1+(i+1)*CallsignLen]), "\x00")
		addr, err := ax25.ParseAddress(call)
		if err == nil {
			digis = append(digis, addr)
		}
	}
	return digis
}
