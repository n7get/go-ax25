// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package bbs

import (
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/n7get/go-ax25/agwpe"
	"github.com/n7get/go-ax25/cmd/ax25-bbs/heard"
	"github.com/n7get/go-ax25/cmd/ax25-bbs/store"
)

// InputMode tracks what kind of input the session expects next.
type InputMode int

const (
	InputModeCommand       InputMode = iota
	InputModeSubject                 // composing: waiting for subject line
	InputModeBody                    // composing: accumulating body lines
	InputModeSysopResponse           // waiting for SYSOP challenge answer
	InputModeConfig                  // inside CONFIG command mode
)

// Session holds per-connection BBS state.
type Session struct {
	RemoteCall string // full callsign of the connected station
	BaseCall   string // callsign without SSID
	LocalCall  string // BBS callsign (from config)
	Port       uint8  // AGWPE port

	InputMode InputMode
	IsSysop   bool

	// Message composition state.
	PendingTo      string
	PendingSubject string
	PendingBody    strings.Builder
	PendingFlags   uint8

	// Line buffer for accumulating partial lines.
	lineBuf   strings.Builder
	lastWasCR bool

	// Sysop auth state.
	Sysop SysopState

	// Set when CONFIG was requested; after sysop auth enter config mode.
	pendingConfigEntry bool
}

// SessionManager manages all active BBS sessions.
type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]*Session // key: "remoteCall:localCall"
	cfg      BBSConfig
	store    store.MessageStore
	heard    *heard.List
	client   *agwpe.Client
}

// NewSessionManager creates a new SessionManager.
func NewSessionManager(cfg BBSConfig, st store.MessageStore, hl *heard.List) *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
		cfg:      cfg,
		store:    st,
		heard:    hl,
	}
}

// SetAGWPEClient sets the AGWPE client used for sending data.
func (m *SessionManager) SetAGWPEClient(c *agwpe.Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.client = c
}

func sessionKey(remoteCall, localCall string) string {
	return remoteCall + ":" + localCall
}

// OnConnect handles a new incoming connection.
func (m *SessionManager) OnConnect(remoteCall, localCall string, port uint8) {
	m.mu.Lock()

	key := sessionKey(remoteCall, localCall)
	if _, exists := m.sessions[key]; exists {
		slog.Warn("BBS: duplicate connect", "remote", remoteCall, "local", localCall)
		m.mu.Unlock()
		return
	}

	sess := &Session{
		RemoteCall: remoteCall,
		BaseCall:   BaseCallsign(remoteCall),
		LocalCall:  localCall,
		Port:       port,
		InputMode:  InputModeCommand,
	}

	// Initialize sysop auth.
	sess.Sysop = NewSysopState(
		m.cfg.SysopSecret,
		time.Duration(m.cfg.SysopChallengeTimeout)*time.Second,
		time.Duration(m.cfg.SysopSessionTimeout)*time.Second,
		time.Duration(m.cfg.SysopLockout)*time.Second,
		m.cfg.SysopMaxAttempts,
	)

	m.sessions[key] = sess
	greeting := m.cfg.Greeting
	prompt := m.cfg.Prompt

	m.mu.Unlock()

	slog.Info("BBS: connect", "remote", remoteCall, "local", localCall)

	// Send greeting and prompt outside the lock to avoid deadlock:
	// sendBytes acquires m.mu to read m.client.
	m.sendText(sess, greeting+"\r")
	m.sendText(sess, m.bannerSummary(sess))
	m.sendText(sess, prompt+"\r")
}

// OnData handles incoming data from a connected station.
func (m *SessionManager) OnData(remoteCall, localCall string, data []byte) {
	m.mu.Lock()
	key := sessionKey(remoteCall, localCall)
	sess, ok := m.sessions[key]
	m.mu.Unlock()

	if !ok {
		slog.Warn("BBS: data for unknown session", "remote", remoteCall, "local", localCall)
		return
	}

	// Feed bytes into line buffer, dispatch complete lines.
	for _, b := range data {
		if b == '\r' {
			line := sess.lineBuf.String()
			sess.lineBuf.Reset()
			sess.lastWasCR = true
			m.processLine(sess, line)
		} else if b == '\n' {
			if sess.lastWasCR {
				sess.lastWasCR = false
				continue // skip LF after CR
			}
			line := sess.lineBuf.String()
			sess.lineBuf.Reset()
			m.processLine(sess, line)
		} else {
			sess.lastWasCR = false
			sess.lineBuf.WriteByte(b)
		}
	}
}

// OnDisconnect handles a station disconnecting.
func (m *SessionManager) OnDisconnect(remoteCall, localCall string) {
	m.mu.Lock()
	key := sessionKey(remoteCall, localCall)
	delete(m.sessions, key)
	m.mu.Unlock()

	slog.Info("BBS: disconnect", "remote", remoteCall, "local", localCall)
}

// DisconnectAll tears down all sessions (used during shutdown).
func (m *SessionManager) DisconnectAll() {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.sessions = make(map[string]*Session)
	m.mu.Unlock()

	for _, s := range sessions {
		m.sendDisconnect(s)
	}
}

// sendText sends a text string to the remote station via AGWPE.
func (m *SessionManager) sendText(sess *Session, text string) {
	m.sendBytes(sess, []byte(text))
}

// sendBytes sends raw bytes to the remote station via AGWPE.
func (m *SessionManager) sendBytes(sess *Session, data []byte) {
	m.mu.Lock()
	client := m.client
	m.mu.Unlock()

	if client == nil {
		slog.Warn("BBS: no AGWPE client, cannot send")
		return
	}

	frame := agwpe.BuildSendData(sess.Port, sess.LocalCall, sess.RemoteCall, 0xF0, data)
	if err := client.SendFrame(frame); err != nil {
		slog.Error("BBS: send failed", "remote", sess.RemoteCall, "err", err)
	}
}

// sendDisconnect sends a disconnect request via AGWPE.
func (m *SessionManager) sendDisconnect(sess *Session) {
	m.mu.Lock()
	client := m.client
	m.mu.Unlock()

	if client == nil {
		return
	}

	frame := agwpe.BuildDisconnectReq(sess.Port, sess.LocalCall, sess.RemoteCall)
	if err := client.SendFrame(frame); err != nil {
		slog.Error("BBS: disconnect send failed", "remote", sess.RemoteCall, "err", err)
	}
}

// sendPrompt sends the BBS prompt to the session.
func (m *SessionManager) sendPrompt(sess *Session) {
	m.sendText(sess, m.cfg.Prompt+"\r")
}
