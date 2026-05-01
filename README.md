# go-ax25

A pure-Go implementation of the AX.25 amateur radio packet protocol stack, including KISS physical-layer drivers, a multi-port frame router, connected-mode sessions, digipeating, beaconing, and an AGWPE server. Originally translated from the [esp-ax25](https://github.com/n7get/esp-ax25) ESP-IDF C component.

## Packages

```
ax25/           — core AX.25 library (import this in your own apps)
  types.go        — constants, enumerations, core structs
  address.go      — AX.25 address encode/decode/parse
  frame.go        — frame encode/decode, control byte helpers
  kiss.go         — KISS framing encoder and streaming decoder
  router.go       — multi-port frame router (Switch, Bridge, Hub modes)
  phy.go          — PHY interface + KISSSerialPHY (io.ReadWriter backed)
  conn.go         — AX.25 v2.0 connected-mode session (T1/T2/T3, window, retries)
  beacon.go       — periodic UI beacon with escape-sequence text support
  digipeater.go   — MAC-layer digipeater (H-bit relay via router port)
  config.go       — INI-file backed runtime configuration
  *_test.go       — unit + fuzz tests for every component

agwpe/          — AGW Packet Engine (AGWPE) server and client over TCP

phy/            — concrete PHY drivers
  kiss_serial.go      — serial KISS PHY
  kiss_tcp_client.go  — KISS TCP client PHY
  kiss_tcp_server.go  — KISS TCP server (accepts TNC clients)
```

## Applications (`cmd/`)

### `router` — AX.25 frame router

Bridges a radio-side PHY to one or more software clients. Runs a KISS TCP server and an AGWPE TCP server simultaneously, forwarding frames between all connected clients and the uplink PHY.

```
Uplink (one of):
  serial KISS  — direct serial connection to a TNC or modem
  KISS TCP     — outbound TCP connection to a KISS TCP server

Downlink (always on):
  KISS TCP server  — accepts KISS TCP clients
  AGWPE TCP server — accepts AGWPE clients
```

**Router modes** (set `router.mode` in `ax25.ini`):

| Mode | Behaviour |
|---|---|
| `switch` | Address-based forwarding. Delivers each frame to the port whose static address matches the destination; falls back to default-mode ports if no match. This is the normal digipeater/gateway mode. |
| `bridge` | Two-sided partition. Ports marked `default` form the network/uplink side; all other ports form the client side. Frames cross between sides but not within the same side. |
| `hub` | Flood every frame to every port except the source. Useful for monitoring or test setups. |

Usage:

```sh
router [-config ax25.ini] [-debug]
```

Configure the uplink, listening ports, and router mode in `ax25.ini`.

---

### `terminal` — connected-mode AX.25 terminal

Binds stdin/stdout to a single AX.25 connected-mode session. Useful for chatting with another station or a BBS over packet radio.

Supports three transport interfaces (select via flag or `ax25.ini`):

| Flag | Transport |
|---|---|
| `-agwpe` | AGWPE TCP client |
| `-kiss` | KISS TCP client |
| `-serial` | Serial KISS (direct to TNC) |

```sh
# Connect to N7GET-1 via AGWPE router
terminal -agwpe N7GET-1

# Connect via serial TNC with a digipeater path
terminal -serial N7GET-1 WB7TNC-3

# Passive listen (incoming connections only; AGWPE and serial)
terminal -agwpe
```

Additional flags:

```
-local <call>      local callsign (overrides ax25.ini terminal.callsign)
-server <host>     override server host from ax25.ini
-port <n>          override server port from ax25.ini
-device <path>     override serial device from ax25.ini (serial mode only)
-interfaces        list enabled transports and exit
-debug             enable debug logging
-config <path>     path to ax25.ini (default: ax25.ini)
```

Escape sequence `~.` (on its own line) sends DISC and exits. `Ctrl-C` also disconnects cleanly.

---

### `bbs` — packet radio bulletin board system

A lightweight packet BBS that accepts AX.25 connected-mode sessions via an AGWPE server. Supports private messages, bulletins, a heard-station log, and sysop management commands.

```sh
bbs [-config ax25.ini] [-debug]
```

BBS commands (subset):

```
H / ?       Help
I           Station info
J           Heard list
L           List messages
R <n>       Read message n
S <call>    Send private message to <call>
SB <call>   Send bulletin to <call>
K <n>       Delete message n
B           Disconnect
```

Messages are stored in a SQLite database. Configure the BBS callsign, AGWPE connection, and database path in `ax25.ini`.

---

## Quick start (library)

```go
import "github.com/n7get/go-ax25/ax25"

// 1. Create a router.
r := ax25.NewRouter()

// 2. Attach a KISS serial PHY (e.g. /dev/ttyUSB0 opened as io.ReadWriter).
phy := ax25.NewKISSSerialPHY(serialPort, ax25.KISSSerialPHYConfig{})
phy.Start(ctx)

// 3. Bridge PHY → router.
go func() {
    for f := range phy.RxFrames() {
        r.Send(f, nil)
    }
}()

// 4. Register a receive port.
myAddr, _ := ax25.ParseAddress("N7GET-1")
port := &ax25.Port{
    Mode:        ax25.PortModeStatic,
    Destination: myAddr,
    OnRxFrame:   func(f *ax25.Frame) { fmt.Println("rx:", f.Source) },
}
r.RegisterPort(port)

// 5. Send a UI frame.
dst, _ := ax25.ParseAddress("APRS")
r.Send(&ax25.Frame{
    Destination: dst,
    Source:      myAddr,
    IsCommand:   true,
    Type:        ax25.FrameUI,
    Control:     ax25.CtrlUI,
    PID:         ax25.PIDNone,
    Payload:     []byte(">Hello from go-ax25"),
}, port)
```

See `examples/` for more complete working programs.

## Running tests

```sh
go test ./...
```

## License

GNU General Public License v2.0 or later — see [LICENSE](LICENSE).
