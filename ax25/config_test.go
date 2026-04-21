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

func TestConfig_GetBool_Aliases(t *testing.T) {
	cfg := NewConfig(nil)
	for _, v := range []string{"1", "true", "yes", "on", "TRUE", "YES", "ON"} {
		cfg.Set("b", v)
		if got := cfg.GetBool("b", false); !got {
			t.Errorf("GetBool(%q): got false, want true", v)
		}
	}
	for _, v := range []string{"0", "false", "no", "off", "FALSE", "NO", "OFF"} {
		cfg.Set("b", v)
		if got := cfg.GetBool("b", true); got {
			t.Errorf("GetBool(%q): got true, want false", v)
		}
	}
	// Invalid value → default
	cfg.Set("b", "maybe")
	if got := cfg.GetBool("b", true); !got {
		t.Errorf("GetBool invalid value: got false, want default true")
	}
}

func TestConfig_Get_NotFound(t *testing.T) {
	cfg := NewConfig(nil)
	_, err := cfg.Get("does.not.exist")
	if err != ErrConfigKeyNotFound {
		t.Errorf("Get missing key: got %v, want ErrConfigKeyNotFound", err)
	}
}

func TestConfig_GetInt_Invalid(t *testing.T) {
	cfg := NewConfig(nil)
	cfg.Set("num", "notanumber")
	if got := cfg.GetInt("num", 42); got != 42 {
		t.Errorf("GetInt invalid value: got %d, want 42", got)
	}
}

func TestConfig_Set_Overwrite(t *testing.T) {
	cfg := NewConfig(nil)
	cfg.Set("beacon.destination", "FIRST")
	cfg.Set("beacon.destination", "SECOND")
	if got := cfg.GetStr("beacon.destination", ""); got != "SECOND" {
		t.Errorf("Set overwrite: got %q, want \"SECOND\"", got)
	}
}

func TestConfig_ExtraSchema(t *testing.T) {
	extra := []ConfigParam{
		{Key: "bbs.sysop", DefaultValue: "SYSOP", Description: "BBS sysop callsign"},
	}
	cfg := NewConfig(extra)
	if got := cfg.GetStr("bbs.sysop", ""); got != "SYSOP" {
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
	if got := cfg.GetStr("beacon.source", ""); got != "K0DER" {
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
	if got := cfg.GetStr("beacon.source", ""); got != "W1AW" {
		t.Errorf("beacon.source: got %q, want \"W1AW\"", got)
	}
}

func TestParseINIValue_UnclosedQuote(t *testing.T) {
	// An unclosed quoted string returns everything after the opening quote.
	got := parseINIValue(`"unclosed`)
	if got != "unclosed" {
		t.Errorf("parseINIValue unclosed quote: got %q, want \"unclosed\"", got)
	}
}

func TestParseINIValue(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// Inline comment stripping
		{"value # inline comment", "value"},
		{"value", "value"},
		{"# whole line comment", ""},
		// Escaped hash – backslash consumed, # preserved
		{`value \# not a comment`, "value # not a comment"},
		// Quoted values
		{`"hello world"`, "hello world"},
		{`"has # hash inside"`, "has # hash inside"},
		{`"escaped \" quote"`, `escaped " quote`},
		// Trailing whitespace trimmed in unquoted
		{"value   # comment", "value"},
		// Empty
		{"", ""},
	}
	for _, tt := range tests {
		got := parseINIValue(tt.input)
		if got != tt.want {
			t.Errorf("parseINIValue(%q) = %q, want %q", tt.input, got, tt.want)
		}
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
	f.WriteString(`destination = BEACON \# not a comment` + "\n")
	f.Close()

	cfg := NewConfig(nil)
	if err := cfg.LoadINI(f.Name()); err != nil {
		t.Fatalf("LoadINI: %v", err)
	}
	if got := cfg.GetStr("beacon.source", ""); got != "W1AW" {
		t.Errorf("beacon.source: got %q, want \"W1AW\"", got)
	}
	if got := cfg.GetStr("beacon.text", ""); got != "hello world" {
		t.Errorf("beacon.text: got %q, want \"hello world\"", got)
	}
	if got := cfg.GetStr("beacon.destination", ""); got != "BEACON # not a comment" {
		t.Errorf("beacon.destination: got %q, want \"BEACON # not a comment\"", got)
	}
}
