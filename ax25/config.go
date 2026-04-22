// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	ini "gopkg.in/ini.v1"
)

// ---------------------------------------------------------------------------
// Config — INI-file backed runtime configuration
// ---------------------------------------------------------------------------

// ConfigParam describes a single configuration parameter.
type ConfigParam struct {
	Key          string // dotted key, e.g. "beacon.source"
	DefaultValue string
	Description  string
}

// Config holds the runtime configuration loaded from an INI file.
type Config struct {
	mu     sync.RWMutex
	values map[string]string
	schema []ConfigParam
}

var (
	ErrConfigKeyNotFound = errors.New("ax25: config key not found")
	ErrConfigNotInit     = errors.New("ax25: config not initialised")
)

// DefaultSchema is the built-in parameter schema.
var DefaultSchema = []ConfigParam{
	// Station identity
	{Key: "station.callsign", DefaultValue: "N0CALL", Description: "Station callsign"},
	{Key: "station.ssid", DefaultValue: "0", Description: "Station SSID (0-15)"},

	// Beacon
	{Key: "beacon.source", DefaultValue: "", Description: "Beacon source callsign (empty = disabled)"},
	{Key: "beacon.destination", DefaultValue: "BEACON", Description: "Beacon destination callsign"},
	{Key: "beacon.via", DefaultValue: "", Description: "Comma-separated digipeater path"},
	{Key: "beacon.text", DefaultValue: "go-ax25", Description: "Beacon text (supports \\r \\n \\xHH escapes)"},
	{Key: "beacon.every", DefaultValue: "0", Description: "Beacon interval in minutes (0 = disabled)"},

	// Digipeater
	{Key: "digi.callsign", DefaultValue: "", Description: "Digipeater callsign (empty = disabled)"},

	// AX.25 connected-mode timers
	{Key: "conn.t1_ms", DefaultValue: "10000", Description: "T1 acknowledgement timeout (ms)"},
	{Key: "conn.t2_ms", DefaultValue: "1000", Description: "T2 response delay timeout (ms)"},
	{Key: "conn.t3_ms", DefaultValue: "180000", Description: "T3 inactive link timeout (ms)"},
	{Key: "conn.n2_retries", DefaultValue: "10", Description: "N2 maximum retry count"},
	{Key: "conn.window_size", DefaultValue: "4", Description: "k: maximum outstanding I-frames (1-7)"},

	// Router
	{Key: "router.port_queue_depth", DefaultValue: "32", Description: "Default per-port frame queue depth"},

	// AGWPE client
	{Key: "agwpe.client.read_buf", DefaultValue: "4132", Description: "AGWPE client rx read buffer size (bytes)"},
	{Key: "agwpe.client.tx_queue_depth", DefaultValue: "8", Description: "AGWPE client TX channel depth"},

	// AGWPE server
	{Key: "agwpe.server.port", DefaultValue: "8000", Description: "AGWPE TCP listen port"},
	{Key: "agwpe.server.read_buf", DefaultValue: "4132", Description: "AGWPE server rx read buffer size (bytes)"},
	{Key: "agwpe.server.tx_queue_depth", DefaultValue: "64", Description: "AGWPE server TX channel depth"},
	{Key: "agwpe.server.max_conns", DefaultValue: "4", Description: "AGWPE server max simultaneous AX.25 connections"},

	// KISS serial PHY
	{Key: "kiss.serial.device", DefaultValue: "/dev/ttyUSB0", Description: "Serial device for KISS TNC"},
	{Key: "kiss.serial.baud", DefaultValue: "9600", Description: "Serial baud rate"},
	{Key: "kiss.serial.read_buf", DefaultValue: "1024", Description: "KISS serial rx read buffer size (bytes)"},
	{Key: "kiss.serial.rx_queue_depth", DefaultValue: "64", Description: "KISS serial rx frame queue depth"},
	{Key: "kiss.serial.tx_queue_depth", DefaultValue: "32", Description: "KISS serial tx frame queue depth"},

	// KISS TCP client PHY
	{Key: "kiss.client.read_buf", DefaultValue: "4096", Description: "KISS TCP client rx read buffer size (bytes)"},
	{Key: "kiss.client.tx_queue_depth", DefaultValue: "8", Description: "KISS TCP client TX channel depth"},

	// KISS TCP server PHY
	{Key: "kiss.server.port", DefaultValue: "8100", Description: "KISS TCP listen port"},
	{Key: "kiss.server.read_buf", DefaultValue: "4096", Description: "KISS TCP server rx read buffer size per client (bytes)"},
	{Key: "kiss.server.tx_queue_depth", DefaultValue: "8", Description: "KISS TCP server TX channel depth per client"},
}

// NewConfig creates a Config with the given schema (merged with DefaultSchema).
func NewConfig(extra []ConfigParam) *Config {
	c := &Config{
		values: make(map[string]string),
	}
	c.schema = append(c.schema, DefaultSchema...)
	c.schema = append(c.schema, extra...)
	// Seed defaults.
	for _, p := range c.schema {
		c.values[p.Key] = p.DefaultValue
	}
	return c
}

// LoadINI reads key=value pairs from an INI file, ignoring comments and blanks.
func (c *Config) LoadINI(path string) error {
	_, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // missing file is not an error
		}
		return fmt.Errorf("ax25: config: stat %q: %w", path, err)
	}

	file, err := ini.LoadSources(ini.LoadOptions{
		SkipUnrecognizableLines: true,
		IgnoreInlineComment:     true,
	}, path)
	if err != nil {
		return fmt.Errorf("ax25: config: parse %q: %w", path, err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for _, section := range file.Sections() {
		sectionName := section.Name()
		for _, key := range section.Keys() {
			cfgKey := key.Name()
			if sectionName != ini.DefaultSection {
				cfgKey = sectionName + "." + cfgKey
			}
			// Keep compatibility with previous parser behavior for inline comments,
			// escaping, and quoted strings.
			c.values[cfgKey] = parseINIValue(strings.TrimSpace(key.Value()))
		}
	}
	return nil
}

// parseINIValue strips an inline # comment (unless escaped with \) and
// unwraps a double-quoted value. Inside quotes, \" is treated as a literal
// quote character. Outside quotes, \# is treated as a literal # (backslash
// consumed).
func parseINIValue(raw string) string {
	if len(raw) == 0 {
		return raw
	}
	// Quoted value.
	if raw[0] == '"' {
		var buf strings.Builder
		i := 1
		for i < len(raw) {
			ch := raw[i]
			if ch == '\\' && i+1 < len(raw) && raw[i+1] == '"' {
				buf.WriteByte('"')
				i += 2
				continue
			}
			if ch == '"' {
				break // closing quote
			}
			buf.WriteByte(ch)
			i++
		}
		return buf.String()
	}
	// Unquoted value: scan for unescaped #.
	var buf strings.Builder
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if ch == '\\' && i+1 < len(raw) && raw[i+1] == '#' {
			buf.WriteByte('#')
			i++
			continue
		}
		if ch == '#' {
			break // inline comment
		}
		buf.WriteByte(ch)
	}
	return strings.TrimRight(buf.String(), " \t")
}

// Get returns the string value for key, or ErrConfigKeyNotFound.
func (c *Config) Get(key string) (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.values[key]
	if !ok {
		return "", ErrConfigKeyNotFound
	}
	return v, nil
}

// GetStr returns the string value for key, or defaultVal if not found.
func (c *Config) GetStr(key, defaultVal string) string {
	v, err := c.Get(key)
	if err != nil {
		return defaultVal
	}
	return v
}

// GetInt returns the integer value for key, or defaultVal if not found/invalid.
func (c *Config) GetInt(key string, defaultVal int) int {
	v, err := c.Get(key)
	if err != nil {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}

// GetBool returns the boolean value for key, or defaultVal if not found/invalid.
func (c *Config) GetBool(key string, defaultVal bool) bool {
	v, err := c.Get(key)
	if err != nil {
		return defaultVal
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return defaultVal
}

// Set updates a key at runtime (not persisted).
func (c *Config) Set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values[key] = value
}
