// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"os"
	"strings"
	"testing"
)

func requirePanicContains(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic containing %q, got none", want)
		}
		got := r.(string)
		if !strings.Contains(got, want) {
			t.Fatalf("panic mismatch: got %q, want substring %q", got, want)
		}
	}()
	fn()
}

// TestSchemaKeysMatchConsts verifies that every key in DefaultSchema has a
// corresponding entry in allConfigKeys (the core const list), and vice versa.
// This catches schema additions that are missing a const and consts that no
// longer correspond to any schema entry.
func TestSchemaKeysMatchConsts(t *testing.T) {
	// Build set from allConfigKeys.
	constSet := make(map[ConfigKey]bool, len(allConfigKeys))
	for _, k := range allConfigKeys {
		constSet[k] = true
	}

	// Every DefaultSchema key must appear in allConfigKeys.
	for _, p := range DefaultSchema {
		if !constSet[p.Key] {
			t.Errorf("schema key %q has no matching const in allConfigKeys", p.Key)
		}
	}

	// Every allConfigKeys entry must appear in DefaultSchema.
	schemaSet := make(map[ConfigKey]bool, len(DefaultSchema))
	for _, p := range DefaultSchema {
		schemaSet[p.Key] = true
	}
	for _, k := range allConfigKeys {
		if !schemaSet[k] {
			t.Errorf("const %q in allConfigKeys has no matching entry in DefaultSchema", k)
		}
	}
}

func TestDefaultSchema_NoDuplicateKeys(t *testing.T) {
	seen := make(map[ConfigKey]bool, len(DefaultSchema))
	for _, p := range DefaultSchema {
		if seen[p.Key] {
			t.Fatalf("duplicate key in DefaultSchema: %q", p.Key)
		}
		seen[p.Key] = true
	}
}

func TestConfig_Defaults(t *testing.T) {
	cfg := NewConfig(nil)
	if got := cfg.GetStr(KeyBeaconDestination); got != "BEACON" {
		t.Errorf("beacon.destination default: got %q, want \"BEACON\"", got)
	}
	if got := cfg.GetInt(KeyBeaconEvery); got != 0 {
		t.Errorf("beacon.every default: got %d, want 0", got)
	}
	if got := cfg.GetInt(KeyAgwpeServerMaxClients); got != 16 {
		t.Errorf("agwpe.server.max_clients default: got %d, want 16", got)
	}
	if got := cfg.GetInt(KeyKissServerMaxClients); got != 16 {
		t.Errorf("kiss.server.max_clients default: got %d, want 16", got)
	}
	if got := cfg.GetBool(KeyMonitorEnabled); got {
		t.Errorf("monitor.enabled default: got true, want false")
	}
	if got := cfg.GetStr(KeyMonitorType); got != "ax25" {
		t.Errorf("monitor.type default: got %q, want \"ax25\"", got)
	}
	if got := cfg.GetStr(KeyMonitorPrefix); got != "monitor" {
		t.Errorf("monitor.prefix default: got %q, want \"monitor\"", got)
	}
}

func TestConfig_Set(t *testing.T) {
	cfg := NewConfig(nil)
	cfg.Set(KeyBeaconSource, "N7GET-1")
	if got := cfg.GetStr(KeyBeaconSource); got != "N7GET-1" {
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
	if got := cfg.GetStr(KeyBeaconSource); got != "W1AW" {
		t.Errorf("beacon.source: got %q, want \"W1AW\"", got)
	}
	if got := cfg.GetInt(KeyBeaconEvery); got != 5 {
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
	if got := cfg.GetStr(KeyBeaconSource); got != "W1AW" {
		t.Errorf("beacon.source: got %q, want \"W1AW\"", got)
	}
	if got := cfg.GetInt(KeyBeaconEvery); got != 10 {
		t.Errorf("beacon.every: got %d, want 10", got)
	}
	if got := cfg.GetStr(KeyKissClientHost); got != "192.168.1.1" {
		t.Errorf("kiss.client.host: got %q, want \"192.168.1.1\"", got)
	}
	if got := cfg.GetInt(KeyKissClientPort); got != 9001 {
		t.Errorf("kiss.client.port: got %d, want 9001", got)
	}
}

func TestConfig_GetBool(t *testing.T) {
	cfg := NewConfig(nil)
	cfg.Set(KeyKissSerialEnabled, "true")
	if got := cfg.GetBool(KeyKissSerialEnabled); !got {
		t.Errorf("GetBool: got false, want true")
	}
	cfg.Set(KeyKissSerialEnabled, "false")
	if got := cfg.GetBool(KeyKissSerialEnabled); got {
		t.Errorf("GetBool: got true, want false")
	}
	requirePanicContains(t, "missing key \"missing\"", func() {
		_ = cfg.GetBool(ConfigKey("missing"))
	})
}

func TestConfig_GetBool_Aliases(t *testing.T) {
	cfg := NewConfig(nil)
	for _, v := range []string{"1", "true", "TRUE", "True", "t", "T"} {
		cfg.Set(KeyKissSerialEnabled, v)
		if got := cfg.GetBool(KeyKissSerialEnabled); !got {
			t.Errorf("GetBool(%q): got false, want true", v)
		}
	}
	for _, v := range []string{"0", "false", "FALSE", "False", "f", "F"} {
		cfg.Set(KeyKissSerialEnabled, v)
		if got := cfg.GetBool(KeyKissSerialEnabled); got {
			t.Errorf("GetBool(%q): got true, want false", v)
		}
	}
	// Invalid value panics.
	cfg.Set(KeyKissSerialEnabled, "maybe")
	requirePanicContains(t, "invalid bool for key \"kiss.serial.enabled\": \"maybe\"", func() {
		_ = cfg.GetBool(KeyKissSerialEnabled)
	})
}

func TestConfig_Get_NotFoundPanics(t *testing.T) {
	cfg := NewConfig(nil)
	requirePanicContains(t, "missing key \"does.not.exist\"", func() {
		_ = cfg.Get(ConfigKey("does.not.exist"))
	})
}

func TestConfig_GetInt_Invalid(t *testing.T) {
	cfg := NewConfig(nil)
	cfg.Set(KeyBeaconEvery, "notanumber")
	requirePanicContains(t, "invalid int for key \"beacon.every\": \"notanumber\"", func() {
		_ = cfg.GetInt(KeyBeaconEvery)
	})
}

func TestConfig_GetStr_MissingPanics(t *testing.T) {
	cfg := NewConfig(nil)
	requirePanicContains(t, "missing key \"does.not.exist\"", func() {
		_ = cfg.GetStr(ConfigKey("does.not.exist"))
	})
}

func TestConfig_GetInt_MissingPanics(t *testing.T) {
	cfg := NewConfig(nil)
	requirePanicContains(t, "missing key \"does.not.exist\"", func() {
		_ = cfg.GetInt(ConfigKey("does.not.exist"))
	})
}

func TestConfig_Set_Overwrite(t *testing.T) {
	cfg := NewConfig(nil)
	cfg.Set(KeyBeaconDestination, "FIRST")
	cfg.Set(KeyBeaconDestination, "SECOND")
	if got := cfg.GetStr(KeyBeaconDestination); got != "SECOND" {
		t.Errorf("Set overwrite: got %q, want \"SECOND\"", got)
	}
}

func TestConfig_ExtraSchema(t *testing.T) {
	extra := []ConfigParam{
		{Key: "bbs.sysop", DefaultValue: "SYSOP", Description: "BBS sysop callsign"},
	}
	cfg := NewConfig(extra)
	if got := cfg.GetStr(ConfigKey("bbs.sysop")); got != "SYSOP" {
		t.Errorf("extra schema default: got %q, want \"SYSOP\"", got)
	}
}

func TestConfig_LoadINI_SemicolonComment(t *testing.T) {
	f, err := os.CreateTemp("", "ax25_config_*.ini")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(f.Name())
	f.WriteString("; semicolon comment\n")
	f.WriteString("beacon.source = K0DER\n")
	f.Close()

	cfg := NewConfig(nil)
	if err := cfg.LoadINI(f.Name()); err != nil {
		t.Fatalf("LoadINI: %v", err)
	}
	if got := cfg.GetStr(KeyBeaconSource); got != "K0DER" {
		t.Errorf("beacon.source: got %q, want \"K0DER\"", got)
	}
}

func TestConfig_LoadINI_MalformedLine(t *testing.T) {
	f, err := os.CreateTemp("", "ax25_config_*.ini")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(f.Name())
	f.WriteString("this line has no equals sign\n")
	f.WriteString("beacon.source = W1AW\n")
	f.Close()

	cfg := NewConfig(nil)
	if err := cfg.LoadINI(f.Name()); err != nil {
		t.Fatalf("LoadINI: %v", err)
	}
	// Malformed line is silently skipped; valid line is loaded.
	if got := cfg.GetStr(KeyBeaconSource); got != "W1AW" {
		t.Errorf("beacon.source: got %q, want \"W1AW\"", got)
	}
}

func TestConfig_LoadINI_InlineCommentAndQuotes(t *testing.T) {
	f, err := os.CreateTemp("", "ax25_config_*.ini")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(f.Name())
	f.WriteString("[beacon]\n")
	f.WriteString("source = W1AW # the ARRL beacon\n")
	f.WriteString(`text = "hello world"` + "\n")
	f.Close()

	cfg := NewConfig(nil)
	if err := cfg.LoadINI(f.Name()); err != nil {
		t.Fatalf("LoadINI: %v", err)
	}
	if got := cfg.GetStr(KeyBeaconSource); got != "W1AW" {
		t.Errorf("beacon.source: got %q, want \"W1AW\"", got)
	}
	if got := cfg.GetStr(KeyBeaconText); got != "hello world" {
		t.Errorf("beacon.text: got %q, want \"hello world\"", got)
	}
}
