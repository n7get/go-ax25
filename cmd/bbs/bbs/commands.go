// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package bbs

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/n7get/go-ax25/cmd/bbs/store"
)

const (
	FlagPrivate  uint8 = 0x01
	FlagBulletin uint8 = 0x02

	readSeparator = "----------------------------------------\r"
)

// bannerSummary returns a message-count summary line for the connect banner.
func (m *SessionManager) bannerSummary(sess *Session) string {
	msgs, err := m.store.List(0, false, sess.BaseCall, sess.IsSysop)
	if err != nil || len(msgs) == 0 {
		return fmt.Sprintf("*** No messages for %s\r", sess.BaseCall)
	}
	lastRead, _ := m.store.GetLastRead(sess.BaseCall)
	newCount := 0
	for _, msg := range msgs {
		if msg.ID > lastRead {
			newCount++
		}
	}
	return fmt.Sprintf("*** You have %d messages (%d new)\r", len(msgs), newCount)
}

// cmdHelp sends the help text.
func (m *SessionManager) cmdHelp(sess *Session) {
	help := "Commands:\r" +
		"  B           Disconnect\r" +
		"  H           Show this help text\r" +
		"  I           Show BBS station information\r" +
		"  J           Show heard list\r" +
		"  K <n>       Delete message n (own or sysop)\r" +
		"  L           List readable messages\r" +
		"  LL [n]      List newest n readable messages\r" +
		"  LM          List only my sent/received messages\r" +
		"  R <n>       Read message n\r" +
		"  S <to>      Send message (auto private/bulletin)\r" +
		"  SB <topic>  Send bulletin message\r" +
		"  SP <call>   Send private message\r" +
		"  SYSOP       Start SYSOP challenge login\r" +
		"  SYSOP NEW   Refresh active SYSOP challenge\r"
	m.sendText(sess, help)
}

// cmdInfo sends station information.
func (m *SessionManager) cmdInfo(sess *Session) {
	info := fmt.Sprintf("Call:    %s\r", m.cfg.Callsign) +
		fmt.Sprintf("Sysop:   %s\r", m.cfg.SysopName) +
		fmt.Sprintf("Version: %s\r", m.cfg.Version)
	m.sendText(sess, info)
}

// cmdHeard sends the heard list.
func (m *SessionManager) cmdHeard(sess *Session) {
	entries := m.heard.Entries()
	if len(entries) == 0 {
		m.sendText(sess, "*** No stations heard\r")
		return
	}

	now := time.Now()
	var sb strings.Builder
	sb.WriteString("Heard:\r")
	for _, e := range entries {
		elapsed := now.Sub(e.Time)
		days := int(elapsed.Hours()) / 24
		hours := int(elapsed.Hours()) % 24
		mins := int(elapsed.Minutes()) % 60
		sb.WriteString(fmt.Sprintf("  %-10s %dd %02d:%02d ago\r", e.Callsign, days, hours, mins))
	}
	m.sendText(sess, sb.String())
}

// cmdList lists messages. If mineOnly is true, only show messages to/from the caller.
func (m *SessionManager) cmdList(sess *Session, limit int, mineOnly bool) {
	msgs, err := m.store.List(limit, mineOnly, sess.BaseCall, sess.IsSysop)
	if err != nil {
		slog.Error("BBS: list failed", "err", err)
		m.sendText(sess, "*** List failed\r")
		return
	}

	if len(msgs) == 0 {
		if mineOnly {
			m.sendText(sess, "*** No messages for you\r")
		} else {
			m.sendText(sess, "*** No readable messages\r")
		}
		return
	}

	var sb strings.Builder
	sb.WriteString("   # From            To              Flags Subject\r")
	for _, msg := range msgs {
		if !mineOnly && !isReadableForUser(sess, &msg) {
			continue
		}
		pFlag := '-'
		bFlag := '-'
		if msg.Flags&FlagPrivate != 0 {
			pFlag = 'P'
		}
		if msg.Flags&FlagBulletin != 0 {
			bFlag = 'B'
		}
		sb.WriteString(fmt.Sprintf("%4d %-15s %-15s %c%c    %s\r",
			msg.ID, msg.From, msg.To, pFlag, bFlag, msg.Subject))
	}
	m.sendText(sess, sb.String())
}

// cmdListLast lists the last N messages.
func (m *SessionManager) cmdListLast(sess *Session, arg string) {
	n := 10
	if arg != "" {
		if v, err := strconv.Atoi(arg); err == nil && v > 0 {
			n = v
		}
	}
	m.cmdList(sess, n, false)
}

// cmdRead reads a message by ID.
func (m *SessionManager) cmdRead(sess *Session, arg string) {
	id, err := strconv.ParseUint(strings.TrimSpace(arg), 10, 32)
	if err != nil || id == 0 {
		m.sendText(sess, "*** Usage: R <message number>\r")
		return
	}

	msg, err := m.store.Read(uint32(id))
	if err != nil {
		m.sendText(sess, "*** Message not found\r")
		return
	}

	idx := store.MessageIndex{
		ID:    msg.ID,
		From:  msg.From,
		To:    msg.To,
		Flags: msg.Flags,
	}
	if !isReadableForUser(sess, &idx) {
		m.sendText(sess, "*** Access denied\r")
		return
	}

	header := fmt.Sprintf("Msg %d From:%s To:%s Subj:%s\r", msg.ID, msg.From, msg.To, msg.Subject)
	m.sendText(sess, header)
	m.sendText(sess, readSeparator)
	m.sendText(sess, msg.Body)
	m.sendText(sess, readSeparator)

	// Update last-read pointer.
	lastRead, _ := m.store.GetLastRead(sess.BaseCall)
	if uint32(id) > lastRead {
		_ = m.store.SetLastRead(sess.BaseCall, uint32(id))
	}
}

// cmdSend begins composing a message.
func (m *SessionManager) cmdSend(sess *Session, arg string, forceFlags uint8) {
	to := strings.TrimSpace(arg)
	if to == "" {
		m.sendText(sess, "*** Usage: S <callsign or topic>\r")
		return
	}

	to = strings.ToUpper(to)

	// Determine flags: if forceFlags is set, use it. Otherwise auto-detect.
	flags := forceFlags
	if flags == 0 {
		if IsValidCallsign(to) {
			flags = FlagPrivate
		} else {
			flags = FlagBulletin
		}
	}

	sess.PendingTo = BaseCallsign(to)
	sess.PendingFlags = flags
	sess.PendingBody.Reset()
	sess.PendingSubject = ""
	sess.InputMode = InputModeSubject

	m.sendText(sess, "Subject: \r")
}

// processSubject handles the subject line during message composition.
func (m *SessionManager) processSubject(sess *Session, line string) {
	subject := strings.TrimSpace(line)
	if subject == "" {
		m.sendText(sess, "*** Aborted\r")
		sess.InputMode = InputModeCommand
		m.sendPrompt(sess)
		return
	}

	if len(subject) > 48 {
		subject = subject[:48]
	}

	sess.PendingSubject = subject
	sess.InputMode = InputModeBody

	m.sendText(sess, "Enter body, use /EX to finish:\r")
}

// processBody handles body lines during message composition.
func (m *SessionManager) processBody(sess *Session, line string) {
	trimmed := strings.TrimSpace(line)

	if strings.EqualFold(trimmed, "/EX") {
		m.finishPost(sess)
		return
	}

	// Check max body size.
	currentLen := sess.PendingBody.Len()
	lineLen := len(line) + 1 // +1 for \r
	if currentLen+lineLen > m.cfg.MaxBodyLen {
		m.sendText(sess, "*** Message too large, truncated. Sending now.\r")
		m.finishPost(sess)
		return
	}

	sess.PendingBody.WriteString(line)
	sess.PendingBody.WriteByte('\r')
}

// finishPost stores the composed message.
func (m *SessionManager) finishPost(sess *Session) {
	id, err := m.store.Store(
		sess.BaseCall,
		sess.PendingTo,
		sess.PendingSubject,
		sess.PendingBody.String(),
		sess.PendingFlags,
	)

	if err != nil {
		slog.Error("BBS: store failed", "err", err)
		m.sendText(sess, "*** Store failed\r")
	} else {
		m.sendText(sess, fmt.Sprintf("*** Stored as message %d\r", id))
	}

	sess.InputMode = InputModeCommand
	sess.PendingBody.Reset()
	sess.PendingSubject = ""
	sess.PendingTo = ""
	sess.PendingFlags = 0
	m.sendPrompt(sess)
}

// cmdKill deletes a message.
func (m *SessionManager) cmdKill(sess *Session, arg string) {
	id, err := strconv.ParseUint(strings.TrimSpace(arg), 10, 32)
	if err != nil || id == 0 {
		m.sendText(sess, "*** Usage: K <message number>\r")
		return
	}

	// Check ownership.
	idx, err := m.store.Find(uint32(id))
	if err != nil || idx == nil {
		m.sendText(sess, "*** Message not found\r")
		return
	}

	if !sess.IsSysop && idx.From != sess.BaseCall {
		m.sendText(sess, "*** Kill denied\r")
		return
	}

	if err := m.store.Delete(uint32(id)); err != nil {
		m.sendText(sess, "*** Delete failed\r")
		return
	}

	m.sendText(sess, "*** Message deleted\r")
}

// cmdBye disconnects the station.
func (m *SessionManager) cmdBye(sess *Session) {
	m.sendText(sess, "*** Goodbye\r")
	m.sendDisconnect(sess)
}

// cmdSysop handles the SYSOP command.
func (m *SessionManager) cmdSysop(sess *Session, arg string) {
	if m.cfg.SysopSecret == "" {
		m.sendText(sess, "*** SYSOP not configured\r")
		return
	}

	argUpper := strings.ToUpper(strings.TrimSpace(arg))

	if argUpper == "NEW" {
		if sess.Sysop.IsAwaitingResponse() {
			m.issueSysopChallenge(sess)
		} else {
			m.sendText(sess, "*** No active SYSOP challenge\r")
		}
		return
	}

	if sess.Sysop.IsLockedOut() {
		mins := sess.Sysop.LockoutMinutes()
		m.sendText(sess, fmt.Sprintf("*** SYSOP locked out for %d minutes\r", mins))
		return
	}

	m.issueSysopChallenge(sess)
}

// isReadableForUser checks if a message is readable by the current user.
func isReadableForUser(sess *Session, msg *store.MessageIndex) bool {
	if msg.Flags&FlagBulletin != 0 {
		return true
	}
	if sess.IsSysop {
		return true
	}
	if msg.Flags&FlagPrivate != 0 {
		return msg.To == sess.BaseCall || msg.From == sess.BaseCall
	}
	return false
}

// cmdConfig handles the CONFIG command: enter config mode, with sysop auth if required.
func (m *SessionManager) cmdConfig(sess *Session) {
	if m.cfg.SysopSecret != "" && !sess.IsSysop {
		if sess.Sysop.IsLockedOut() {
			mins := sess.Sysop.LockoutMinutes()
			m.sendText(sess, fmt.Sprintf("*** SYSOP locked out for %d minutes\r", mins))
			m.sendPrompt(sess)
			return
		}
		sess.pendingConfigEntry = true
		m.issueSysopChallenge(sess)
		return
	}
	m.enterConfigMode(sess)
}

// enterConfigMode transitions the session into CONFIG mode.
func (m *SessionManager) enterConfigMode(sess *Session) {
	sess.InputMode = InputModeConfig
	m.sendText(sess, "*** Entering config mode\r")
	m.sendConfigPrompt(sess)
}

// sendConfigPrompt sends the CONFIG mode prompt.
func (m *SessionManager) sendConfigPrompt(sess *Session) {
	m.sendText(sess, "CONFIG READY>\r")
}

// processConfigCommand handles commands while in CONFIG mode.
func (m *SessionManager) processConfigCommand(sess *Session, line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		m.sendConfigPrompt(sess)
		return
	}

	parts := strings.SplitN(line, " ", 2)
	cmd := strings.ToLower(parts[0])
	arg := ""
	if len(parts) > 1 {
		arg = strings.TrimSpace(parts[1])
	}

	switch cmd {
	case "get":
		vals := m.configValues()
		if val, ok := vals[arg]; ok {
			m.sendText(sess, fmt.Sprintf("%s=%s\r", arg, val))
		} else {
			m.sendText(sess, fmt.Sprintf("*** Unknown key: %s\r", arg))
		}
	case "exit":
		sess.InputMode = InputModeCommand
		m.sendText(sess, "*** Exiting config mode\r")
		m.sendPrompt(sess)
		return
	default:
		m.sendText(sess, "*** Unknown config command\r")
	}

	m.sendConfigPrompt(sess)
}

// configValues returns the current BBS configuration as a key→value map.
func (m *SessionManager) configValues() map[string]string {
	cfg := m.cfg
	return map[string]string{
		"bbs.callsign":                  cfg.Callsign,
		"bbs.greeting":                  cfg.Greeting,
		"bbs.prompt":                    cfg.Prompt,
		"bbs.sysop_name":                cfg.SysopName,
		"bbs.version":                   cfg.Version,
		"bbs.max_messages":              strconv.Itoa(cfg.MaxMessages),
		"bbs.max_body_len":              strconv.Itoa(cfg.MaxBodyLen),
		"bbs.sysop_challenge_timeout_s": strconv.Itoa(cfg.SysopChallengeTimeout),
		"bbs.sysop_session_timeout_s":   strconv.Itoa(cfg.SysopSessionTimeout),
		"bbs.sysop_lockout_s":           strconv.Itoa(cfg.SysopLockout),
		"bbs.sysop_max_attempts":        strconv.Itoa(cfg.SysopMaxAttempts),
		"bbs.db_path":                   cfg.DBPath,
		"bbs.host":                      cfg.AGWPEHost,
		"bbs.port":                      strconv.Itoa(int(cfg.AGWPEPort)),
	}
}
