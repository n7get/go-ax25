# test-utils

Manual integration test utilities for go-ax25. Each utility is a self-contained
Go program that can be run directly with `go run main.go`.

---

## test-agwpe

Exercises the AGWPE client/server protocol against a **running external AGWPE
server** (e.g. ax25-router). Tests version query, port info, port capabilities,
heard-stations list, outstanding-frames queries, callsign registration, connect,
data exchange, and disconnect.

**Requires:** an external AGWPE server and (optionally) a remote AX.25 station.

```
go run main.go -local N7GET-9 -remote W7SCS-1 -agwpe 127.0.0.1:8000
```

| Flag | Default | Description |
|------|---------|-------------|
| `-agwpe` | `localhost:8000` | AGWPE server host:port |
| `-local` | — | Local callsign (required) |
| `-remote` | — | Remote callsign for connection tests (optional) |
| `-port` | `0` | AGWPE radio port number |
| `-heard` | false | Enable heard-stations test (skipped by default) |
| `-login-user` | — | AGWPE login username (empty = skip login) |
| `-login-pass` | — | AGWPE login password |
| `-connect-timeout` | 15000 | Connection timeout in ms |
| `-step-timeout` | 5000 | Per-test timeout in ms |
| `-debug` | false | Enable debug logging |

---

## test-bbs

Runs a scripted BBS command test suite against a **running external BBS** (via
AGWPE). Connects to the BBS, exercises standard user commands (J, L, R, S, B,
…), and optionally the sysop CONFIG path. Prints PASS/FAIL for each command.

**Requires:** a running ax25-router + ax25-bbs (or equivalent).

```
go run main.go -local N7GET-9 -remote N7GET-2 -agwpe 127.0.0.1:8000
```

| Flag | Default | Description |
|------|---------|-------------|
| `-agwpe` | `localhost:8000` | AGWPE server host:port |
| `-local` | — | Local callsign (required) |
| `-remote` | — | BBS callsign (required) |
| `-sysop-secret` | — | Sysop password for CONFIG auth test (empty = skip) |
| `-connect-timeout` | 15000 | Connection timeout in ms |
| `-step-timeout` | 10000 | Per-command timeout in ms |
| `-debug` | false | Enable debug logging |

---

## test-hub

Self-contained integration test for **Hub-mode routing**. Spins up an in-process
Hub router, an embedded BBS, a single AGWPE TCP server, and a test AGWPE client
— no external processes required. Optionally relays through a fixed `RELAY`
digipeater.

**Topology:**

```
test-client ──AGWPE──► hub-router ──AGWPE──► embedded-BBS
                            │
                        (RELAY digi)
```

```
go run main.go -local N7GET-9 -remote N7GET-2
go run main.go -local N7GET-9 -remote N7GET-2 -with-digi
```

| Flag | Default | Description |
|------|---------|-------------|
| `-agwpe` | `127.0.0.1:18000` | AGWPE listen address |
| `-local` | `N7GET-9` | Test client callsign |
| `-remote` | `N7GET-2` | BBS callsign |
| `-with-digi` | false | Use `RELAY` as AX.25 digipeater path |
| `-connect-timeout` | 15000 | Connection timeout in ms |
| `-step-timeout` | 10000 | Step timeout in ms |
| `-trace` | false | Print per-hop forwarding counters at end |
| `-debug` | false | Enable debug logging |

---

## test-bridge

Self-contained integration test for **Bridge-mode routing**. Wires three
in-process routers (left Bridge → Hub → right Bridge) with two AGWPE TCP
servers. The embedded BBS attaches to the left server; the test client attaches
to the right server. Optionally relays through a `RELAY` digipeater on the Hub.

**Topology:**

```
test-client ──AGWPE──► right-bridge ──► hub ──► left-bridge ──AGWPE──► embedded-BBS
                                         │
                                     (RELAY digi)
```

```
go run main.go -local N7GET-9 -remote N7GET-2
go run main.go -local N7GET-9 -remote N7GET-2 -with-digi
```

| Flag | Default | Description |
|------|---------|-------------|
| `-left-agwpe` | `127.0.0.1:18100` | AGWPE listen address (BBS side) |
| `-right-agwpe` | `127.0.0.1:18101` | AGWPE listen address (client side) |
| `-local` | `N7GET-9` | Test client callsign |
| `-remote` | `N7GET-2` | BBS callsign |
| `-with-digi` | false | Use `RELAY` as AX.25 digipeater path |
| `-connect-timeout` | 15000 | Connection timeout in ms |
| `-step-timeout` | 10000 | Step timeout in ms |
| `-trace` | false | Print per-hop forwarding counters at end |
| `-debug` | false | Enable debug logging |
