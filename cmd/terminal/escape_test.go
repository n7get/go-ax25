// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProcessLine(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantData []byte // expected single payload (nil if wantDisc or wantErr)
		wantDisc bool
		wantErr  bool
	}{
		// Whole-line escapes
		{name: "disconnect ~.", line: "~.", wantDisc: true},
		{name: "literal tilde ~~", line: "~~", wantData: []byte("~\r")},

		// Regular lines
		{name: "empty line", line: "", wantData: []byte("\r")},
		{name: "normal text", line: "hello", wantData: []byte("hello\r")},
		{name: "tilde not at start", line: "hi ~ there", wantData: []byte("hi ~ there\r")},

		// Inline control character escapes \a-\z
		{name: `\a ctrl-a`, line: `\a`, wantData: []byte{0x01, '\r'}},
		{name: `\c ctrl-c`, line: `\c`, wantData: []byte{0x03, '\r'}},
		{name: `\z ctrl-z`, line: `\z`, wantData: []byte{0x1A, '\r'}},
		{name: `inline \z in text`, line: `hello\zworld`, wantData: []byte{'h', 'e', 'l', 'l', 'o', 0x1A, 'w', 'o', 'r', 'l', 'd', '\r'}},

		// Inline byte value escapes \<digits>
		{name: `\0 byte 0`, line: `\0`, wantData: []byte{0x00, '\r'}},
		{name: `\3 byte 3`, line: `\3`, wantData: []byte{0x03, '\r'}},
		{name: `\27 ESC`, line: `\27`, wantData: []byte{0x1B, '\r'}},
		{name: `\255 0xFF`, line: `\255`, wantData: []byte{0xFF, '\r'}},
		{name: `\256 out of range literal`, line: `\256`, wantData: []byte{'\\', '2', '5', '6', '\r'}},
		{name: `inline \27 in text`, line: `esc\27end`, wantData: []byte{'e', 's', 'c', 0x1B, 'e', 'n', 'd', '\r'}},

		// Inline literal backslash \\
		{name: `\\ literal backslash`, line: `\\`, wantData: []byte{'\\', '\r'}},
		{name: `\\ inline`, line: `a\\b`, wantData: []byte{'a', '\\', 'b', '\r'}},

		// Unknown escape passes through literally
		{name: `unknown escape \A`, line: `\A`, wantData: []byte{'\\', 'A', '\r'}},

		// Trailing backslash passes through
		{name: `trailing backslash`, line: `abc\`, wantData: []byte{'a', 'b', 'c', '\\', '\r'}},

		// ~! alone is an error
		{name: "~! alone", line: "~!", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, disc, _, err := processLine(tt.line)
			if (err != nil) != tt.wantErr {
				t.Errorf("processLine(%q) error = %v, wantErr %v", tt.line, err, tt.wantErr)
				return
			}
			if disc != tt.wantDisc {
				t.Errorf("processLine(%q) disc = %v, want %v", tt.line, disc, tt.wantDisc)
			}
			if tt.wantData == nil {
				return
			}
			if len(result) != 1 {
				t.Fatalf("processLine(%q) returned %d payloads, want 1", tt.line, len(result))
			}
			if string(result[0]) != string(tt.wantData) {
				t.Errorf("processLine(%q) = %q, want %q", tt.line, result[0], tt.wantData)
			}
		})
	}
}

func TestProcessLineFile(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	// File contains inline escapes too to verify they are expanded
	if err := os.WriteFile(testFile, []byte("line1\nhi\\3there\nline3\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, disc, echoLocally, err := processLine("~!" + testFile)
	if err != nil {
		t.Fatalf("processLine() error = %v", err)
	}
	if disc {
		t.Error("processLine() disc = true, want false")
	}
	if !echoLocally {
		t.Error("processLine() echoLocally = false, want true")
	}
	want := [][]byte{
		[]byte("line1\r"),
		{'h', 'i', 0x03, 't', 'h', 'e', 'r', 'e', '\r'},
		[]byte("line3\r"),
	}
	if len(result) != len(want) {
		t.Fatalf("processLine() len = %d, want %d; got %q", len(result), len(want), result)
	}
	for i := range want {
		if string(result[i]) != string(want[i]) {
			t.Errorf("result[%d] = %q, want %q", i, result[i], want[i])
		}
	}
}

func TestProcessLineFileNotFound(t *testing.T) {
	result, disc, _, err := processLine("~!/nonexistent/file.txt")
	if err == nil {
		t.Error("expected error, got nil")
	}
	if disc {
		t.Error("disc = true, want false")
	}
	if result != nil {
		t.Errorf("result = %v, want nil", result)
	}
}
