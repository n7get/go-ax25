// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/viper"
	ini "gopkg.in/ini.v1"
)

// ---------------------------------------------------------------------------
// Config — viper-backed runtime configuration with INI file + env var support
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

// Config holds the runtime configuration. Values are resolved in priority order:
// explicit Set call > environment variable (GOAX25_* prefix) > INI file > schema default.
type Config struct {
	v         *viper.Viper
	knownKeys map[ConfigKey]bool
	schema    []ConfigParam
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

	KeyAgwpeServerAddr         ConfigKey = "agwpe.server.addr"
	KeyAgwpeServerEnabled      ConfigKey = "agwpe.server.enabled"
	KeyAgwpeServerMaxClients   ConfigKey = "agwpe.server.max_clients"
	KeyAgwpeServerMaxConns     ConfigKey = "agwpe.server.max_conns"
	KeyAgwpeServerReadBuf      ConfigKey = "agwpe.server.read_buf"
	KeyAgwpeServerTxQueueDepth ConfigKey = "agwpe.server.tx_queue_depth"

	KeyBeaconDestination ConfigKey = "beacon.destination"
	KeyBeaconAddr        ConfigKey = "beacon.addr"
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

	KeyKissClientEnabled      ConfigKey = "kiss.client.enabled"
	KeyKissClientHost         ConfigKey = "kiss.client.host"
	KeyKissClientLogFrames    ConfigKey = "kiss.client.log_frames"
	KeyKissClientPort         ConfigKey = "kiss.client.port"
	KeyKissClientReadBuf      ConfigKey = "kiss.client.read_buf"
	KeyKissClientTxQueueDepth ConfigKey = "kiss.client.tx_queue_depth"

	KeyKissSerialEnabled      ConfigKey = "kiss.serial.enabled"
	KeyKissSerialBaud         ConfigKey = "kiss.serial.baud"
	KeyKissSerialDevice       ConfigKey = "kiss.serial.device"
	KeyKissSerialLogFrames    ConfigKey = "kiss.serial.log_frames"
	KeyKissSerialReadBuf      ConfigKey = "kiss.serial.read_buf"
	KeyKissSerialRxQueueDepth ConfigKey = "kiss.serial.rx_queue_depth"
	KeyKissSerialTxQueueDepth ConfigKey = "kiss.serial.tx_queue_depth"

	KeyKissServerAddr         ConfigKey = "kiss.server.addr"
	KeyKissServerEnabled      ConfigKey = "kiss.server.enabled"
	KeyKissServerLogFrames    ConfigKey = "kiss.server.log_frames"
	KeyKissServerMaxClients   ConfigKey = "kiss.server.max_clients"
	KeyKissServerPromiscuous  ConfigKey = "kiss.server.promiscuous"
	KeyKissServerReadBuf      ConfigKey = "kiss.server.read_buf"
	KeyKissServerTxQueueDepth ConfigKey = "kiss.server.tx_queue_depth"

	KeyMonitorEnabled ConfigKey = "monitor.enabled"
	KeyMonitorPrefix  ConfigKey = "monitor.prefix"
	KeyMonitorType    ConfigKey = "monitor.type"

	KeyRouterMode           ConfigKey = "router.mode"
	KeyRouterPortQueueDepth ConfigKey = "router.port_queue_depth"
	KeyTerminalCallsign     ConfigKey = "terminal.callsign"
)

// allConfigKeys lists every core key constant defined in this package.
var allConfigKeys = []ConfigKey{
	KeyAgwpeClientHost, KeyAgwpeClientPort, KeyAgwpeClientReadBuf, KeyAgwpeClientTxQueueDepth,
	KeyAgwpeServerEnabled, KeyAgwpeServerAddr, KeyAgwpeServerMaxClients, KeyAgwpeServerMaxConns, KeyAgwpeServerReadBuf, KeyAgwpeServerTxQueueDepth,
	KeyBeaconDestination, KeyBeaconAddr, KeyBeaconEvery, KeyBeaconSource, KeyBeaconText, KeyBeaconVia,
	KeyConnN2Retries, KeyConnT1Ms, KeyConnT2Ms, KeyConnT3Ms, KeyConnWindowSize,
	KeyDigiCallsign,
	KeyKissClientEnabled, KeyKissClientHost, KeyKissClientLogFrames, KeyKissClientPort, KeyKissClientReadBuf, KeyKissClientTxQueueDepth,
	KeyKissSerialEnabled, KeyKissSerialBaud, KeyKissSerialDevice, KeyKissSerialLogFrames, KeyKissSerialReadBuf, KeyKissSerialRxQueueDepth, KeyKissSerialTxQueueDepth,
	KeyKissServerAddr, KeyKissServerEnabled, KeyKissServerLogFrames, KeyKissServerMaxClients, KeyKissServerPromiscuous, KeyKissServerReadBuf, KeyKissServerTxQueueDepth,
	KeyMonitorEnabled, KeyMonitorPrefix, KeyMonitorType,
	KeyRouterMode, KeyRouterPortQueueDepth,
	KeyTerminalCallsign,
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
	{Key: KeyTerminalCallsign, DefaultValue: "N0CALL", Description: "Terminal local callsign"},

	// Beacon
	{Key: KeyBeaconSource, DefaultValue: "", Description: "Beacon source callsign (empty = disabled)"},
	{Key: KeyBeaconDestination, DefaultValue: "BEACON", Description: "Beacon destination callsign"},
	{Key: KeyBeaconAddr, DefaultValue: "", Description: "Beacon KISS TCP target address host:port (overrides kiss.client host/port)"},
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
	{Key: KeyRouterMode, DefaultValue: "switch", Description: "Router dispatch mode: switch, bridge, or hub"},
	{Key: KeyRouterPortQueueDepth, DefaultValue: "32", Description: "Default per-port frame queue depth"},

	// AGWPE client
	{Key: KeyAgwpeClientHost, DefaultValue: "localhost", Description: "AGWPE client host"},
	{Key: KeyAgwpeClientPort, DefaultValue: "8000", Description: "AGWPE client port"},
	{Key: KeyAgwpeClientReadBuf, DefaultValue: "4132", Description: "AGWPE client rx read buffer size (bytes)"},
	{Key: KeyAgwpeClientTxQueueDepth, DefaultValue: "8", Description: "AGWPE client TX channel depth"},

	// AGWPE server
	{Key: KeyAgwpeServerEnabled, DefaultValue: "true", Description: "Enable AGWPE TCP server"},
	{Key: KeyAgwpeServerAddr, DefaultValue: ":8000", Description: "AGWPE TCP listen address"},
	{Key: KeyAgwpeServerMaxClients, DefaultValue: "16", Description: "AGWPE TCP server max simultaneous clients (0 = unlimited)"},
	{Key: KeyAgwpeServerReadBuf, DefaultValue: "4132", Description: "AGWPE server rx read buffer size (bytes)"},
	{Key: KeyAgwpeServerTxQueueDepth, DefaultValue: "64", Description: "AGWPE server TX channel depth"},
	{Key: KeyAgwpeServerMaxConns, DefaultValue: "4", Description: "AGWPE server max simultaneous AX.25 connections"},

	// KISS serial PHY
	{Key: KeyKissSerialEnabled, DefaultValue: "false", Description: "Enable serial KISS PHY (mutually exclusive with KISS TCP client)"},
	{Key: KeyKissSerialDevice, DefaultValue: "/dev/ttyUSB0", Description: "Serial device for KISS TNC"},
	{Key: KeyKissSerialBaud, DefaultValue: "9600", Description: "Serial baud rate"},
	{Key: KeyKissSerialLogFrames, DefaultValue: "false", Description: "Log KISS boundary frames for serial KISS PHY when monitor.type=kiss"},
	{Key: KeyKissSerialReadBuf, DefaultValue: "1024", Description: "KISS serial rx read buffer size (bytes)"},
	{Key: KeyKissSerialRxQueueDepth, DefaultValue: "64", Description: "KISS serial rx frame queue depth"},
	{Key: KeyKissSerialTxQueueDepth, DefaultValue: "32", Description: "KISS serial tx frame queue depth"},

	// KISS TCP client PHY
	{Key: KeyKissClientEnabled, DefaultValue: "false", Description: "Enable KISS TCP client PHY (mutually exclusive with serial PHY)"},
	{Key: KeyKissClientHost, DefaultValue: "localhost", Description: "KISS TCP client host"},
	{Key: KeyKissClientLogFrames, DefaultValue: "false", Description: "Log KISS boundary frames for KISS TCP client PHY when monitor.type=kiss"},
	{Key: KeyKissClientPort, DefaultValue: "8100", Description: "KISS TCP client port"},
	{Key: KeyKissClientReadBuf, DefaultValue: "4096", Description: "KISS TCP client rx read buffer size (bytes)"},
	{Key: KeyKissClientTxQueueDepth, DefaultValue: "8", Description: "KISS TCP client TX channel depth"},

	// KISS TCP server PHY
	{Key: KeyKissServerAddr, DefaultValue: ":8100", Description: "KISS TCP server listen address"},
	{Key: KeyKissServerEnabled, DefaultValue: "true", Description: "Enable KISS TCP server"},
	{Key: KeyKissServerLogFrames, DefaultValue: "false", Description: "Log KISS boundary frames for KISS TCP server client PHYs when monitor.type=kiss"},
	{Key: KeyKissServerMaxClients, DefaultValue: "16", Description: "KISS TCP server max simultaneous clients (0 = unlimited)"},
	{Key: KeyKissServerPromiscuous, DefaultValue: "false", Description: "Register KISS TCP server client ports as promiscuous (unsupported in bridge mode)"},
	{Key: KeyKissServerReadBuf, DefaultValue: "4096", Description: "KISS TCP server rx read buffer size per client (bytes)"},
	{Key: KeyKissServerTxQueueDepth, DefaultValue: "8", Description: "KISS TCP server TX channel depth per client"},

	// Monitor
	{Key: KeyMonitorEnabled, DefaultValue: "false", Description: "Enable router capture logging to pcap"},
	{Key: KeyMonitorPrefix, DefaultValue: "monitor", Description: "Capture file prefix; files are written as <prefix>-yymmddvv.pcap"},
	{Key: KeyMonitorType, DefaultValue: "ax25", Description: "Capture data type: ax25 or kiss"},
}

// NewConfig creates a Config with the given schema (merged with DefaultSchema).
// Environment variables with the GOAX25_ prefix override config file and default values.
// Dots in config keys are replaced with underscores in env var names, e.g.
// beacon.source → GOAX25_BEACON_SOURCE.
func NewConfig(extra []ConfigParam) *Config {
	codecReg := viper.NewCodecRegistry()
	_ = codecReg.RegisterCodec("ini", iniCodec{})
	v := viper.NewWithOptions(viper.WithCodecRegistry(codecReg))
	v.SetEnvPrefix("GOAX25")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	schema := make([]ConfigParam, 0, len(DefaultSchema)+len(extra))
	schema = append(schema, DefaultSchema...)
	schema = append(schema, extra...)

	knownKeys := make(map[ConfigKey]bool, len(schema))
	for _, p := range schema {
		v.SetDefault(string(p.Key), p.DefaultValue)
		knownKeys[p.Key] = true
	}
	return &Config{v: v, knownKeys: knownKeys, schema: schema}
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

	c.v.SetConfigFile(path)
	c.v.SetConfigType("ini")
	if err := c.v.ReadInConfig(); err != nil {
		return fmt.Errorf("ax25: config: parse %q: %w", path, err)
	}
	return nil
}

// iniCodec is a viper Codec that reads INI files using gopkg.in/ini.v1.
// It is registered as the "ini" decoder when each Config is created.
type iniCodec struct{}

func (iniCodec) Decode(b []byte, m map[string]any) error {
	f, err := ini.LoadSources(ini.LoadOptions{
		SkipUnrecognizableLines: true,
	}, b)
	if err != nil {
		return err
	}
	for _, section := range f.Sections() {
		sectionName := section.Name()
		for _, key := range section.Keys() {
			var fullKey string
			if sectionName == ini.DefaultSection {
				fullKey = key.Name()
			} else {
				fullKey = sectionName + "." + key.Name()
			}
			iniDeepSet(m, strings.Split(fullKey, "."), key.Value())
		}
	}
	return nil
}

func (iniCodec) Encode(v map[string]any) ([]byte, error) {
	return nil, errors.New("ax25: ini encoding not supported")
}

// iniDeepSet stores value at the nested path within m.
func iniDeepSet(m map[string]any, path []string, value string) {
	if len(path) == 1 {
		m[path[0]] = value
		return
	}
	next, ok := m[path[0]].(map[string]any)
	if !ok {
		next = make(map[string]any)
		m[path[0]] = next
	}
	iniDeepSet(next, path[1:], value)
}

// Get returns the string value for key, panicking if the key is not in the schema.
func (c *Config) Get(key ConfigKey) string {
	if !c.knownKeys[key] {
		panic(fmt.Sprintf("ax25: config: missing key %q", key))
	}
	return c.v.GetString(string(key))
}

// GetStr returns the string value for key, panicking if the key is not in the schema.
func (c *Config) GetStr(key ConfigKey) string {
	return c.Get(key)
}

// GetInt returns the integer value for key, panicking if the key is not in the schema
// or if the value cannot be converted to int.
func (c *Config) GetInt(key ConfigKey) int {
	v := c.Get(key)
	n, err := strconv.Atoi(v)
	if err != nil {
		panic(fmt.Sprintf("ax25: config: invalid int for key %q: %q", key, v))
	}
	return n
}

// GetBool returns the boolean value for key using strconv.ParseBool semantics
// (accepts 1/0/t/f/T/F/TRUE/FALSE/true/false/True/False).
// It panics if the key is not in the schema or if the value is not a valid bool.
func (c *Config) GetBool(key ConfigKey) bool {
	v := c.Get(key)
	b, err := strconv.ParseBool(v)
	if err != nil {
		panic(fmt.Sprintf("ax25: config: invalid bool for key %q: %q", key, v))
	}
	return b
}

// Set overrides a key at runtime (not persisted). Takes effect immediately
// at the highest viper priority, overriding env vars and config file values.
func (c *Config) Set(key ConfigKey, value string) {
	c.v.Set(string(key), value)
}
