// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"os"
	"testing"
)

func TestConfig_Defaults(t *testing.T) {
	cfg := NewConfig(nil)
	if got := cfg.GetStr("beacon.destination", ""); got != "BEACON" {
		t.Errorf("beacon.destination default: got %q, want \"BEACON\"", got)
	}
	if got := cfg.GetInt("beacon.every", -1); got != 0 {
		t.Errorf("beacon.every default: got %d, want 0", got)
	}
}

func TestConfig_Set(t *testing.T) {
	cfg := NewConfig(nil)
	cfg.Set("beacon.source", "N7GET-1")
	if got := cfg.GetStr("beacon.source", ""); got != "N7GET-1" {
		t.Errorf("beacon.source: got %q, want \"N7GET-1\"", got)
	}
}

func TestConfig_LoadINI(t *testing.T) {
	f, err := os.CreateTemp("", "ax25_config_*.ini")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(f.Name())
	f.WriteString("beacon.source = W1AW\n")
	f.WriteString("# comment\n")
	f.WriteString("beacon.every = 5\n")
	f.Close()

	cfg := NewConfig(nil)
	if err := cfg.LoadINI(f.Name()); err != nil {
		t.Fatalf("LoadINI: %v", err)
	}
	if got := cfg.GetStr("beacon.source", ""); got != "W1AW" {
		t.Errorf("beacon.source: got %q, want \"W1AW\"", got)
	}
	if got := cfg.GetInt("beacon.every", 0); got != 5 {
		t.Errorf("beacon.every: got %d, want 5", got)
	}
}

func TestConfig_MissingFile(t *testing.T) {
	cfg := NewConfig(nil)
	if err := cfg.LoadINI("/nonexistent/path/config.ini"); err != nil {
		t.Errorf("LoadINI missing file: expected nil, got %v", err)
	}
}
