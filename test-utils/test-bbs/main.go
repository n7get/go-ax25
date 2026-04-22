// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

// test-bbs is an automated BBS command test suite for go-ax25.
//
// It connects to a BBS via an AGWPE server (e.g. ax25-router), runs a scripted
// set of BBS command tests, and prints a PASS/FAIL summary to stdout.
//
// Usage:
//
//	test-bbs -local N7GET-9 -remote W1AW-1 -agwpe localhost:8000
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/n7get/go-ax25/agwpe"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	rxQueueSize = 64
	captureMax  = 4096

	sysopChallengeIndexCount = 4
	sysopResponseLen         = 6
)

var promptTokens = []string{
	"BBS READY>",
	"BBS READY:",
	"BBS> ",
}

var configPromptTokens = []string{
	"CONFIG READY>",
}

// ---------------------------------------------------------------------------
// appCtx — shared state between conn callbacks and test goroutine
// ---------------------------------------------------------------------------

type appCtx struct {
	client     *agwpe.Client
	localCall  string
	remoteCall string
	agwpePort  uint8

	mu          sync.Mutex
	rxCh        chan []byte
	connectedCh chan struct{}
	disconnCh   chan struct{}

	// test state
	testsRun      int
	testsFailed   int
	postedMsgID   uint32
	createdIDs    []uint32
	runSubjectTag string
}

func newAppCtx() *appCtx {
	return &appCtx{
		connectedCh: make(chan struct{}, 1),
		disconnCh:   make(chan struct{}, 1),
	}
}

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
		slog.Warn("TEST-BBS: RX queue full, dropping bytes", "len", len(data))
	}
}

func (a *appCtx) openRxCh() {
	a.mu.Lock()
	a.rxCh = make(chan []byte, rxQueueSize)
	a.mu.Unlock()
}

func (a *appCtx) closeRxCh() {
	a.mu.Lock()
	ch := a.rxCh
	a.rxCh = nil
	a.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

func (a *appCtx) getRxCh() chan []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rxCh
}

// ---------------------------------------------------------------------------
// AGWPE frame dispatcher
// ---------------------------------------------------------------------------

func (a *appCtx) handleFrame(f *agwpe.Frame) {
	if f == nil {
		return
	}
	switch f.Kind {
	case agwpe.KindConnectResp:
		slog.Debug("TEST-BBS: connected")
		select {
		case a.connectedCh <- struct{}{}:
		default:
		}
	case agwpe.KindRecvData:
		slog.Debug("TEST-BBS: data", "len", len(f.Data))
		a.enqueue(f.Data)
	case agwpe.KindDisconnectResp:
		slog.Debug("TEST-BBS: disconnected")
		a.closeRxCh()
		select {
		case a.disconnCh <- struct{}{}:
		default:
		}
	}
}

// ---------------------------------------------------------------------------
// byteReader — io.Reader over chan []byte, context-aware
// ---------------------------------------------------------------------------

type byteReader struct {
	ctx context.Context
	ch  <-chan []byte
	buf []byte
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

// ---------------------------------------------------------------------------
// Token scanner
// ---------------------------------------------------------------------------

// readUntilToken reads bytes from ch until the given token appears in the
// stream.  All bytes read are appended to capture (up to captureMax).
// Returns io.EOF if the channel closes, context.DeadlineExceeded on timeout.
func readUntilToken(ctx context.Context, ch <-chan []byte, token string, capture *strings.Builder) error {
	if token == "" {
		return fmt.Errorf("readUntilToken: empty token")
	}
	tokenLen := len(token)
	window := make([]byte, 0, tokenLen)

	br := &byteReader{ctx: ctx, ch: ch}
	oneByte := make([]byte, 1)
	for {
		n, err := br.Read(oneByte)
		if n > 0 {
			b := oneByte[0]
			if capture != nil && capture.Len() < captureMax {
				capture.WriteByte(b)
			}
			if len(window) < tokenLen {
				window = append(window, b)
			} else {
				copy(window, window[1:])
				window[tokenLen-1] = b
			}
			if len(window) == tokenLen && string(window) == token {
				return nil
			}
		}
		if err != nil {
			return err
		}
	}
}

// readUntilAnyToken reads until any of the given tokens appears in the stream.
func readUntilAnyToken(ctx context.Context, ch <-chan []byte, tokens []string, capture *strings.Builder) error {
	if len(tokens) == 0 {
		return fmt.Errorf("readUntilAnyToken: no tokens")
	}
	maxLen := 0
	for _, t := range tokens {
		if len(t) > maxLen {
			maxLen = len(t)
		}
	}
	window := make([]byte, 0, maxLen)

	br := &byteReader{ctx: ctx, ch: ch}
	oneByte := make([]byte, 1)
	for {
		n, err := br.Read(oneByte)
		if n > 0 {
			b := oneByte[0]
			if capture != nil && capture.Len() < captureMax {
				capture.WriteByte(b)
			}
			if len(window) < maxLen {
				window = append(window, b)
			} else {
				copy(window, window[1:])
				window[maxLen-1] = b
			}
			winStr := string(window)
			for _, tok := range tokens {
				tl := len(tok)
				wl := len(winStr)
				if wl >= tl && winStr[wl-tl:] == tok {
					return nil
				}
			}
		}
		if err != nil {
			return err
		}
	}
}

func readUntilPrompt(ctx context.Context, ch <-chan []byte, capture *strings.Builder) error {
	return readUntilAnyToken(ctx, ch, promptTokens, capture)
}

func readUntilConfigPrompt(ctx context.Context, ch <-chan []byte, capture *strings.Builder) error {
	return readUntilAnyToken(ctx, ch, configPromptTokens, capture)
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func sendText(a *appCtx, text string) error {
	return a.client.SendFrame(agwpe.BuildSendData(a.agwpePort, a.localCall, a.remoteCall, 0xF0, []byte(text)))
}

func runCmdExpectPrompt(ctx context.Context, a *appCtx, cmd, expect string) (string, bool) {
	if err := sendText(a, cmd); err != nil {
		return "", false
	}
	var cap strings.Builder
	ch := a.getRxCh()
	if ch == nil {
		return "", false
	}
	if err := readUntilPrompt(ctx, ch, &cap); err != nil {
		return cap.String(), false
	}
	captured := cap.String()
	if expect == "" {
		return captured, true
	}
	return captured, strings.Contains(captured, expect)
}

func runCmdExpectConfigPrompt(ctx context.Context, a *appCtx, cmd, expect string) (string, bool) {
	if err := sendText(a, cmd); err != nil {
		return "", false
	}
	var cap strings.Builder
	ch := a.getRxCh()
	if ch == nil {
		return "", false
	}
	if err := readUntilConfigPrompt(ctx, ch, &cap); err != nil {
		return cap.String(), false
	}
	captured := cap.String()
	if expect == "" {
		return captured, true
	}
	return captured, strings.Contains(captured, expect)
}

// reconnectSession shuts down the current connection and re-establishes it.
func reconnectSession(ctx context.Context, a *appCtx, connectTimeout time.Duration) bool {
	// flush any buffered rx data
	a.closeRxCh()

	// drain disconnect signal
	select {
	case <-a.disconnCh:
	default:
	}

	if err := a.client.SendFrame(agwpe.BuildDisconnectReq(a.agwpePort, a.localCall, a.remoteCall)); err != nil {
		slog.Warn("TEST-BBS: reconnect: disconnect send failed", "err", err)
	}

	select {
	case <-a.disconnCh:
	case <-time.After(connectTimeout):
		slog.Warn("TEST-BBS: reconnect: timed out waiting for disconnect")
		return false
	case <-ctx.Done():
		return false
	}

	// drain connect signal
	select {
	case <-a.connectedCh:
	default:
	}

	// open fresh rx channel
	a.openRxCh()

	if err := a.client.SendFrame(agwpe.BuildConnectReq(a.agwpePort, a.localCall, a.remoteCall)); err != nil {
		slog.Warn("TEST-BBS: reconnect: connect send failed", "err", err)
		return false
	}

	select {
	case <-a.connectedCh:
		return true
	case <-time.After(connectTimeout):
		slog.Warn("TEST-BBS: reconnect: timed out waiting for connect")
		return false
	case <-ctx.Done():
		return false
	}
}

// parseBannerCounts parses "*** You have N messages (M new)" or "*** No messages".
func parseBannerCounts(capture string) (total, newCount uint32, ok bool) {
	if strings.Contains(capture, "*** No messages for ") {
		return 0, 0, true
	}
	idx := strings.Index(capture, "*** You have ")
	if idx < 0 {
		return 0, 0, false
	}
	var t, n uint64
	s := capture[idx:]
	_, err := fmt.Sscanf(s, "*** You have %d messages (%d new)", &t, &n)
	if err != nil {
		return 0, 0, false
	}
	return uint32(t), uint32(n), true
}

// parseStoredMessageID returns the last "Stored as message N" ID found.
func parseStoredMessageID(capture string) (uint32, bool) {
	const needle = "*** Stored as message "
	var lastIdx int = -1
	search := capture
	off := 0
	for {
		i := strings.Index(search, needle)
		if i < 0 {
			break
		}
		lastIdx = off + i
		off += i + len(needle)
		search = capture[off:]
	}
	if lastIdx < 0 {
		return 0, false
	}
	rest := capture[lastIdx+len(needle):]
	var id uint64
	var err error
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return 0, false
	}
	id, err = strconv.ParseUint(fields[0], 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(id), true
}

// parseSysopChallenge extracts 4 integer indices from "CHAL i1 i2 i3 i4".
func parseSysopChallenge(capture string) ([sysopChallengeIndexCount]uint16, bool) {
	var indices [sysopChallengeIndexCount]uint16
	idx := strings.Index(capture, "CHAL ")
	if idx < 0 {
		return indices, false
	}
	rest := capture[idx:]
	var i1, i2, i3, i4 uint64
	_, err := fmt.Sscanf(rest, "CHAL %d %d %d %d", &i1, &i2, &i3, &i4)
	if err != nil {
		return indices, false
	}
	indices[0] = uint16(i1)
	indices[1] = uint16(i2)
	indices[2] = uint16(i3)
	indices[3] = uint16(i4)
	return indices, true
}

// buildSysopResponse builds a 6-char response from secret + 4 challenge indices.
func buildSysopResponse(secret string, indices [sysopChallengeIndexCount]uint16) (string, bool) {
	if len(secret) < sysopChallengeIndexCount {
		return "", false
	}
	required := make([]byte, sysopChallengeIndexCount)
	for i, idx := range indices {
		if idx == 0 || int(idx) > len(secret) {
			return "", false
		}
		required[i] = secret[idx-1]
	}
	resp := make([]byte, sysopResponseLen)
	for i := range resp {
		resp[i] = required[i%sysopChallengeIndexCount]
	}
	return string(resp), true
}

// collectSubjectIDs scans capture for message list lines containing subjectTag
// and returns their numeric IDs.
func collectSubjectIDs(capture, subjectTag string) []uint32 {
	var ids []uint32
	seen := map[uint32]bool{}
	for _, line := range strings.FieldsFunc(capture, func(r rune) bool { return r == '\n' || r == '\r' }) {
		if !strings.Contains(line, subjectTag) {
			continue
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		n, err := strconv.ParseUint(f[0], 10, 32)
		if err != nil || n == 0 {
			continue
		}
		id := uint32(n)
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

func (a *appCtx) trackCreatedID(id uint32) {
	if id == 0 {
		return
	}
	for _, existing := range a.createdIDs {
		if existing == id {
			return
		}
	}
	a.createdIDs = append(a.createdIDs, id)
}

func (a *appCtx) latestTrackedID() uint32 {
	var latest uint32
	for _, id := range a.createdIDs {
		if id > latest {
			latest = id
		}
	}
	return latest
}

func (a *appCtx) collectRunMessagesFromList(ctx context.Context) bool {
	cap, ok := runCmdExpectPrompt(ctx, a, "LL 200\r", "")
	if !ok {
		return false
	}
	for _, id := range collectSubjectIDs(cap, a.runSubjectTag) {
		a.trackCreatedID(id)
	}
	return true
}

func (a *appCtx) resolvePostedMessageID(ctx context.Context) bool {
	a.collectRunMessagesFromList(ctx)
	latest := a.latestTrackedID()
	if latest == 0 {
		return false
	}
	a.postedMsgID = latest
	return true
}

func (a *appCtx) cleanupCreatedMessages(ctx context.Context) bool {
	a.collectRunMessagesFromList(ctx)
	if len(a.createdIDs) == 0 {
		return true
	}
	for _, id := range a.createdIDs {
		cmd := fmt.Sprintf("K %d\r", id)
		runCmdExpectPrompt(ctx, a, cmd, "")
	}
	// Reset and re-query to verify no run-tagged messages remain.
	a.createdIDs = nil
	a.collectRunMessagesFromList(ctx)
	return len(a.createdIDs) == 0
}

func (a *appCtx) recordResult(name string, pass bool, detail string) {
	a.testsRun++
	if !pass {
		a.testsFailed++
	}
	status := "PASS"
	if !pass {
		status = "FAIL"
	}
	fmt.Printf("[%s] %s - %s\n", status, name, detail)
}

// ---------------------------------------------------------------------------
// reconnectAndCaptureBanner — reconnect + read banner + parse counts
// ---------------------------------------------------------------------------

func reconnectAndCaptureBanner(
	ctx context.Context,
	a *appCtx,
	connectTimeout, stepTimeout time.Duration,
) (total, newCount uint32, ok bool) {
	if !reconnectSession(ctx, a, connectTimeout) {
		return 0, 0, false
	}
	stepCtx, cancel := context.WithTimeout(ctx, stepTimeout)
	defer cancel()
	var cap strings.Builder
	ch := a.getRxCh()
	if ch == nil {
		return 0, 0, false
	}
	if err := readUntilAnyToken(stepCtx, ch, promptTokens, &cap); err != nil {
		return 0, 0, false
	}
	t, n, parsed := parseBannerCounts(cap.String())
	return t, n, parsed
}

// ---------------------------------------------------------------------------
// CONFIG mode test block
// ---------------------------------------------------------------------------

func runConfigModeTests(
	ctx context.Context,
	a *appCtx,
	sysopSecret string,
	connectTimeout, stepTimeout time.Duration,
) {
	authExpected := sysopSecret != ""
	inConfigMode := false

	// send CONFIG and read first response line
	if err := sendText(a, "CONFIG\r"); err != nil {
		label := "CONFIG no-auth entry"
		if authExpected {
			label = "CONFIG auth entry"
		}
		a.recordResult(label, false, "failed to send CONFIG command")
		return
	}

	var captured string
	{
		stepCtx, cancel := context.WithTimeout(ctx, stepTimeout)
		var firstLineCap strings.Builder
		ch := a.getRxCh()
		err := readUntilToken(stepCtx, ch, "\r", &firstLineCap)
		cancel()
		if err != nil {
			label := "CONFIG no-auth entry"
			if authExpected {
				label = "CONFIG auth entry"
			}
			a.recordResult(label, false, "timeout waiting for CONFIG response")
			goto recover
		}
		captured = firstLineCap.String()
	}

	if indices, ok := parseSysopChallenge(captured); ok {
		// --- auth path ---
		if !authExpected {
			a.recordResult("CONFIG no-auth entry", false,
				"challenge returned but -sysop-secret is empty")
			goto recover
		}
		resp, built := buildSysopResponse(sysopSecret, indices)
		if !built {
			a.recordResult("CONFIG auth entry", false,
				"unable to build sysop response from -sysop-secret")
			goto recover
		}
		if err := sendText(a, resp+"\r"); err != nil {
			a.recordResult("CONFIG auth entry", false, "failed to send challenge response")
			goto recover
		}
		stepCtx2, cancel2 := context.WithTimeout(ctx, stepTimeout)
		var authCap strings.Builder
		ch2 := a.getRxCh()
		err2 := readUntilConfigPrompt(stepCtx2, ch2, &authCap)
		cancel2()
		authCapStr := authCap.String()
		inConfigMode = err2 == nil && strings.Contains(authCapStr, "*** Entering config mode")
		pass := err2 == nil &&
			strings.Contains(authCapStr, "*** SYSOP authenticated") &&
			inConfigMode
		detail := "challenge answered and config prompt received"
		if !pass {
			detail = "challenge response rejected or config prompt missing"
		}
		a.recordResult("CONFIG auth entry", pass, detail)
		if !inConfigMode {
			goto recover
		}

	} else if strings.Contains(captured, "*** Entering config mode") {
		// --- no-auth path (first line already contains banner) ---
		if authExpected {
			a.recordResult("CONFIG auth entry", false,
				"entered config mode without challenge while -sysop-secret is set")
			stepCtx3, cancel3 := context.WithTimeout(ctx, stepTimeout)
			ch3 := a.getRxCh()
			if ch3 != nil {
				_ = readUntilConfigPrompt(stepCtx3, ch3, nil)
			}
			cancel3()
			inConfigMode = true
			goto recover
		}
		stepCtx4, cancel4 := context.WithTimeout(ctx, stepTimeout)
		var noAuthCap strings.Builder
		ch4 := a.getRxCh()
		err4 := readUntilConfigPrompt(stepCtx4, ch4, &noAuthCap)
		cancel4()
		inConfigMode = err4 == nil
		detail := "entered config mode without SYSOP challenge"
		if !inConfigMode {
			detail = "config prompt missing after entry banner"
		}
		a.recordResult("CONFIG no-auth entry", inConfigMode, detail)
		if !inConfigMode {
			goto recover
		}
	} else {
		label := "CONFIG no-auth entry"
		if authExpected {
			label = "CONFIG auth entry"
		}
		a.recordResult(label, false,
			fmt.Sprintf("unexpected CONFIG response: %.80s", captured))
		goto recover
	}

	// --- inside config mode ---
	{
		_, ok := runCmdExpectConfigPrompt(ctx, a, "get bbs.callsign\r", "bbs.callsign=")
		detail := "read-only config query succeeded"
		if !ok {
			detail = "missing bbs.callsign output"
		}
		a.recordResult("CONFIG get bbs.callsign", ok, detail)
	}

	{
		if err := sendText(a, "exit\r"); err != nil {
			a.recordResult("CONFIG exit", false, "failed to send exit command")
			goto recover
		}
		stepCtx5, cancel5 := context.WithTimeout(ctx, stepTimeout)
		var exitCap strings.Builder
		ch5 := a.getRxCh()
		err5 := readUntilPrompt(stepCtx5, ch5, &exitCap)
		cancel5()
		ok := err5 == nil && strings.Contains(exitCap.String(), "*** Exiting config mode")
		detail := "returned to BBS prompt"
		if !ok {
			detail = "failed to leave config mode"
		}
		a.recordResult("CONFIG exit", ok, detail)
	}
	return

recover:
	if inConfigMode {
		if err := sendText(a, "exit\r"); err == nil {
			stepCtx6, cancel6 := context.WithTimeout(ctx, stepTimeout)
			ch6 := a.getRxCh()
			if ch6 != nil {
				_ = readUntilPrompt(stepCtx6, ch6, nil)
			}
			cancel6()
		}
		return
	}
	if reconnectSession(ctx, a, connectTimeout) {
		stepCtx7, cancel7 := context.WithTimeout(ctx, stepTimeout)
		ch7 := a.getRxCh()
		if ch7 != nil {
			_ = readUntilPrompt(stepCtx7, ch7, nil)
		}
		cancel7()
	}
}

// ---------------------------------------------------------------------------
// Main test script
// ---------------------------------------------------------------------------

func runScript(
	ctx context.Context,
	a *appCtx,
	sysopSecret string,
	connectTimeout, stepTimeout time.Duration,
) {
	// unique tag for this run's messages
	a.runSubjectTag = fmt.Sprintf("AUTOTEST-%d", time.Now().UnixMilli())

	// ── wait for initial banner ──
	var bannerTotal, bannerNew uint32

	stepCtx, cancel := context.WithTimeout(ctx, stepTimeout)
	var bannerCap strings.Builder
	ch := a.getRxCh()
	bannerErr := readUntilAnyToken(stepCtx, ch, promptTokens, &bannerCap)
	cancel()

	if bannerErr != nil {
		// try reconnect
		if reconnectSession(ctx, a, connectTimeout) {
			stepCtx2, cancel2 := context.WithTimeout(ctx, stepTimeout)
			var bannerCap2 strings.Builder
			ch2 := a.getRxCh()
			bannerErr = readUntilAnyToken(stepCtx2, ch2, promptTokens, &bannerCap2)
			cancel2()
			if bannerErr == nil {
				bannerCap = bannerCap2
			}
		}
	}

	if bannerErr == nil {
		bannerStr := bannerCap.String()
		t, n, parsed := parseBannerCounts(bannerStr)
		bannerTotal, bannerNew = t, n
		detail := "unable to parse banner summary"
		if parsed {
			detail = fmt.Sprintf("parsed total=%d new=%d", bannerTotal, bannerNew)
		}
		a.recordResult("Banner unread summary", parsed, detail)

		hasText := strings.Contains(bannerStr, "Welcome") || strings.Contains(bannerStr, "BBS")
		detail2 := "received"
		if !hasText {
			detail2 = "missing banner text"
		}
		a.recordResult("Banner and prompt", hasText, detail2)
	} else {
		a.recordResult("Banner and prompt", false, "timeout waiting for first prompt")
	}

	// ── I command ──
	{
		_, ok := runCmdExpectPrompt(ctx, a, "I\r", "Call:")
		detail := "info returned"
		if !ok {
			detail = "missing info output"
		}
		a.recordResult("I command", ok, detail)
	}

	// ── J command ──
	{
		_, ok := runCmdExpectPrompt(ctx, a, "J\r", "Heard:")
		detail := "heard list returned"
		if !ok {
			detail = "heard output missing"
		}
		a.recordResult("J command", ok, detail)
	}

	// ── CONFIG mode ──
	runConfigModeTests(ctx, a, sysopSecret, connectTimeout, stepTimeout)

	// ── LL command ──
	{
		cap, ok := runCmdExpectPrompt(ctx, a, "LL 5\r", "# From")
		if !ok {
			ok = strings.Contains(cap, "*** No messages") ||
				strings.Contains(cap, "*** No readable messages")
		}
		detail := "list output valid"
		if !ok {
			detail = "list output invalid"
		}
		a.recordResult("LL command", ok, detail)
	}

	// ── SB post ──
	var postOK bool
	{
		if err := sendText(a, "SB TEST\r"); err == nil {
			stepCtx, cancel := context.WithTimeout(ctx, stepTimeout)
			var subCap strings.Builder
			ch := a.getRxCh()
			err = readUntilToken(stepCtx, ch, "Subject: ", &subCap)
			cancel()
			ok := err == nil
			detail := "entered subject mode"
			if !ok {
				detail = "failed to enter subject mode"
			}
			a.recordResult("SB command", ok, detail)
			postOK = ok
		} else {
			a.recordResult("SB command", false, "failed to send SB command")
		}
	}

	if postOK {
		subjectLine := a.runSubjectTag + "\r"
		if err := sendText(a, subjectLine); err == nil {
			stepCtx, cancel := context.WithTimeout(ctx, stepTimeout)
			var bodyCap strings.Builder
			ch := a.getRxCh()
			err = readUntilToken(stepCtx, ch, "Enter body, use /EX to finish:\r", &bodyCap)
			cancel()
			ok := err == nil
			detail := "body prompt received"
			if !ok {
				detail = "body prompt missing"
			}
			a.recordResult("Subject entry", ok, detail)
			postOK = ok
		} else {
			a.recordResult("Subject entry", false, "failed to send subject")
			postOK = false
		}
	}

	if postOK {
		_ = sendText(a, "line one from test_bbs\r")
		if err := sendText(a, "/EX\r"); err == nil {
			stepCtx, cancel := context.WithTimeout(ctx, stepTimeout)
			var exCap strings.Builder
			ch := a.getRxCh()
			err = readUntilAnyToken(stepCtx, ch, promptTokens, &exCap)
			cancel()
			ok := err == nil
			if ok {
				exCapStr := exCap.String()
				if id, parsed := parseStoredMessageID(exCapStr); parsed {
					a.trackCreatedID(id)
					a.postedMsgID = id
				}
				ok = a.resolvePostedMessageID(ctx)
			}
			detail := "message stored"
			if !ok {
				detail = "store confirmation missing"
			}
			a.recordResult("Post + /EX", ok, detail)
			postOK = ok
		} else {
			a.recordResult("Post + /EX", false, "failed to send /EX")
			postOK = false
		}
	}

	// ── Banner after post ──
	if a.postedMsgID != 0 {
		bAfterPostTotal, bAfterPostNew, ok := reconnectAndCaptureBanner(
			ctx, a, connectTimeout, stepTimeout)
		ok = ok &&
			bAfterPostTotal >= bannerTotal+1 &&
			bAfterPostNew <= bAfterPostTotal &&
			(bAfterPostNew == 0 || bAfterPostNew == bannerNew+1)
		detail := fmt.Sprintf("total=%d new=%d (persisted last-read may keep new=0)",
			bAfterPostTotal, bAfterPostNew)
		if !ok {
			detail = fmt.Sprintf("unexpected post banner total=%d new=%d",
				bAfterPostTotal, bAfterPostNew)
		}
		a.recordResult("Banner after post", ok, detail)

		// ── L command ──
		{
			_, ok := runCmdExpectPrompt(ctx, a, "L\r", a.runSubjectTag)
			detail := "posted message listed"
			if !ok {
				detail = "posted message missing from list"
			}
			a.recordResult("L command", ok, detail)
		}

		// ── R command ──
		{
			cmd := fmt.Sprintf("R %d\r", a.postedMsgID)
			cap, ok := runCmdExpectPrompt(ctx, a, cmd, "line one from test_bbs")
			if !ok && strings.Contains(cap, "*** Message not found") {
				// re-resolve and retry
				if a.resolvePostedMessageID(ctx) {
					cmd = fmt.Sprintf("R %d\r", a.postedMsgID)
					_, ok = runCmdExpectPrompt(ctx, a, cmd, "line one from test_bbs")
				}
			}
			detail := "message body read"
			if !ok {
				detail = "failed to read stored message"
			}
			a.recordResult("R command", ok, detail)
		}

		// ── Banner after read ──
		bAfterReadTotal, bAfterReadNew, ok2 := reconnectAndCaptureBanner(
			ctx, a, connectTimeout, stepTimeout)
		ok2 = ok2 &&
			bAfterReadTotal == bAfterPostTotal &&
			bAfterReadNew <= bAfterPostNew
		detail2 := fmt.Sprintf("total=%d new=%d", bAfterReadTotal, bAfterReadNew)
		if !ok2 {
			detail2 = fmt.Sprintf("unexpected read banner total=%d new=%d",
				bAfterReadTotal, bAfterReadNew)
		}
		a.recordResult("Banner after read", ok2, detail2)

		// ── K command ──
		{
			cmd := fmt.Sprintf("K %d\r", a.postedMsgID)
			_, ok := runCmdExpectPrompt(ctx, a, cmd, "*** Message deleted")
			detail := "message deleted"
			if !ok {
				detail = "delete confirmation missing"
			}
			a.recordResult("K command", ok, detail)
		}

		// ── R after K ──
		{
			cmd := fmt.Sprintf("R %d\r", a.postedMsgID)
			_, ok := runCmdExpectPrompt(ctx, a, cmd, "*** Message not found")
			detail := "not-found confirmed"
			if !ok {
				detail = "unexpected read result after delete"
			}
			a.recordResult("R after K", ok, detail)
		}

		// ── Banner after delete ──
		bAfterDelTotal, bAfterDelNew, ok3 := reconnectAndCaptureBanner(
			ctx, a, connectTimeout, stepTimeout)
		ok3 = ok3 && bAfterDelTotal+1 == bAfterPostTotal && bAfterDelNew == 0
		detail3 := fmt.Sprintf("total=%d new=%d", bAfterDelTotal, bAfterDelNew)
		if !ok3 {
			detail3 = fmt.Sprintf("unexpected delete banner total=%d new=%d",
				bAfterDelTotal, bAfterDelNew)
		}
		a.recordResult("Banner after delete", ok3, detail3)
	}

	// ── SP private message ──
	var privateMsgID uint32
	var privateSubject string
	privateSubject = a.runSubjectTag + "-PRIV"
	var spOK bool

	{
		if err := sendText(a, "SP N1PVT-1\r"); err == nil {
			stepCtx, cancel := context.WithTimeout(ctx, stepTimeout)
			var spCap strings.Builder
			ch := a.getRxCh()
			err = readUntilToken(stepCtx, ch, "Subject: ", &spCap)
			cancel()
			ok := err == nil
			detail := "entered private subject mode"
			if !ok {
				detail = "failed to enter private subject mode"
			}
			a.recordResult("SP private command", ok, detail)
			spOK = ok
		} else {
			a.recordResult("SP private command", false, "failed to send SP command")
		}
	}

	if spOK {
		if err := sendText(a, privateSubject+"\r"); err == nil {
			stepCtx, cancel := context.WithTimeout(ctx, stepTimeout)
			var privSubCap strings.Builder
			ch := a.getRxCh()
			err = readUntilToken(stepCtx, ch, "Enter body, use /EX to finish:\r", &privSubCap)
			cancel()
			ok := err == nil
			detail := "private body prompt received"
			if !ok {
				detail = "private body prompt missing"
			}
			a.recordResult("Private subject entry", ok, detail)
			spOK = ok
		} else {
			a.recordResult("Private subject entry", false, "failed to send private subject")
			spOK = false
		}
	}

	if spOK {
		_ = sendText(a, "private line from test_bbs\r")
		if err := sendText(a, "/EX\r"); err == nil {
			stepCtx, cancel := context.WithTimeout(ctx, stepTimeout)
			var privExCap strings.Builder
			ch := a.getRxCh()
			err = readUntilAnyToken(stepCtx, ch, promptTokens, &privExCap)
			cancel()
			ok := err == nil
			if ok {
				if id, parsed := parseStoredMessageID(privExCap.String()); parsed {
					privateMsgID = id
					a.trackCreatedID(id)
				}
			}
			detail := "private message stored"
			if !ok {
				detail = "private store confirmation missing"
			}
			a.recordResult("Private post + /EX", ok, detail)
			spOK = ok
		} else {
			a.recordResult("Private post + /EX", false, "failed to send /EX")
			spOK = false
		}
	}

	if privateMsgID != 0 {
		// private hidden from L
		{
			cap, ok := runCmdExpectPrompt(ctx, a, "L\r", "")
			ok = ok && !strings.Contains(cap, privateSubject)
			detail := "private message filtered"
			if !ok {
				detail = "private message leaked into readable list"
			}
			a.recordResult("Private hidden from L", ok, detail)
		}

		// private visible in LM
		{
			cap, ok := runCmdExpectPrompt(ctx, a, "LM\r", "")
			ok = ok && strings.Contains(cap, privateSubject)
			detail := "mine-only list includes private post"
			if !ok {
				detail = "mine-only list missing private post"
			}
			a.recordResult("Private visible in LM", ok, detail)
		}

		// private access denied for sender
		{
			cmd := fmt.Sprintf("R %d\r", privateMsgID)
			_, ok := runCmdExpectPrompt(ctx, a, cmd, "*** Access denied")
			detail := "sender cannot read non-addressed private message"
			if !ok {
				detail = "private read access control failed"
			}
			a.recordResult("Private access denied", ok, detail)
		}

		// private delete
		{
			cmd := fmt.Sprintf("K %d\r", privateMsgID)
			_, ok := runCmdExpectPrompt(ctx, a, cmd, "*** Message deleted")
			detail := "sender deleted private post"
			if !ok {
				detail = "private delete confirmation missing"
			}
			a.recordResult("Private delete", ok, detail)
		}
	} else if spOK {
		// spOK but no ID — mark private tests as failed
		a.recordResult("Private hidden from L", false, "no private message ID to test")
		a.recordResult("Private visible in LM", false, "no private message ID to test")
		a.recordResult("Private access denied", false, "no private message ID to test")
		a.recordResult("Private delete", false, "no private message ID to test")
	}

	// ── Cleanup ──
	{
		ok := a.cleanupCreatedMessages(ctx)
		detail := "no run-tagged messages left"
		if !ok {
			detail = "some run-tagged messages remain"
		}
		a.recordResult("Cleanup created messages", ok, detail)
	}

	// ── SP validation ──
	{
		_, ok := runCmdExpectPrompt(ctx, a, "SP BAD\r", "*** SP requires valid callsign")
		detail := "invalid callsign rejected"
		if !ok {
			detail = "invalid callsign accepted"
		}
		a.recordResult("SP validation", ok, detail)
	}

	// ── Unknown command ──
	{
		_, ok := runCmdExpectPrompt(ctx, a, "XYZ\r", "Commands:")
		detail := "help shown"
		if !ok {
			detail = "help not shown"
		}
		a.recordResult("Unknown command help", ok, detail)
	}

	// ── B disconnect ──
	{
		_ = sendText(a, "B\r")
		select {
		case <-a.disconnCh:
			a.recordResult("B disconnect", true, "remote disconnected")
		case <-time.After(stepTimeout):
			a.recordResult("B disconnect", false, "disconnect timeout")
		case <-ctx.Done():
			a.recordResult("B disconnect", false, "context cancelled")
		}
	}
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	localCall := flag.String("local", "", "local callsign, e.g. N7GET-9 (required)")
	remoteCall := flag.String("remote", "", "BBS callsign, e.g. W1AW-1 (required)")
	agwpeAddr := flag.String("agwpe", "localhost:8000", "AGWPE server host:port")
	sysopSecret := flag.String("sysop-secret", "", "sysop password for CONFIG auth test (empty = no-auth)")
	connectTimeoutMs := flag.Int("connect-timeout", 15000, "connection timeout in ms")
	stepTimeoutMs := flag.Int("step-timeout", 10000, "per-command timeout in ms")
	debug := flag.Bool("debug", false, "enable debug logging")
	flag.Parse()

	if *debug {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr,
			&slog.HandlerOptions{Level: slog.LevelDebug})))
	}

	// validate required flags
	ok := true
	if *localCall == "" {
		fmt.Fprintln(os.Stderr, "error: -local is required")
		ok = false
	}
	if *remoteCall == "" {
		fmt.Fprintln(os.Stderr, "error: -remote is required")
		ok = false
	}
	if *agwpeAddr == "" {
		fmt.Fprintln(os.Stderr, "error: -agwpe is required")
		ok = false
	}
	if !ok {
		flag.Usage()
		os.Exit(2)
	}

	// parse -agwpe host:port
	lastColon := strings.LastIndex(*agwpeAddr, ":")
	if lastColon < 0 {
		fmt.Fprintf(os.Stderr, "error: -agwpe must be host:port, got %q\n", *agwpeAddr)
		os.Exit(2)
	}
	agwpeHost := (*agwpeAddr)[:lastColon]
	agwpePort, err := strconv.Atoi((*agwpeAddr)[lastColon+1:])
	if err != nil || agwpePort < 1 || agwpePort > 65535 {
		fmt.Fprintf(os.Stderr, "error: -agwpe invalid port in %q\n", *agwpeAddr)
		os.Exit(2)
	}

	connectTimeout := time.Duration(*connectTimeoutMs) * time.Millisecond
	stepTimeout := time.Duration(*stepTimeoutMs) * time.Millisecond

	// ── AGWPE client ──
	a := newAppCtx()
	a.localCall = *localCall
	a.remoteCall = *remoteCall
	a.agwpePort = 0

	client, err := agwpe.NewClient(agwpe.ClientConfig{
		Host:           agwpeHost,
		Port:           uint16(agwpePort),
		ConnectTimeout: connectTimeout,
		ReconnectDelay: 5 * time.Second,
		OnRxFrame:      a.handleFrame,
		OnError:        func(e error) { slog.Error("TEST-BBS: AGWPE error", "err", e) },
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: create AGWPE client: %v\n", err)
		os.Exit(1)
	}
	a.client = client

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a.openRxCh()
	client.Start()
	slog.Info("TEST-BBS: AGWPE client started", "addr", *agwpeAddr)

	// register callsign and initiate connection, retrying until the AGWPE
	// TCP connection is established
	go func() {
		for {
			time.Sleep(500 * time.Millisecond)
			if err := client.SendFrame(agwpe.BuildRegisterCall(0, *localCall)); err != nil {
				slog.Debug("TEST-BBS: waiting for AGWPE connection", "err", err)
				continue
			}
			if err := client.SendFrame(agwpe.BuildConnectReq(0, *localCall, *remoteCall)); err != nil {
				slog.Debug("TEST-BBS: connect req failed", "err", err)
				continue
			}
			slog.Info("TEST-BBS: connect request sent",
				"local", *localCall, "remote", *remoteCall)
			return
		}
	}()

	// wait for connect
	select {
	case <-a.connectedCh:
		slog.Info("TEST-BBS: AX.25 connected")
	case <-time.After(connectTimeout):
		fmt.Fprintln(os.Stderr, "error: timed out waiting for AX.25 connection")
		os.Exit(1)
	}

	// handle Ctrl+C
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\ninterrupted")
		cancel()
	}()

	// ── run tests ──
	runScript(ctx, a, *sysopSecret, connectTimeout, stepTimeout)

	// ── summary ──
	passed := a.testsRun - a.testsFailed
	fmt.Printf("========================================\n")
	fmt.Printf("BBS tests complete: run=%d failed=%d passed=%d\n",
		a.testsRun, a.testsFailed, passed)
	fmt.Printf("========================================\n")

	client.Stop()

	if a.testsFailed > 0 {
		os.Exit(1)
	}
}
