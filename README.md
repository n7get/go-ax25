# go-ax25

A pure-Go implementation of the AX.25 amateur radio packet protocol,
translated from the [esp-ax25](https://github.com/n7get/esp-ax25) ESP-IDF C component.

## Package layout

```
ax25/
  types.go        — constants, enumerations, core structs
  address.go      — AX.25 address encode/decode/parse
  frame.go        — frame encode/decode, control byte helpers
  kiss.go         — KISS framing encoder and streaming decoder
  router.go       — multi-port frame router (static, default, promiscuous, digipeater)
  phy.go          — PHY interface + KISSSerialPHY (io.ReadWriter backed)
  conn.go         — AX.25 v2.0 connected-mode session (T1/T2/T3, window, retries)
  beacon.go       — periodic UI beacon with escape-sequence text support
  digipeater.go   — MAC-layer digipeater (H-bit relay via router port)
  config.go       — INI-file backed runtime configuration
  *_test.go       — unit + fuzz tests for every component
```

## Quick start

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

## Running tests

```sh
go test ./ax25/...
```

## License

GNU General Public License v2.0 or later — see [LICENSE](LICENSE).
