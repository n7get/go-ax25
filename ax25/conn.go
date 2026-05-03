// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Connected-mode session (AX.25 v2.0)
// ---------------------------------------------------------------------------

// ConnState represents the state of a connected-mode session.
type ConnState int

const (
	ConnStateDisconnected       ConnState = iota
	ConnStateAwaitingConnection           // SABM sent, waiting for UA
	ConnStateAwaitingRelease              // DISC sent, waiting for UA/DM
	ConnStateConnected
	ConnStateTimerRecovery // T1 expired, polling peer
)

func (s ConnState) String() string {
	switch s {
	case ConnStateDisconnected:
		return "DISCONNECTED"
	case ConnStateAwaitingConnection:
		return "AWAITING_CONNECTION"
	case ConnStateAwaitingRelease:
		return "AWAITING_RELEASE"
	case ConnStateConnected:
		return "CONNECTED"
	case ConnStateTimerRecovery:
		return "TIMER_RECOVERY"
	default:
		return "UNKNOWN"
	}
}

// ConnError carries error information delivered to OnError.
type ConnError struct {
	Code       error
	Message    string
	RetryCount int
}

func (e *ConnError) Error() string { return fmt.Sprintf("%s: %v", e.Message, e.Code) }

// ConnCallbacks holds the application callbacks for a Conn.
type ConnCallbacks struct {
	// OnConnect is called when a connection is established.
	// isLocalInitiated is true if we sent the SABM.
	// Must not block.
	OnConnect func(remote Address, isLocalInitiated bool)

	// OnDisconnect is called when the session ends.
	// Must not block.
	OnDisconnect func()

	// OnLinkReset is called when SABM is received while already connected.
	// Must not block.
	OnLinkReset func()

	// OnError is called on non-fatal errors (e.g. retry exhausted).
	// Must not block.
	OnError func(err *ConnError)

	// OnData is called when I-frame payload data arrives.
	// Must not block.
	OnData func(data []byte)

	// OnTxFrame is called when a frame must be transmitted.
	// Required. Must not block.
	OnTxFrame func(f *Frame)
}

// ConnConfig holds timer and window parameters.
type ConnConfig struct {
	T1     time.Duration // Acknowledgement timeout (default 10s)
	T2     time.Duration // Response delay timeout (default 1s)
	T3     time.Duration // Inactive link timeout (default 3m)
	N2     int           // Maximum retry count (default 10)
	Window int           // Maximum outstanding I-frames, 1-7 (default 4)
}

func defaultConnConfig() ConnConfig {
	return ConnConfig{
		T1:     10 * time.Second,
		T2:     1 * time.Second,
		T3:     3 * time.Minute,
		N2:     10,
		Window: 4,
	}
}

// pendingFrame holds an unacknowledged I-frame payload.
type pendingFrame struct {
	data        []byte
	ns          uint8
	transmitted bool
}

// Conn is an AX.25 v2.0 connected-mode session.
//
// All public methods are goroutine-safe.
type Conn struct {
	mu  sync.Mutex
	cfg ConnConfig
	cbs ConnCallbacks

	localAddr  Address
	remoteAddr Address
	path       []Address // digipeater path

	state            ConnState
	isLocalInitiated bool
	refusePending    bool

	// Sequence numbers (mod 8)
	vs uint8 // V(S) send state
	vr uint8 // V(R) receive state
	va uint8 // V(A) acknowledge state

	// Flow control
	peerBusy   bool
	localBusy  bool
	rejSent    bool
	ackPending bool

	retryCount int

	// Unacknowledged I-frames (window)
	txQueue []pendingFrame

	// Timers
	ctx    context.Context
	cancel context.CancelFunc

	t1Timer *time.Timer
	t2Timer *time.Timer
	t3Timer *time.Timer

	t1Active bool
	t2Active bool
	t3Active bool

	pendingActions []func()
}

var (
	ErrConnNotConnected  = errors.New("ax25: not connected")
	ErrConnAlreadyActive = errors.New("ax25: connection already active")
	ErrConnSendBufFull   = errors.New("ax25: send buffer full")
	ErrConnInvalidArg    = errors.New("ax25: invalid argument")
)

// NewConn creates a new Conn with the given local address, callbacks, and config.
// If cfg is nil, defaults are used.
func NewConn(local Address, cbs ConnCallbacks, cfg *ConnConfig) (*Conn, error) {
	if cbs.OnData == nil || cbs.OnTxFrame == nil {
		return nil, fmt.Errorf("ax25: NewConn: OnData and OnTxFrame are required")
	}
	c := &Conn{
		localAddr: local,
		cbs:       cbs,
		state:     ConnStateDisconnected,
	}
	if cfg != nil {
		c.cfg = *cfg
	} else {
		c.cfg = defaultConnConfig()
	}
	if c.cfg.Window < 1 || c.cfg.Window > 7 {
		c.cfg.Window = 4
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	return c, nil
}

// Close tears down the session and releases resources.
func (c *Conn) Close() {
	c.mu.Lock()
	c.stopAllTimers()
	c.state = ConnStateDisconnected
	c.mu.Unlock()
	c.cancel()
}

// State returns the current connection state.
func (c *Conn) State() ConnState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// IsConnected reports whether the session is in CONNECTED or TIMER_RECOVERY state.
func (c *Conn) IsConnected() bool {
	s := c.State()
	return s == ConnStateConnected || s == ConnStateTimerRecovery
}

// Connect initiates a connection to remote, optionally via digipeaters.
func (c *Conn) Connect(remote Address, via ...Address) error {
	c.mu.Lock()
	if c.state != ConnStateDisconnected {
		c.mu.Unlock()
		return ErrConnAlreadyActive
	}
	c.remoteAddr = remote
	c.path = via
	c.isLocalInitiated = true
	c.resetStateVars()
	c.state = ConnStateAwaitingConnection
	c.retryCount = 0
	c.startT1()
	c.sendSABM(true)
	actions := c.takePendingActions()
	c.mu.Unlock()
	c.runPendingActions(actions)
	return nil
}

// Shutdown initiates a graceful disconnect.
func (c *Conn) Shutdown() error {
	c.mu.Lock()
	if c.state == ConnStateDisconnected {
		c.mu.Unlock()
		return ErrConnNotConnected
	}
	c.stopAllTimers()
	c.state = ConnStateAwaitingRelease
	c.retryCount = 0
	c.startT1()
	c.sendDISC(true)
	actions := c.takePendingActions()
	c.mu.Unlock()
	c.runPendingActions(actions)
	return nil
}

// SendData queues data for transmission as I-frames.
func (c *Conn) SendData(data []byte) error {
	c.mu.Lock()
	if c.state != ConnStateConnected && c.state != ConnStateTimerRecovery {
		c.mu.Unlock()
		return ErrConnNotConnected
	}
	if len(c.txQueue) >= c.cfg.Window {
		c.mu.Unlock()
		return ErrConnSendBufFull
	}
	payload := make([]byte, len(data))
	copy(payload, data)
	c.txQueue = append(c.txQueue, pendingFrame{data: payload})
	c.sendPendingIFrames()
	actions := c.takePendingActions()
	c.mu.Unlock()
	c.runPendingActions(actions)
	return nil
}

// Refuse may be called from within OnConnect to reject an incoming connection.
func (c *Conn) Refuse() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refusePending = true
}

// SetBusy signals local busy / not-busy state to the peer.
func (c *Conn) SetBusy(busy bool) {
	c.mu.Lock()
	if c.localBusy == busy {
		c.mu.Unlock()
		return
	}
	c.localBusy = busy
	if c.state == ConnStateConnected || c.state == ConnStateTimerRecovery {
		if busy {
			c.ackPending = false
			c.stopT2()
			c.sendRNR(false, false)
		} else {
			c.sendRR(false, false)
		}
	}
	actions := c.takePendingActions()
	c.mu.Unlock()
	c.runPendingActions(actions)
}

// OnFrame processes a received frame. Call this from the PHY/router receive path.
func (c *Conn) OnFrame(f *Frame) error {
	if f.Type == FrameUI {
		return nil // ignore UI frames
	}
	c.mu.Lock()
	var err error
	switch c.state {
	case ConnStateDisconnected:
		err = c.handleDisconnected(f)
	case ConnStateAwaitingConnection:
		err = c.handleAwaitingConnection(f)
	case ConnStateAwaitingRelease:
		err = c.handleAwaitingRelease(f)
	case ConnStateConnected:
		err = c.handleConnected(f)
	case ConnStateTimerRecovery:
		err = c.handleTimerRecovery(f)
	}
	actions := c.takePendingActions()
	c.mu.Unlock()
	c.runPendingActions(actions)
	return err
}

// ---------------------------------------------------------------------------
// State handlers (called with c.mu held)
// ---------------------------------------------------------------------------

func (c *Conn) handleDisconnected(f *Frame) error {
	if f.Type == FrameU && f.Control&0xEF == CtrlSABM {
		// Incoming connection request.
		c.remoteAddr = f.Source
		c.path = extractReturnPath(f)
		c.isLocalInitiated = false
		c.resetStateVars()
		c.state = ConnStateConnected
		c.refusePending = false
		// Fire OnConnect before replying so Refuse can reject the session.
		if c.cbs.OnConnect != nil {
			remote := c.remoteAddr
			onConnect := c.cbs.OnConnect
			c.mu.Unlock()
			onConnect(remote, false)
			c.mu.Lock()
		}
		if c.refusePending {
			c.sendDM(HasPF(f.Control))
			c.enterDisconnected()
			return nil
		}
		c.sendUA(HasPF(f.Control))
		c.startT3()
		return nil
	}
	// All other frames in disconnected state: send DM.
	c.remoteAddr = f.Source
	c.path = extractReturnPath(f)
	c.sendDM(HasPF(f.Control))
	return nil
}

func (c *Conn) handleAwaitingConnection(f *Frame) error {
	switch {
	case f.Type == FrameU && f.Control&0xEF == CtrlUA:
		// Connection accepted.
		c.stopT1()
		c.resetStateVars()
		c.state = ConnStateConnected
		if c.cbs.OnConnect != nil {
			remote := c.remoteAddr
			c.enqueueAction(func() { c.cbs.OnConnect(remote, true) })
		}
		c.startT3()
		c.sendPendingIFrames()
	case f.Type == FrameU && f.Control&0xEF == CtrlDM:
		// Connection refused.
		c.stopT1()
		c.fireError(ErrConnNotConnected, "connection refused (DM received)", 0)
		c.enterDisconnected()
	}
	return nil
}

func (c *Conn) handleAwaitingRelease(f *Frame) error {
	switch {
	case f.Type == FrameU && (f.Control&0xEF == CtrlUA || f.Control&0xEF == CtrlDM):
		c.stopT1()
		c.enterDisconnected()
	}
	return nil
}

func (c *Conn) handleConnected(f *Frame) error {
	switch f.Type {
	case FrameI:
		return c.handleIFrame(f)
	case FrameS:
		return c.handleSFrame(f)
	case FrameU:
		return c.handleUFrameConnected(f)
	}
	return nil
}

func (c *Conn) handleTimerRecovery(f *Frame) error {
	// Same as connected for most frames.
	return c.handleConnected(f)
}

func (c *Conn) handleIFrame(f *Frame) error {
	if c.localBusy {
		c.sendRNR(false, false)
		return nil
	}
	ns := ExtractNS(f.Control)
	nr := ExtractNR(f.Control)
	pf := HasPF(f.Control)

	// Validate N(R).
	if !c.isValidNR(nr) {
		return c.startNRErrorRecovery("invalid N(R) in I-frame")
	}
	c.handleReceivedNR(nr)

	if ns == c.vr {
		// In-sequence frame.
		c.vr = mod8(c.vr + 1)
		c.rejSent = false
		if c.cbs.OnData != nil && len(f.Payload) > 0 {
			data := make([]byte, len(f.Payload))
			copy(data, f.Payload)
			c.enqueueAction(func() { c.cbs.OnData(data) })
		}
		if pf {
			c.sendAck(true, false)
			c.ackPending = false
		} else {
			c.ackPending = true
			c.startT2()
		}
	} else {
		// Out-of-sequence frame.
		if !c.rejSent {
			c.rejSent = true
			c.sendREJ(pf, false)
		} else if pf {
			c.sendRR(true, false)
		}
	}
	return nil
}

func (c *Conn) handleSFrame(f *Frame) error {
	nr := ExtractNR(f.Control)
	pf := HasPF(f.Control)

	if !c.isValidNR(nr) {
		return c.startNRErrorRecovery("invalid N(R) in S-frame")
	}

	ctrlType := f.Control & 0x0F
	switch ctrlType {
	case CtrlRRMask:
		c.peerBusy = false
		c.handleReceivedNR(nr)
		if pf && c.state == ConnStateTimerRecovery {
			c.stopT1()
			c.state = ConnStateConnected
			c.sendPendingIFrames()
		} else if pf {
			c.sendRR(true, false)
		}
	case CtrlRNRMask:
		c.peerBusy = true
		c.handleReceivedNR(nr)
		if pf {
			c.stopT1()
			c.startT3()
		}
	case CtrlREJMask:
		c.peerBusy = false
		c.handleReceivedNR(nr)
		c.stopT1()
		c.retryCount = 0
		c.state = ConnStateConnected
		c.retransmitFrom(nr)
		if pf {
			c.sendRR(true, false)
		}
	}
	return nil
}

func (c *Conn) handleUFrameConnected(f *Frame) error {
	switch f.Control & 0xEF {
	case CtrlSABM:
		// Link reset while connected.
		c.resetStateVars()
		c.sendUA(HasPF(f.Control))
		if c.cbs.OnLinkReset != nil {
			c.enqueueAction(c.cbs.OnLinkReset)
		}
	case CtrlDISC:
		c.sendUA(HasPF(f.Control))
		c.enterDisconnected()
	case CtrlDM:
		c.fireError(errors.New("DM received while connected"), "remote disconnected", c.retryCount)
		c.enterDisconnected()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Timer callbacks (called from goroutines; acquire lock)
// ---------------------------------------------------------------------------

func (c *Conn) onT1Expired() {
	c.mu.Lock()
	c.t1Active = false
	slog.Debug("ax25: conn T1 expired", "state", c.state, "retry", c.retryCount)

	switch c.state {
	case ConnStateAwaitingConnection:
		if c.retryCount >= c.cfg.N2 {
			c.fireError(errors.New("connection timeout"), "N2 exceeded", c.retryCount)
			c.enterDisconnected()
		} else {
			c.retryCount++
			c.sendSABM(true)
			c.startT1()
		}
	case ConnStateAwaitingRelease:
		if c.retryCount >= c.cfg.N2 {
			c.fireError(errors.New("disconnect timeout"), "N2 exceeded", c.retryCount)
			c.enterDisconnected()
		} else {
			c.retryCount++
			c.sendDISC(true)
			c.startT1()
		}
	case ConnStateConnected:
		c.state = ConnStateTimerRecovery
		c.retryCount = 0
		fallthrough
	case ConnStateTimerRecovery:
		if c.retryCount >= c.cfg.N2 {
			c.fireError(errors.New("link failure"), "N2 exceeded", c.retryCount)
			c.sendDM(true)
			c.enterDisconnected()
		} else {
			c.retryCount++
			c.sendRR(true, true)
			c.startT1()
		}
	}
	actions := c.takePendingActions()
	c.mu.Unlock()
	c.runPendingActions(actions)
}

func (c *Conn) onT2Expired() {
	c.mu.Lock()
	c.t2Active = false
	if (c.state == ConnStateConnected || c.state == ConnStateTimerRecovery) && c.ackPending {
		c.sendAck(false, false)
		c.ackPending = false
	}
	actions := c.takePendingActions()
	c.mu.Unlock()
	c.runPendingActions(actions)
}

func (c *Conn) onT3Expired() {
	c.mu.Lock()
	c.t3Active = false
	if c.state == ConnStateConnected {
		c.sendRR(true, true)
		c.state = ConnStateTimerRecovery
		c.retryCount = 0
		c.startT1()
	}
	actions := c.takePendingActions()
	c.mu.Unlock()
	c.runPendingActions(actions)
}

// ---------------------------------------------------------------------------
// Timer helpers (called with c.mu held)
// ---------------------------------------------------------------------------

func (c *Conn) startT1() {
	c.stopT1()
	c.t1Active = true
	c.t1Timer = time.AfterFunc(c.cfg.T1, c.onT1Expired)
}

func (c *Conn) stopT1() {
	if c.t1Timer != nil {
		c.t1Timer.Stop()
		c.t1Timer = nil
	}
	c.t1Active = false
}

func (c *Conn) startT2() {
	if c.t2Active {
		return
	}
	c.t2Active = true
	c.t2Timer = time.AfterFunc(c.cfg.T2, c.onT2Expired)
}

func (c *Conn) stopT2() {
	if c.t2Timer != nil {
		c.t2Timer.Stop()
		c.t2Timer = nil
	}
	c.t2Active = false
}

func (c *Conn) startT3() {
	c.stopT3()
	c.t3Active = true
	c.t3Timer = time.AfterFunc(c.cfg.T3, c.onT3Expired)
}

func (c *Conn) stopT3() {
	if c.t3Timer != nil {
		c.t3Timer.Stop()
		c.t3Timer = nil
	}
	c.t3Active = false
}

func (c *Conn) stopAllTimers() {
	c.stopT1()
	c.stopT2()
	c.stopT3()
}

// ---------------------------------------------------------------------------
// Frame builders (called with c.mu held)
// ---------------------------------------------------------------------------

func (c *Conn) buildFrame(ctrl byte, pid byte, payload []byte) *Frame {
	f := &Frame{
		Destination: c.remoteAddr,
		Source:      c.localAddr,
		Digipeaters: append([]Address(nil), c.path...),
		IsCommand:   true,
		Control:     ctrl,
		PID:         pid,
		Payload:     payload,
	}
	f.Type = IdentifyFrameType(ctrl)
	return f
}

func (c *Conn) tx(f *Frame) {
	if c.cbs.OnTxFrame != nil {
		frame := cloneConnFrame(f)
		c.enqueueAction(func() { c.cbs.OnTxFrame(frame) })
	}
}

func (c *Conn) sendSABM(poll bool) {
	ctrl := CtrlSABM
	if poll {
		ctrl |= CtrlPFBit
	}
	c.tx(c.buildFrame(ctrl, 0, nil))
}

func (c *Conn) sendDISC(poll bool) {
	ctrl := CtrlDISC
	if poll {
		ctrl |= CtrlPFBit
	}
	c.tx(c.buildFrame(ctrl, 0, nil))
}

func (c *Conn) sendUA(final bool) {
	ctrl := CtrlUA
	if final {
		ctrl |= CtrlPFBit
	}
	f := c.buildFrame(ctrl, 0, nil)
	f.IsCommand = false
	c.tx(f)
}

func (c *Conn) sendDM(final bool) {
	ctrl := CtrlDM
	if final {
		ctrl |= CtrlPFBit
	}
	f := c.buildFrame(ctrl, 0, nil)
	f.IsCommand = false
	c.tx(f)
}

func (c *Conn) sendRR(pf, isCmd bool) {
	ctrl := BuildRRControl(c.vr, pf)
	f := c.buildFrame(ctrl, 0, nil)
	f.IsCommand = isCmd
	c.tx(f)
}

func (c *Conn) sendRNR(pf, isCmd bool) {
	ctrl := BuildRNRControl(c.vr, pf)
	f := c.buildFrame(ctrl, 0, nil)
	f.IsCommand = isCmd
	c.tx(f)
}

func (c *Conn) sendREJ(pf, isCmd bool) {
	ctrl := BuildREJControl(c.vr, pf)
	f := c.buildFrame(ctrl, 0, nil)
	f.IsCommand = isCmd
	c.tx(f)
}

func (c *Conn) sendAck(pf, isCmd bool) {
	if c.localBusy {
		c.sendRNR(pf, isCmd)
	} else {
		c.sendRR(pf, isCmd)
	}
}

func (c *Conn) sendPendingIFrames() {
	for i := range c.txQueue {
		pf := &c.txQueue[i]
		if pf.transmitted {
			continue
		}
		outstanding := mod8(c.vs - c.va)
		if int(outstanding) >= c.cfg.Window || c.peerBusy {
			break
		}
		pf.ns = c.vs
		pf.transmitted = true
		ctrl := BuildIControl(c.vs, c.vr, false)
		c.vs = mod8(c.vs + 1)
		payload := make([]byte, len(pf.data))
		copy(payload, pf.data)
		f := c.buildFrame(ctrl, PIDNone, payload)
		f.Type = FrameI
		c.tx(f)
		if !c.t1Active {
			c.startT1()
		}
	}
}

func (c *Conn) retransmitFrom(nr uint8) {
	for i := range c.txQueue {
		pf := &c.txQueue[i]
		if !pf.transmitted {
			continue
		}
		// Retransmit frames from nr onwards.
		pf.transmitted = false
	}
	c.vs = nr
	c.sendPendingIFrames()
}

// ---------------------------------------------------------------------------
// Sequence number helpers (called with c.mu held)
// ---------------------------------------------------------------------------

func mod8(n uint8) uint8 { return n & 0x07 }

func (c *Conn) isValidNR(nr uint8) bool {
	// nr must be in [va, vs] (mod 8)
	va := c.va
	vs := c.vs
	if va <= vs {
		return nr >= va && nr <= vs
	}
	// Wrapped
	return nr >= va || nr <= vs
}

func (c *Conn) handleReceivedNR(nr uint8) {
	// Acknowledge frames up to nr.
	for c.va != nr {
		// Remove the oldest pending frame.
		if len(c.txQueue) > 0 {
			c.txQueue = c.txQueue[1:]
		}
		c.va = mod8(c.va + 1)
	}
	if c.va == c.vs {
		c.stopT1()
		c.startT3()
	} else if !c.t1Active {
		c.startT1()
	}
}

func (c *Conn) startNRErrorRecovery(msg string) error {
	c.fireError(fmt.Errorf("ax25: N(R) error"), msg, c.retryCount)
	c.sendDM(true)
	c.enterDisconnected()
	return nil
}

// ---------------------------------------------------------------------------
// State transitions (called with c.mu held)
// ---------------------------------------------------------------------------

func (c *Conn) resetStateVars() {
	c.vs = 0
	c.vr = 0
	c.va = 0
	c.peerBusy = false
	c.localBusy = false
	c.rejSent = false
	c.ackPending = false
	c.txQueue = c.txQueue[:0]
}

func (c *Conn) enterDisconnected() {
	c.stopAllTimers()
	c.state = ConnStateDisconnected
	if c.cbs.OnDisconnect != nil {
		c.enqueueAction(c.cbs.OnDisconnect)
	}
}

func (c *Conn) fireError(code error, msg string, retry int) {
	if c.cbs.OnError != nil {
		err := &ConnError{Code: code, Message: msg, RetryCount: retry}
		c.enqueueAction(func() { c.cbs.OnError(err) })
	}
}

func (c *Conn) enqueueAction(action func()) {
	c.pendingActions = append(c.pendingActions, action)
}

func (c *Conn) takePendingActions() []func() {
	actions := c.pendingActions
	c.pendingActions = nil
	return actions
}

func (c *Conn) runPendingActions(actions []func()) {
	for _, action := range actions {
		action()
	}
}

func cloneConnFrame(f *Frame) *Frame {
	if f == nil {
		return nil
	}
	clone := *f
	if len(f.Digipeaters) > 0 {
		clone.Digipeaters = append([]Address(nil), f.Digipeaters...)
	}
	if len(f.Payload) > 0 {
		clone.Payload = append([]byte(nil), f.Payload...)
	}
	return &clone
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// extractReturnPath builds a digipeater path for replies from an incoming frame.
// It collects digipeaters that have been traversed (H-bit set), reverses them
// for the return trip, and clears HasBeenRepeated so the repeater will forward them.
func extractReturnPath(f *Frame) []Address {
	var path []Address
	for _, d := range f.Digipeaters {
		if d.HasBeenRepeated {
			d.HasBeenRepeated = false
			path = append(path, d)
		}
	}
	// Reverse: multi-hop paths must be traversed in opposite order on the return trip.
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}
