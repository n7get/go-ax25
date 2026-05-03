package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCaptureFileName(t *testing.T) {
	got := captureFileName("/tmp/frames", "260503", 7)
	want := "/tmp/frames-26050307.pcap"
	if got != want {
		t.Fatalf("captureFileName() = %q, want %q", got, want)
	}
}

func TestFirstAvailableVV(t *testing.T) {
	dir := t.TempDir()
	prefix := filepath.Join(dir, "router")
	day := "260503"

	for i := 0; i < 3; i++ {
		name := captureFileName(prefix, day, i)
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			t.Fatalf("seed %q: %v", name, err)
		}
	}

	vv, err := firstAvailableVV(prefix, day)
	if err != nil {
		t.Fatalf("firstAvailableVV: %v", err)
	}
	if vv != 3 {
		t.Fatalf("firstAvailableVV = %d, want 3", vv)
	}
}

func TestRotateSIGHUPIncrementsVV(t *testing.T) {
	dir := t.TempDir()
	prefix := filepath.Join(dir, "router")

	m, err := newFrameMonitor(monitorTypeAX25, prefix)
	if err != nil {
		t.Fatalf("newFrameMonitor: %v", err)
	}
	defer m.Close()

	m.mu.Lock()
	day := m.currentDay
	vv := m.currentVV
	m.mu.Unlock()

	if err := m.RotateSIGHUP(); err != nil {
		t.Fatalf("RotateSIGHUP: %v", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.currentDay != day {
		t.Fatalf("currentDay = %q, want %q", m.currentDay, day)
	}
	if m.currentVV != vv+1 {
		t.Fatalf("currentVV = %d, want %d", m.currentVV, vv+1)
	}
}

func TestRotateSIGHUPVVExhausted(t *testing.T) {
	dir := t.TempDir()
	prefix := filepath.Join(dir, "router")

	m, err := newFrameMonitor(monitorTypeAX25, prefix)
	if err != nil {
		t.Fatalf("newFrameMonitor: %v", err)
	}
	defer m.Close()

	m.mu.Lock()
	m.currentVV = 99
	m.mu.Unlock()

	err = m.RotateSIGHUP()
	if !errors.Is(err, errMonitorVVExhausted) {
		t.Fatalf("RotateSIGHUP error = %v, want %v", err, errMonitorVVExhausted)
	}
}

func TestNextMidnightUsesLocalClock(t *testing.T) {
	loc := time.FixedZone("LOCALPLUS2", 2*60*60)
	now := time.Date(2026, 5, 3, 23, 59, 1, 0, loc)
	next := nextMidnight(now)
	want := time.Date(2026, 5, 4, 0, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("nextMidnight = %v, want %v", next, want)
	}
}
