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

// ConfigKey is a type-safe configuration key.
// Getters and setters require ConfigKey, so raw string literals cause a compile error.
type ConfigKey string

// ConfigParam describes a single configuration parameter.
type ConfigParam struct {
	Key          ConfigKey // dotted key, e.g. "beacon.source"
	DefaultValue string
	Description  string
}

// Config holds the runtime configuration loaded from an INI file.
type Config struct {
	mu     sync.RWMutex
	values map[ConfigKey]string
	schema []ConfigParam
}

var (
	ErrConfigKeyNotFound = errors.New("ax25: config key not found")
	ErrConfigNotInit     = errors.New("ax25: config not initialised")
)

// Core configuration key constants for type-safe access to Config values.
const (
	KeyAgwpeClientHost         ConfigKey = "agwpe.client.host"
	KeyAgwpeClientPort         ConfigKey = "agwpe.client.port"
	KeyAgwpeClientReadBuf      ConfigKey = "agwpe.client.read_buf"
	KeyAgwpeClientTxQueueDepth ConfigKey = "agwpe.client.tx_queue_depth"

	KeyAgwpeServerEnabled      ConfigKey = "agwpe.server.enabled"
	KeyAgwpeServerMaxConns     ConfigKey = "agwpe.server.max_conns"
	KeyAgwpeServerPort         ConfigKey = "agwpe.server.port"
	KeyAgwpeServerReadBuf      ConfigKey = "agwpe.server.read_buf"
	KeyAgwpeServerTxQueueDepth ConfigKey = "agwpe.server.tx_queue_depth"

	KeyBeaconDestination ConfigKey = "beacon.destination"
	KeyBeaconEvery       ConfigKey = "beacon.every"
	KeyBeaconSource      ConfigKey = "beacon.source"
	KeyBeaconText        ConfigKey = "beacon.text"
	KeyBeaconVia         ConfigKey = "beacon.via"

	KeyConnN2Retries  ConfigKey = "conn.n2_retries"
	KeyConnT1Ms       ConfigKey = "conn.t1_ms"
	KeyConnT2Ms       ConfigKey = "conn.t2_ms"
	KeyConnT3Ms       ConfigKey = "conn.t3_ms"
	KeyConnWindowSize ConfigKey = "conn.window_size"

	KeyDigiCallsign ConfigKey = "digi.callsign"

	KeyKissClientHost         ConfigKey = "kiss.client.host"
	KeyKissClientPort         ConfigKey = "kiss.client.port"
	KeyKissClientReadBuf      ConfigKey = "kiss.client.read_buf"
	KeyKissClientTxQueueDepth ConfigKey = "kiss.client.tx_queue_depth"

	KeyKissSerialBaud         ConfigKey = "kiss.serial.baud"
	KeyKissSerialDevice       ConfigKey = "kiss.serial.device"
	KeyKissSerialReadBuf      ConfigKey = "kiss.serial.read_buf"
	KeyKissSerialRxQueueDepth ConfigKey = "kiss.serial.rx_queue_depth"
	KeyKissSerialTxQueueDepth ConfigKey = "kiss.serial.tx_queue_depth"

	KeyKissServerAddr         ConfigKey = "kiss.server.addr"
	KeyKissServerEnabled      ConfigKey = "kiss.server.enabled"
	KeyKissServerMaxClients   ConfigKey = "kiss.server.max_clients"
	KeyKissServerPort         ConfigKey = "kiss.server.port"
	KeyKissServerReadBuf      ConfigKey = "kiss.server.read_buf"
	KeyKissServerTxQueueDepth ConfigKey = "kiss.server.tx_queue_depth"

	KeyRouterPortQueueDepth ConfigKey = "router.port_queue_depth"
	KeyStationCallsign      ConfigKey = "station.callsign"
	KeyStationSsid          ConfigKey = "station.ssid"
)

// allConfigKeys lists every core key constant defined in this package.
var allConfigKeys = []ConfigKey{
	KeyAgwpeClientHost, KeyAgwpeClientPort, KeyAgwpeClientReadBuf, KeyAgwpeClientTxQueueDepth,
	KeyAgwpeServerEnabled, KeyAgwpeServerMaxConns, KeyAgwpeServerPort, KeyAgwpeServerReadBuf, KeyAgwpeServerTxQueueDepth,
	KeyBeaconDestination, KeyBeaconEvery, KeyBeaconSource, KeyBeaconText, KeyBeaconVia,
	KeyConnN2Retries, KeyConnT1Ms, KeyConnT2Ms, KeyConnT3Ms, KeyConnWindowSize,
	KeyDigiCallsign,
	KeyKissClientHost, KeyKissClientPort, KeyKissClientReadBuf, KeyKissClientTxQueueDepth,
	KeyKissSerialBaud, KeyKissSerialDevice, KeyKissSerialReadBuf, KeyKissSerialRxQueueDepth, KeyKissSerialTxQueueDepth,
	KeyKissServerAddr, KeyKissServerEnabled, KeyKissServerMaxClients, KeyKissServerPort, KeyKissServerReadBuf, KeyKissServerTxQueueDepth,
	KeyRouterPortQueueDepth,
	KeyStationCallsign, KeyStationSsid,
}

// AllSchemaKeys returns the set of all valid core configuration keys.
func AllSchemaKeys() map[ConfigKey]bool {
	m := make(map[ConfigKey]bool, len(allConfigKeys))
	for _, k := range allConfigKeys {
		m[k] = true
	}
	return m
}

// DefaultSchema is the built-in parameter schema.
var DefaultSchema = []ConfigParam{
	// Station identity
	{Key: KeyStationCallsign, DefaultValue: "N0CALL", Description: "Station callsign"},
	{Key: KeyStationSsid, DefaultValue: "0", Description: "Station SSID (0-15)"},

	// Beacon
	{Key: KeyBeaconSource, DefaultValue: "", Description: "Beacon source callsign (empty = disabled)"},
	{Key: KeyBeaconDestination, DefaultValue: "BEACON", Description: "Beacon destination callsign"},
	{Key: KeyBeaconVia, DefaultValue: "", Description: "Comma-separated digipeater path"},
	{Key: KeyBeaconText, DefaultValue: "go-ax25", Description: "Beacon text (supports \\r \\n \\xHH escapes)"},
	{Key: KeyBeaconEvery, DefaultValue: "0", Description: "Beacon interval in minutes (0 = disabled)"},

	// Digipeater
	{Key: KeyDigiCallsign, DefaultValue: "", Description: "Digipeater callsign (empty = disabled)"},

	// AX.25 connected-mode timers
	{Key: KeyConnT1Ms, DefaultValue: "10000", Description: "T1 acknowledgement timeout (ms)"},
	{Key: KeyConnT2Ms, DefaultValue: "1000", Description: "T2 response delay timeout (ms)"},
	{Key: KeyConnT3Ms, DefaultValue: "180000", Description: "T3 inactive link timeout (ms)"},
	{Key: KeyConnN2Retries, DefaultValue: "10", Description: "N2 maximum retry count"},
	{Key: KeyConnWindowSize, DefaultValue: "4", Description: "k: maximum outstanding I-frames (1-7)"},

	// Router
	{Key: KeyRouterPortQueueDepth, DefaultValue: "32", Description: "Default per-port frame queue depth"},

	// AGWPE client
	{Key: KeyAgwpeClientHost, DefaultValue: "localhost", Description: "AGWPE client host"},
	{Key: KeyAgwpeClientPort, DefaultValue: "8000", Description: "AGWPE client port"},
	{Key: KeyAgwpeClientReadBuf, DefaultValue: "4132", Description: "AGWPE client rx read buffer size (bytes)"},
	{Key: KeyAgwpeClientTxQueueDepth, DefaultValue: "8", Description: "AGWPE client TX channel depth"},

	// AGWPE server
	{Key: KeyAgwpeServerEnabled, DefaultValue: "true", Description: "Enable AGWPE TCP server"},
	{Key: KeyAgwpeServerPort, DefaultValue: "8000", Description: "AGWPE TCP listen port"},
	{Key: KeyAgwpeServerReadBuf, DefaultValue: "4132", Description: "AGWPE server rx read buffer size (bytes)"},
	{Key: KeyAgwpeServerTxQueueDepth, DefaultValue: "64", Description: "AGWPE server TX channel depth"},
	{Key: KeyAgwpeServerMaxConns, DefaultValue: "4", Description: "AGWPE server max simultaneous AX.25 connections"},

	// KISS serial PHY
	{Key: KeyKissSerialDevice, DefaultValue: "/dev/ttyUSB0", Description: "Serial device for KISS TNC"},
	{Key: KeyKissSerialBaud, DefaultValue: "9600", Description: "Serial baud rate"},
	{Key: KeyKissSerialReadBuf, DefaultValue: "1024", Description: "KISS serial rx read buffer size (bytes)"},
	{Key: KeyKissSerialRxQueueDepth, DefaultValue: "64", Description: "KISS serial rx frame queue depth"},
	{Key: KeyKissSerialTxQueueDepth, DefaultValue: "32", Description: "KISS serial tx frame queue depth"},

	// KISS TCP client PHY
	{Key: KeyKissClientHost, DefaultValue: "localhost", Description: "KISS TCP client host"},
	{Key: KeyKissClientPort, DefaultValue: "8001", Description: "KISS TCP client port"},
	{Key: KeyKissClientReadBuf, DefaultValue: "4096", Description: "KISS TCP client rx read buffer size (bytes)"},
	{Key: KeyKissClientTxQueueDepth, DefaultValue: "8", Description: "KISS TCP client TX channel depth"},

	// KISS TCP server PHY
	{Key: KeyKissServerPort, DefaultValue: "8100", Description: "KISS TCP listen port (integer)"},
	{Key: KeyKissServerAddr, DefaultValue: ":8100", Description: "KISS TCP server listen address"},
	{Key: KeyKissServerEnabled, DefaultValue: "true", Description: "Enable KISS TCP server"},
	{Key: KeyKissServerMaxClients, DefaultValue: "8", Description: "KISS TCP server max simultaneous clients"},
	{Key: KeyKissServerReadBuf, DefaultValue: "4096", Description: "KISS TCP server rx read buffer size per client (bytes)"},
	{Key: KeyKissServerTxQueueDepth, DefaultValue: "8", Description: "KISS TCP server TX channel depth per client"},
}

// NewConfig creates a Config with the given schema (merged with DefaultSchema).
func NewConfig(extra []ConfigParam) *Config {
	c := &Config{
		values: make(map[ConfigKey]string),
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
			cfgKeyStr := key.Name()
			if sectionName != ini.DefaultSection {
				cfgKeyStr = sectionName + "." + cfgKeyStr
			}
			// Keep compatibility with previous parser behavior for inline comments,
			// escaping, and quoted strings.
			c.values[ConfigKey(cfgKeyStr)] = parseINIValue(strings.TrimSpace(key.Value()))
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

// Get returns the string value for key.
// It panics if the key is not present.
func (c *Config) Get(key ConfigKey) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.values[key]
	if !ok {
		panic(fmt.Sprintf("ax25: config: missing key %q", key))
	}
	return v
}

// GetStr returns the string value for key.
// It panics if the key is not present.
func (c *Config) GetStr(key ConfigKey) string {
	return c.Get(key)
}

// GetInt returns the integer value for key.
// It panics if the key is not present or if conversion fails.
func (c *Config) GetInt(key ConfigKey) int {
	v := c.Get(key)
	n, err := strconv.Atoi(v)
	if err != nil {
		panic(fmt.Sprintf("ax25: config: invalid int for key %q: %q", key, v))
	}
	return n
}

// GetBool returns the boolean value for key.
// It panics if the key is not present or if conversion fails.
func (c *Config) GetBool(key ConfigKey) bool {
	v := c.Get(key)
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	panic(fmt.Sprintf("ax25: config: invalid bool for key %q: %q", key, v))
}

// Set updates a key at runtime (not persisted).
func (c *Config) Set(key ConfigKey, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values[key] = value
}
