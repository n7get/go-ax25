# go-ax25

go-ax25 is a cross-platform AX.25 toolkit comprised of ready-to-run applications and a reusable component library. The component library lets you quickly build new AX.25 applications by composing well-tested building blocks — KISS PHY drivers, a multi-port frame router, connected-mode sessions, digipeating, beaconing, and an AGWPE server.

Derived from the [esp-ax25](https://github.com/n7get/esp-ax25) and [esp-tnc](https://github.com/n7get/esp-tnc) ESP-IDF projects, with one key architectural difference: where [esp-tnc](https://github.com/n7get/esp-tnc) is a monolithic application designed to run on a constrained embedded device, go-ax25 is a suite of composable applications designed to work together on general-purpose hardware.

## Applications (`cmd/`)

### `router` — AX.25 frame router

The router is the central component of go-ax25. Physical (PHY) links and client applications send and receive frames through it. The router runs a KISS TCP server and an AGWPE TCP server simultaneously, forwarding frames between connected clients and an uplink. An uplink can be a serial KISS connection to a TNC or sound card modem, or a KISS TCP connection to another router, a PC running UZ7HO SoundModem, or an [esp-tnc](https://github.com/n7get/esp-tnc) instance.

NOTE: When the uplink connects to an [esp-tnc](https://github.com/n7get/esp-tnc) or another go-ax25 router running in switch mode, that upstream router must be configured in bridge mode. In switch mode, a KISS client port is treated as a dynamic port: it is bound to the source address of the first frame received on that interface, and frames from any other address are dropped. Bridge mode removes this restriction by forwarding all frames across the network/client boundary regardless of address.

```
Uplink (one of):
  serial KISS  — direct serial connection to a TNC or modem
  KISS TCP     — outbound TCP connection to a KISS TCP server

Downlink (always on):
  KISS TCP server  — accepts KISS TCP clients
  AGWPE TCP server — accepts AGWPE clients
```

**Router modes** (set `router.mode` in `ax25.ini`):

In all modes, a frame is never delivered back to the port it arrived on.

| Mode | Behaviour |
|---|---|
| `switch` | Address-based forwarding. Delivers each frame to the port whose static or bound address matches the destination; falls back to default-mode ports if no match. Normal digipeater/gateway mode. |
| `bridge` | Forwards frames between the clients and the default port.  Clients will not receive frames from other clients.
| `hub` | Floods every frame to every port except the source. Useful for test setups, software emulation of a radio network. |

**Port types:**

| Port type | Behaviour |
|---|---|
| `static` | Bound to a fixed address. Receives only frames addressed to that callsign+SSID. |
| `dynamic` | Bound to the source address of the first frame it sends. Subsequent frames from any other address on that interface are dropped. Used automatically for KISS TCP clients in switch mode. |
| `default` | Receives frames when no static or dynamic ports matches the destination. In bridge mode, marks the port as the network/uplink side. |
| `promiscuous` | Receives a copy of every frame regardless of address. 
| `digipeater` | Receives frames that have at least one unrepeated digipeater hop. 

```sh
router [-config ax25.ini] [-debug]
```

---

### `terminal` — connected-mode AX.25 terminal

A command line utility to connect using connected mode to an AX.25 station. Useful a BBS over packet radio.

Terminal can connect to AX.25 using the following methods:

| Flag | Transport |
|---|---|
| `-agwpe` | AGWPE TCP client |
| `-kiss` | KISS TCP client |
| `-serial` | Serial KISS (direct to TNC) |

```sh
# Connect to N7GET-1 via AGWPE router or SoundModem
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

The escape sequence `~.` (on its own line) sends DISC and exits. `Ctrl-C` also disconnects cleanly.

---

### `beacon` — standalone UI beacon

Transmits a periodic UI frame announcing a station's presence. Configure source callsign, destination, digipeater path, text, and interval in `ax25.ini` or via environment variables. Connects to a KISS TCP server.

```sh
beacon [-config ax25.ini] [-debug]
```

---

### `bbs` — packet radio bulletin board system

A lightweight packet BBS that accepts AX.25 connected-mode sessions via an AGWPE server. Supports private messages, bulletins, a heard-station log, and sysop management commands. Messages are stored in a SQLite database.

```sh
bbs [-config ax25.ini] [-debug]
```

BBS commands (subset):

```
H / ?           Help
I               Station info
J               Heard list
L               List messages
R <n>           Read message n
S <call>        Send private message to <call>
SB <call>       Send bulletin to <call>
K <n>           Delete message n
B               Disconnect
```

---

## Configuration

All applications read settings from an INI file (default: `ax25.ini` in the working directory) and accept overrides via environment variables. The resolution order from highest to lowest priority is:

1. Runtime `Set` call (internal defaults)
2. Environment variable
3. INI file value
4. Built-in schema default

**Environment variable naming:** prefix `GOAX25_`, then the config key with `.` replaced by `_` and uppercased.

Examples:

| Config key | Environment variable |
|---|---|
| `beacon.source` | `GOAX25_BEACON_SOURCE` |
| `kiss.server.max_clients` | `GOAX25_KISS_SERVER_MAX_CLIENTS` |

A fully annotated `ax25.ini` with all keys and their defaults is included in the repository root. See [env.md](env.md) for the complete table of every config key, its environment variable name, default value, permissible values, and description.

---

## Building

### Prerequisites

- Go 1.23 or later
- CGO is not required for most packages; `modernc.org/sqlite` (used by `bbs`) is a pure-Go SQLite driver

### UN*X (Linux, macOS, BSD)

Build and install all `cmd/` applications into `$GOPATH/bin`:

```sh
./install-cmd-apps.sh
```

Or build a single application directly:

```sh
go build ./cmd/router
go build ./cmd/terminal
go build ./cmd/bbs
go build ./cmd/beacon
```

### Windows

```cmd
install-cmd-apps.cmd
```

Or build a single application:

```cmd
go build .\cmd\router
go build .\cmd\terminal
go build .\cmd\bbs
go build .\cmd\beacon
```

---

## Packages

### `ax25` — core AX.25 library

Import this package in your own applications.

| File | Contents |
|---|---|
| `types.go` | Constants, enumerations, core structs |
| `address.go` | AX.25 address encode/decode/parse |
| `frame.go` | Frame encode/decode, control-byte helpers |
| `kiss.go` | KISS framing encoder and streaming decoder |
| `router.go` | Multi-port frame router (Switch, Bridge, Hub modes) |
| `phy.go` | PHY interface + `KISSSerialPHY` (`io.ReadWriter` backed) |
| `conn.go` | AX.25 v2.0 connected-mode session (T1/T2/T3, window, retries) |
| `beacon.go` | Periodic UI beacon with escape-sequence text support |
| `digipeater.go` | MAC-layer digipeater (H-bit relay via router port) |
| `config.go` | INI-file backed runtime configuration |

### `agwpe` — AGWPE server and client

Implements the AGW Packet Engine protocol over TCP. Use the server to bridge AX.25 frame traffic to AGWPE-speaking applications (Pat, Dire Wolf, etc.). Use the client to connect your application to an existing AGWPE server.

### `phy` — concrete PHY drivers

| File | Description |
|---|---|
| `kiss_serial.go` | Serial KISS PHY — communicates with a TNC over a serial port |
| `kiss_tcp_client.go` | KISS TCP client PHY — connects outbound to a KISS TCP server |
| `kiss_tcp_server.go` | KISS TCP server — accepts inbound KISS TCP client connections |

### `pcap` — capture file I/O

Read and write libpcap-format (`.pcap`) capture files containing AX.25 or KISS-framed traffic. Compatible with Wireshark and tcpdump.

---

## Getting started (library)

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

---

## Examples

The `examples/` directory contains self-contained, runnable programs that demonstrate common usage patterns:

| Directory | Description |
|---|---|
| `examples/beacon/` | Send a periodic UI beacon via a KISS TCP connection |
| `examples/client/` | Connect to a remote station using an AGWPE client |
| `examples/digi/` | Run a simple digipeater |
| `examples/kiss_router/` | A minimal KISS-only frame router |
| `examples/agwpe_server/` | Stand up an AGWPE server backed by a KISS PHY |
| `examples/monitor/` | Promiscuous frame monitor — prints all received frames |

Each example directory contains a shell script (`*.sh`) that sets up an `ax25.ini` and launches the program. Read the script to see which config keys are relevant.

---

## Test utilities

The `test-utils/` directory contains standalone helper programs used during manual integration testing. They are not part of the library API.

| Directory | Description |
|---|---|
| `test-utils/test-agwpe/` | AGWPE client that sends and receives test frames |
| `test-utils/test-bbs/` | Scripted BBS session exerciser |
| `test-utils/test-bridge/` | Two-PHY bridge exerciser |
| `test-utils/test-hub/` | Hub-mode frame flood tester |

---

## Running tests

```sh
# All packages
go test ./...

# With race detector (recommended before committing)
go test -race -count=1 ./...

# Focused — core protocol only
go test -count=1 ./ax25/... -run 'Test(Address|Frame|Kiss)'
```

---

## License

GNU General Public License v2.0 or later — see [LICENSE](LICENSE).
