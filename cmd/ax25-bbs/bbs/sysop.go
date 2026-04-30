// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package bbs

import (
	"crypto/rand"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"
)

const (
	challengeIndexCount = 4
	responseLen         = 6
)

// SysopAuthMode tracks the SYSOP authentication state machine.
type SysopAuthMode int

const (
	SysopAuthIdle SysopAuthMode = iota
	SysopAuthAwaitingResponse
	SysopAuthAuthenticated
	SysopAuthLockedOut
)

// SysopState holds per-session SYSOP authentication state.
type SysopState struct {
	Mode             SysopAuthMode
	Secret           string
	ChallengeIndices [challengeIndexCount]int
	ChallengeExpires time.Time
	LastActivity     time.Time
	LockoutUntil     time.Time
	FailedAttempts   int

	ChallengeTimeout time.Duration
	SessionTimeout   time.Duration
	LockoutDuration  time.Duration
	MaxAttempts      int
}

// NewSysopState creates a new SysopState with the given configuration.
func NewSysopState(secret string, challengeTimeout, sessionTimeout, lockout time.Duration, maxAttempts int) SysopState {
	return SysopState{
		Mode:             SysopAuthIdle,
		Secret:           secret,
		ChallengeTimeout: challengeTimeout,
		SessionTimeout:   sessionTimeout,
		LockoutDuration:  lockout,
		MaxAttempts:      maxAttempts,
	}
}

// IsAuthenticated returns true if the sysop is currently authenticated.
func (s *SysopState) IsAuthenticated() bool {
	return s.Mode == SysopAuthAuthenticated
}

// IsAwaitingResponse returns true if a challenge has been issued.
func (s *SysopState) IsAwaitingResponse() bool {
	return s.Mode == SysopAuthAwaitingResponse
}

// IsLockedOut returns true if the sysop is currently locked out.
func (s *SysopState) IsLockedOut() bool {
	if s.Mode != SysopAuthLockedOut {
		return false
	}
	if time.Now().After(s.LockoutUntil) {
		s.Mode = SysopAuthIdle
		s.FailedAttempts = 0
		return false
	}
	return true
}

// NoteActivity updates the last activity timestamp.
func (s *SysopState) NoteActivity() {
	if s.Mode == SysopAuthAuthenticated {
		s.LastActivity = time.Now()
	}
}

// ExpireIfIdle expires the sysop session if idle too long. Returns true if expired.
func (s *SysopState) ExpireIfIdle() bool {
	if s.Mode != SysopAuthAuthenticated {
		return false
	}
	if time.Since(s.LastActivity) > s.SessionTimeout {
		s.Mode = SysopAuthIdle
		return true
	}
	return false
}

// LockoutMinutes returns the lockout duration in minutes.
func (s *SysopState) LockoutMinutes() int {
	mins := int(s.LockoutDuration.Minutes())
	if mins < 1 {
		mins = 1
	}
	return mins
}

// IssueChallenge generates a new challenge. Returns the challenge text.
func (s *SysopState) IssueChallenge() (string, error) {
	secretLen := len(s.Secret)
	if secretLen < challengeIndexCount {
		return "", fmt.Errorf("sysop secret too short")
	}

	// Pick 4 unique random 1-based indices into the secret.
	used := make(map[int]bool)
	for i := 0; i < challengeIndexCount; i++ {
		for {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(secretLen)))
			if err != nil {
				return "", fmt.Errorf("random generation failed: %w", err)
			}
			idx := int(n.Int64()) + 1 // 1-based
			if !used[idx] {
				used[idx] = true
				s.ChallengeIndices[i] = idx
				break
			}
		}
	}

	s.Mode = SysopAuthAwaitingResponse
	s.ChallengeExpires = time.Now().Add(s.ChallengeTimeout)

	challenge := fmt.Sprintf("CHAL %d %d %d %d\r",
		s.ChallengeIndices[0],
		s.ChallengeIndices[1],
		s.ChallengeIndices[2],
		s.ChallengeIndices[3])

	return challenge, nil
}

// VerifyResponse checks the user's response against the challenge.
type VerifyResult int

const (
	VerifySuccess VerifyResult = iota
	VerifyFailed
	VerifyLockedOut
	VerifyExpired
	VerifyNoActive
)

// SubmitResponse verifies the response to the active challenge.
func (s *SysopState) SubmitResponse(response string) VerifyResult {
	if s.Mode != SysopAuthAwaitingResponse {
		return VerifyNoActive
	}

	if time.Now().After(s.ChallengeExpires) {
		s.Mode = SysopAuthIdle
		return VerifyExpired
	}

	ok := s.verifyResponse(response)
	s.Mode = SysopAuthIdle // clear challenge regardless

	if ok {
		s.FailedAttempts = 0
		s.Mode = SysopAuthAuthenticated
		s.LastActivity = time.Now()
		return VerifySuccess
	}

	s.FailedAttempts++
	if s.FailedAttempts >= s.MaxAttempts {
		s.Mode = SysopAuthLockedOut
		s.LockoutUntil = time.Now().Add(s.LockoutDuration)
		return VerifyLockedOut
	}

	return VerifyFailed
}

// verifyResponse checks if the response contains the correct characters.
// The response must be exactly responseLen characters and contain the 4
// required characters from the secret (order-independent, with padding).
func (s *SysopState) verifyResponse(response string) bool {
	if len(response) != responseLen {
		return false
	}

	required := make([]byte, challengeIndexCount)
	for i := 0; i < challengeIndexCount; i++ {
		idx := s.ChallengeIndices[i]
		if idx < 1 || idx > len(s.Secret) {
			return false
		}
		required[i] = s.Secret[idx-1]
	}

	// Each required character must appear in the response (order-independent).
	used := make([]bool, responseLen)
	for i := 0; i < challengeIndexCount; i++ {
		found := false
		for j := 0; j < responseLen; j++ {
			if !used[j] && response[j] == required[i] {
				used[j] = true
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

// issueSysopChallenge issues a challenge and sends it to the session.
func (m *SessionManager) issueSysopChallenge(sess *Session) {
	challenge, err := sess.Sysop.IssueChallenge()
	if err != nil {
		m.sendText(sess, "*** SYSOP challenge unavailable\r")
		slog.Error("BBS: sysop challenge failed", "err", err)
		return
	}

	sess.InputMode = InputModeSysopResponse
	m.sendText(sess, challenge)
	slog.Info("BBS: SYSOP challenge issued", "remote", sess.RemoteCall)
}

// processSysopResponse handles the response to a SYSOP challenge.
func (m *SessionManager) processSysopResponse(sess *Session, line string) {
	line = strings.TrimSpace(line)

	// Allow "SYSOP NEW" to refresh the challenge.
	if strings.EqualFold(line, "SYSOP NEW") {
		if sess.Sysop.IsAwaitingResponse() {
			m.issueSysopChallenge(sess)
		} else {
			m.sendText(sess, "*** No active SYSOP challenge\r")
			m.sendPrompt(sess)
		}
		return
	}

	result := sess.Sysop.SubmitResponse(line)
	sess.InputMode = InputModeCommand
	sess.pendingConfigEntry = false

	switch result {
	case VerifySuccess:
		sess.IsSysop = true
		m.sendText(sess, "*** SYSOP authenticated\r")
		slog.Info("BBS: SYSOP auth success", "remote", sess.RemoteCall)
		if sess.pendingConfigEntry {
			sess.pendingConfigEntry = false
			m.enterConfigMode(sess)
			return
		}

	case VerifyLockedOut:
		sess.IsSysop = false
		mins := sess.Sysop.LockoutMinutes()
		m.sendText(sess, fmt.Sprintf("*** SYSOP locked out for %d minutes\r", mins))
		slog.Warn("BBS: SYSOP lockout", "remote", sess.RemoteCall, "failures", sess.Sysop.FailedAttempts)

	case VerifyExpired:
		sess.IsSysop = false
		m.sendText(sess, "*** SYSOP challenge expired\r")
		slog.Info("BBS: SYSOP challenge expired", "remote", sess.RemoteCall)

	case VerifyNoActive:
		m.sendText(sess, "*** No active SYSOP challenge\r")

	case VerifyFailed:
		m.sendText(sess, fmt.Sprintf("*** SYSOP auth failed (%d/%d)\r",
			sess.Sysop.FailedAttempts, sess.Sysop.MaxAttempts))
		slog.Info("BBS: SYSOP auth failed", "remote", sess.RemoteCall,
			"failures", sess.Sysop.FailedAttempts)
	}

	m.sendPrompt(sess)
}

// sysopExpireIfIdle expires the sysop session if idle.
func (m *SessionManager) sysopExpireIfIdle(sess *Session) {
	if sess.Sysop.ExpireIfIdle() {
		sess.IsSysop = false
		m.sendText(sess, "*** SYSOP session expired\r")
		slog.Info("BBS: SYSOP session expired", "remote", sess.RemoteCall)
	}
}
