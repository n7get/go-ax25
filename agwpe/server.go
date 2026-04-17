// Package agwpe implements the AGWPE (AGW Packet Engine) protocol.
// server.go - AGWPE server: bridges one TCP client to the AX.25 stack.
package agwpe

import (
	"bytes"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"

	"github.com/n7get/go-ax25/ax25"
)

// ServerConfig configures an AGWPE server instance.
type ServerConfig struct {
	Port            int
	PortDescription string
	TXQueueDepth    int
	MaxConns        int
	OnConnected     func(srv *Server)
	OnDisconnected  func(srv *Server)
}

const (
	defaultServerTXQueueDepth = 64
	defaultServerMaxConns     = 4
)

func (c *ServerConfig) txQueueDepth() int {
	if c.TXQueueDepth > 0 {
		return c.TXQueueDepth
	}
	return defaultServerTXQueueDepth
}

func (c *ServerConfig) maxConns() int {
	if c.MaxConns > 0 {
		return c.MaxConns
	}
	return defaultServerMaxConns
}

func (c *ServerConfig) portDesc() string {
	if c.PortDescription != "" {
		return c.PortDescription
	}
	return fmt.Sprintf("Port%d go-ax25 radio", c.Port+1)
}

type connSlot struct {
	mu         sync.Mutex
	inUse      bool
	localCall  string
	remoteCall string
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

	pendingFrames []atomic.Int64

	netConnMu sync.Mutex
	netConn   net.Conn
}

// NewServer creates a new AGWPE server.
func NewServer(cfg ServerConfig, router *ax25.Router) *Server {
	n := cfg.maxConns()
	s := &Server{
		cfg:           cfg,
		router:        router,
		txCh:          make(chan Frame, cfg.txQueueDepth()),
		slots:         make([]*connSlot, n),
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
	s.txCh = make(chan Frame, s.cfg.txQueueDepth())

	s.disconnectAllSlots()

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
	buf := make([]byte, MaxFrameSize)
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
	case 'P':
	case 'R':
		s.handleVersionReq()
	case 'G':
		s.handlePortInfoReq()
	case 'g':
		s.handlePortCapReq()
	case 'H':
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
	s.enqueue(BuildPortInfoResp(s.cfg.Port, 1, s.cfg.portDesc()))
}

func (s *Server) handlePortCapReq() {
	s.enqueue(BuildPortCapResp(s.cfg.Port))
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
	s.enqueue(BuildRegisterCallResp(call, true))
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
}

func (s *Server) handleSendRaw(f *Frame) {
	frame, err := ToAX25(f)
	if err != nil {
		slog.Warn("agwpe server: bad raw frame", "err", err)
		return
	}
	if err := s.router.Send(frame, &s.routerPort); err != nil {
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
			s.enqueue(BuildConnectedResp(s.cfg.Port, localCall, remoteCall, localInitiated))
		},
		OnDisconnect: func() {
			s.enqueue(BuildDisconnectedResp(s.cfg.Port, localCall, remoteCall))
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
			s.enqueue(BuildConnectedData(s.cfg.Port, localCall, remoteCall, data))
			if slotIdx >= 0 {
				s.pendingFrames[slotIdx].Add(-1)
			}
		},
		OnError: func(err *ax25.ConnError) {
			slog.Warn("agwpe server: conn error", "err", err)
		},
		OnTxFrame: func(frame *ax25.Frame) {
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
	slot := s.findSlot(localCall, remoteCall)
	if slot == nil {
		slog.Warn("agwpe server: send data: no matching slot", "local", localCall, "remote", remoteCall)
		return
	}
	slot.mu.Lock()
	conn := slot.conn
	slot.mu.Unlock()
	if conn == nil {
		return
	}
	if err := conn.SendData(f.Data); err != nil {
		slog.Warn("agwpe server: conn send failed", "err", err)
	}
	idx := s.slotIndex(slot)
	if idx >= 0 {
		s.pendingFrames[idx].Add(1)
	}
}

func (s *Server) handleDisconnect(f *Frame) {
	localCall := f.CallFrom
	remoteCall := f.CallTo
	slot := s.findSlot(localCall, remoteCall)
	if slot == nil {
		return
	}
	slot.mu.Lock()
	conn := slot.conn
	slot.mu.Unlock()
	if conn != nil {
		_ = conn.Shutdown()
	}
}

func (s *Server) handleOutstandingPort() {
	s.enqueue(BuildOutstandingPort(s.cfg.Port, 0))
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
	s.enqueue(BuildOutstandingConn(s.cfg.Port, localCall, remoteCall, int(pending)))
}

func (s *Server) sendRawToClient(f *ax25.Frame) {
	agwpeFrame, err := FromAX25Raw(f, uint8(s.cfg.Port))
	if err != nil {
		return
	}
	s.enqueue(*agwpeFrame)
}

func (s *Server) sendMonitorToClient(f *ax25.Frame) {
	var kind byte
	switch f.Type {
	case ax25.FrameUI:
		kind = 'U'
	case ax25.FrameI:
		kind = 'I'
	default:
		kind = 'S'
	}
	agwpeFrame, err := FromAX25Monitor(f, kind)
	if err != nil {
		return
	}
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
		parts := bytes.SplitN(payload, []byte{'\r'}, 2)
		if len(parts) == 2 {
			for _, digi := range bytes.Fields(parts[0]) {
				addr, err := ax25.ParseAddress(string(digi))
				if err != nil {
					return nil, fmt.Errorf("agwpe server: parse digi: %w", err)
				}
				digis = append(digis, addr)
			}
			payload = parts[1]
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

func parseDigiPath(data []byte) []ax25.Address {
	if len(data) == 0 {
		return nil
	}
	var digis []ax25.Address
	start := 0
	for i := 0; i <= len(data); i++ {
		if i == len(data) || data[i] == 0 {
			if i > start {
				addr, err := ax25.ParseAddress(string(data[start:i]))
				if err == nil {
					digis = append(digis, addr)
				}
			}
			start = i + 1
		}
	}
	return digis
}
