// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package bbs

import (
	"log/slog"
	"strings"
)

// processLine dispatches a complete line based on the current input mode.
func (m *SessionManager) processLine(sess *Session, line string) {
	slog.Debug("BBS: line", "remote", sess.RemoteCall, "mode", sess.InputMode, "line", line)

	switch sess.InputMode {
	case InputModeCommand:
		m.processCommand(sess, line)
	case InputModeSubject:
		m.processSubject(sess, line)
	case InputModeBody:
		m.processBody(sess, line)
	case InputModeSysopResponse:
		m.processSysopResponse(sess, line)
	case InputModeConfig:
		m.processConfigCommand(sess, line)
	}
}

// processCommand parses and dispatches a BBS command.
func (m *SessionManager) processCommand(sess *Session, line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		m.sendPrompt(sess)
		return
	}

	parts := strings.SplitN(line, " ", 2)
	cmd := strings.ToUpper(parts[0])
	arg := ""
	if len(parts) > 1 {
		arg = strings.TrimSpace(parts[1])
	}

	slog.Debug("BBS: command", "remote", sess.RemoteCall, "cmd", cmd, "arg", arg)

	// Expire sysop session if idle.
	m.sysopExpireIfIdle(sess)
	if sess.Sysop.IsAuthenticated() {
		sess.Sysop.NoteActivity()
	}

	switch cmd {
	case "H", "?":
		m.cmdHelp(sess)
	case "I":
		m.cmdInfo(sess)
	case "J":
		m.cmdHeard(sess)
	case "L":
		m.cmdList(sess, 0, false)
	case "LL":
		m.cmdListLast(sess, arg)
	case "LM":
		m.cmdList(sess, 0, true)
	case "R":
		m.cmdRead(sess, arg)
	case "S":
		m.cmdSend(sess, arg, 0)
	case "SP":
		m.cmdSend(sess, arg, FlagPrivate)
	case "SB":
		m.cmdSend(sess, arg, FlagBulletin)
	case "K":
		m.cmdKill(sess, arg)
	case "SYSOP":
		m.cmdSysop(sess, arg)
	case "CONFIG":
		m.cmdConfig(sess)
		return // cmdConfig manages its own prompt
	case "B":
		m.cmdBye(sess)
		return // don't send prompt after disconnect
	default:
		m.sendText(sess, "*** Unknown command. Type H for help.\r")
	}

	m.sendPrompt(sess)
}

// BaseCallsign extracts the callsign without SSID.
func BaseCallsign(call string) string {
	if idx := strings.IndexByte(call, '-'); idx >= 0 {
		call = call[:idx]
	}
	return strings.ToUpper(call)
}

// IsValidCallsign checks if a string looks like a valid amateur callsign.
func IsValidCallsign(s string) bool {
	base := BaseCallsign(s)
	if len(base) < 3 || len(base) > 6 {
		return false
	}

	// Must have 1-2 letter prefix, 1 digit, 1-3 letter suffix.
	i := 0
	for i < len(base) && base[i] >= 'A' && base[i] <= 'Z' {
		i++
	}
	if i < 1 || i > 2 {
		return false
	}
	if i >= len(base) || base[i] < '0' || base[i] > '9' {
		return false
	}
	i++
	suffix := 0
	for i < len(base) && base[i] >= 'A' && base[i] <= 'Z' {
		suffix++
		i++
	}
	return suffix >= 1 && suffix <= 3 && i == len(base)
}
