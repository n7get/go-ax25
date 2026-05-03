#!/usr/bin/env bash
set -euo pipefail

export GOAX25_DIGI_CALLSIGN=relay
export GOAX25_ROUTER_MODE=switch
export GOAX25_KISS_SERVER_PROMISCUOUS=true
export GOAX25_MONITOR_ENABLED=true

go run . -debug
