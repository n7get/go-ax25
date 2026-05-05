#!/usr/bin/env bash
set -euo pipefail

export GOAX25_MONITOR_ENABLED=true

# General testing + RELAY
#export GOAX25_DIGI_CALLSIGN=relay
#export GOAX25_KISS_SERVER_PROMISCUOUS=true

# Testing Pat <-> router <-> SoundModem on PC
export GOAX25_KISS_CLIENT_ENABLED=true
export GOAX25_KISS_CLIENT_HOST=192.168.68.11

go run . -debug 2>&1 | tee router.log | cut -f3- -d' '
