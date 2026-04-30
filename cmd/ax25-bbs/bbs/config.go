// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package bbs

import (
	"github.com/n7get/go-ax25/ax25"
)

// BBS configuration key constants owned by the bbs package.
const (
	KeyBbsCallsign               ax25.ConfigKey = "bbs.callsign"
	KeyBbsGreeting               ax25.ConfigKey = "bbs.greeting"
	KeyBbsPrompt                 ax25.ConfigKey = "bbs.prompt"
	KeyBbsSysopName              ax25.ConfigKey = "bbs.sysop_name"
	KeyBbsVersion                ax25.ConfigKey = "bbs.version"
	KeyBbsMaxMessages            ax25.ConfigKey = "bbs.max_messages"
	KeyBbsMaxBodyLen             ax25.ConfigKey = "bbs.max_body_len"
	KeyBbsSysopSecret            ax25.ConfigKey = "bbs.sysop_secret"
	KeyBbsSysopChallengeTimeoutS ax25.ConfigKey = "bbs.sysop_challenge_timeout_s"
	KeyBbsSysopSessionTimeoutS   ax25.ConfigKey = "bbs.sysop_session_timeout_s"
	KeyBbsSysopLockoutS          ax25.ConfigKey = "bbs.sysop_lockout_s"
	KeyBbsSysopMaxAttempts       ax25.ConfigKey = "bbs.sysop_max_attempts"
	KeyBbsDbPath                 ax25.ConfigKey = "bbs.db_path"
	KeyBbsHost                   ax25.ConfigKey = "bbs.host"
	KeyBbsPort                   ax25.ConfigKey = "bbs.port"
)

var allBBSConfigKeys = []ax25.ConfigKey{
	KeyBbsCallsign,
	KeyBbsGreeting,
	KeyBbsPrompt,
	KeyBbsSysopName,
	KeyBbsVersion,
	KeyBbsMaxMessages,
	KeyBbsMaxBodyLen,
	KeyBbsSysopSecret,
	KeyBbsSysopChallengeTimeoutS,
	KeyBbsSysopSessionTimeoutS,
	KeyBbsSysopLockoutS,
	KeyBbsSysopMaxAttempts,
	KeyBbsDbPath,
	KeyBbsHost,
	KeyBbsPort,
}

// BBSConfigSchema defines the BBS-specific configuration parameters.
var BBSConfigSchema = []ax25.ConfigParam{
	{Key: KeyBbsCallsign, DefaultValue: "N0CALL-2", Description: "BBS station callsign"},
	{Key: KeyBbsGreeting, DefaultValue: "Welcome to Go AX.25 BBS", Description: "Banner text on connect"},
	{Key: KeyBbsPrompt, DefaultValue: "BBS> ", Description: "Command prompt"},
	{Key: KeyBbsSysopName, DefaultValue: "SYSOP", Description: "Sysop display name"},
	{Key: KeyBbsVersion, DefaultValue: "go-ax25-bbs 0.1", Description: "Version string"},
	{Key: KeyBbsMaxMessages, DefaultValue: "500", Description: "Max stored messages"},
	{Key: KeyBbsMaxBodyLen, DefaultValue: "102400", Description: "Max message body size in bytes"},
	{Key: KeyBbsSysopSecret, DefaultValue: "", Description: "SYSOP challenge-response secret"},
	{Key: KeyBbsSysopChallengeTimeoutS, DefaultValue: "300", Description: "SYSOP challenge timeout seconds"},
	{Key: KeyBbsSysopSessionTimeoutS, DefaultValue: "600", Description: "SYSOP session idle timeout seconds"},
	{Key: KeyBbsSysopLockoutS, DefaultValue: "900", Description: "SYSOP lockout duration seconds"},
	{Key: KeyBbsSysopMaxAttempts, DefaultValue: "3", Description: "SYSOP max failed attempts before lockout"},
	{Key: KeyBbsDbPath, DefaultValue: "bbs.db", Description: "SQLite database path"},
	{Key: KeyBbsHost, DefaultValue: "localhost", Description: "AGWPE server host"},
	{Key: KeyBbsPort, DefaultValue: "8000", Description: "AGWPE server port"},
}

// BBSConfig holds the resolved BBS configuration values.
type BBSConfig struct {
	Callsign              string
	Greeting              string
	Prompt                string
	SysopName             string
	Version               string
	MaxMessages           int
	MaxBodyLen            int
	SysopSecret           string
	SysopChallengeTimeout int // seconds
	SysopSessionTimeout   int // seconds
	SysopLockout          int // seconds
	SysopMaxAttempts      int
	DBPath                string
	AGWPEHost             string
	AGWPEPort             uint16
}

// LoadBBSConfig reads BBS settings from an ax25.Config.
func LoadBBSConfig(cfg *ax25.Config) BBSConfig {
	return BBSConfig{
		Callsign:              cfg.GetStr(KeyBbsCallsign),
		Greeting:              cfg.GetStr(KeyBbsGreeting),
		Prompt:                cfg.GetStr(KeyBbsPrompt),
		SysopName:             cfg.GetStr(KeyBbsSysopName),
		Version:               cfg.GetStr(KeyBbsVersion),
		MaxMessages:           cfg.GetInt(KeyBbsMaxMessages),
		MaxBodyLen:            cfg.GetInt(KeyBbsMaxBodyLen),
		SysopSecret:           cfg.GetStr(KeyBbsSysopSecret),
		SysopChallengeTimeout: cfg.GetInt(KeyBbsSysopChallengeTimeoutS),
		SysopSessionTimeout:   cfg.GetInt(KeyBbsSysopSessionTimeoutS),
		SysopLockout:          cfg.GetInt(KeyBbsSysopLockoutS),
		SysopMaxAttempts:      cfg.GetInt(KeyBbsSysopMaxAttempts),
		DBPath:                cfg.GetStr(KeyBbsDbPath),
		AGWPEHost:             cfg.GetStr(KeyBbsHost),
		AGWPEPort:             uint16(cfg.GetInt(KeyBbsPort)),
	}
}
