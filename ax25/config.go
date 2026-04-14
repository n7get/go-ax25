// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package ax25

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
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
	{Key: "beacon.source", DefaultValue: "", Description: "Beacon source callsign (empty = disabled)"},
	{Key: "beacon.destination", DefaultValue: "BEACON", Description: "Beacon destination callsign"},
	{Key: "beacon.via", DefaultValue: "", Description: "Comma-separated digipeater path"},
	{Key: "beacon.text", DefaultValue: "go-ax25", Description: "Beacon text (supports \\r \\n \\xHH escapes)"},
	{Key: "beacon.every", DefaultValue: "0", Description: "Beacon interval in minutes (0 = disabled)"},
	{Key: "digi.callsign", DefaultValue: "", Description: "Digipeater callsign (empty = disabled)"},
	{Key: "conn.t1_ms", DefaultValue: "10000", Description: "T1 acknowledgement timeout (ms)"},
	{Key: "conn.t2_ms", DefaultValue: "1000", Description: "T2 response delay timeout (ms)"},
	{Key: "conn.t3_ms", DefaultValue: "180000", Description: "T3 inactive link timeout (ms)"},
	{Key: "conn.n2_retries", DefaultValue: "10", Description: "N2 maximum retry count"},
	{Key: "conn.window_size", DefaultValue: "4", Description: "k: maximum outstanding I-frames (1-7)"},
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
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // missing file is not an error
		}
		return fmt.Errorf("ax25: config: open %q: %w", path, err)
	}
	defer f.Close()

	c.mu.Lock()
	defer c.mu.Unlock()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		c.values[key] = val
	}
	return scanner.Err()
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
