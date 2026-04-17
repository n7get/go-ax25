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

func TestConfig_LoadINI_Sections(t *testing.T) {
	f, err := os.CreateTemp("", "ax25_config_*.ini")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(f.Name())
	f.WriteString("[beacon]\n")
	f.WriteString("source = W1AW\n")
	f.WriteString("every = 10\n")
	f.WriteString("\n")
	f.WriteString("[kiss.client]\n")
	f.WriteString("host = 192.168.1.1\n")
	f.WriteString("port = 9001\n")
	f.Close()

	cfg := NewConfig(nil)
	if err := cfg.LoadINI(f.Name()); err != nil {
		t.Fatalf("LoadINI: %v", err)
	}
	if got := cfg.GetStr("beacon.source", ""); got != "W1AW" {
		t.Errorf("beacon.source: got %q, want \"W1AW\"", got)
	}
	if got := cfg.GetInt("beacon.every", 0); got != 10 {
		t.Errorf("beacon.every: got %d, want 10", got)
	}
	if got := cfg.GetStr("kiss.client.host", ""); got != "192.168.1.1" {
		t.Errorf("kiss.client.host: got %q, want \"192.168.1.1\"", got)
	}
	if got := cfg.GetInt("kiss.client.port", 0); got != 9001 {
		t.Errorf("kiss.client.port: got %d, want 9001", got)
	}
}

func TestConfig_GetBool(t *testing.T) {
	cfg := NewConfig(nil)
	cfg.Set("foo", "true")
	if got := cfg.GetBool("foo", false); !got {
		t.Errorf("GetBool: got false, want true")
	}
	cfg.Set("foo", "false")
	if got := cfg.GetBool("foo", true); got {
		t.Errorf("GetBool: got true, want false")
	}
	if got := cfg.GetBool("missing", true); !got {
		t.Errorf("GetBool missing key: got false, want default true")
	}
}
