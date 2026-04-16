package main

import (
	"bytes"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"

	"github.com/n7get/go-ax25/ax25"
)

func main() {
	var (
		server    = flag.String("server", "", "KISS TCP address host:port (required), e.g. 127.0.0.1:8001")
		showHex   = flag.Bool("hex", false, "also print hex dump of the decoded AX.25 frame bytes")
		showInfo  = flag.Bool("info", true, "print an ASCII-ish preview of the AX.25 info field (if present)")
		maxInfo   = flag.Int("max-info", 120, "max bytes of info preview to print")
		queueSize = flag.Int("q", 256, "frame print queue size (drops if overwhelmed)")
	)
	flag.Parse()

	if *server == "" {
		fmt.Fprintln(os.Stderr, "error: -server is required")
		flag.Usage()
		os.Exit(2)
	}

	conn, err := net.Dial("tcp", *server)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial KISS TCP %q: %v\n", *server, err)
		os.Exit(1)
	}
	defer conn.Close()

	fmt.Fprintf(os.Stderr, "connected to KISS TCP %s\n", *server)

	frames := make(chan kissFrame, *queueSize)
	var dropped uint64

	dec := ax25.NewKISSDecoder(func(port byte, cmd byte, payload []byte) {
		// Never block the decoder path: copy + enqueue, or drop.
		b := make([]byte, len(payload))
		copy(b, payload)

		select {
		case frames <- kissFrame{ts: time.Now(), port: port, cmd: cmd, ax25: b}:
		default:
			atomic.AddUint64(&dropped, 1)
		}
	})

	// Reader goroutine: pulls bytes from TCP and feeds the streaming decoder.
	readErr := make(chan error, 1)
	go func() {
		_, err := io.Copy(dec, conn)
		readErr <- err
	}()

	// Handle Ctrl+C cleanly.
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	for {
		select {
		case fr := <-frames:
			printOne(fr, *showHex, *showInfo, *maxInfo)

		case err := <-readErr:
			// io.Copy returns nil on clean EOF sometimes; treat both as exit.
			if err != nil {
				fmt.Fprintf(os.Stderr, "read error: %v\n", err)
			} else {
				fmt.Fprintln(os.Stderr, "connection closed")
			}
			reportDrops(dropped)
			return

		case <-sigCh:
			fmt.Fprintln(os.Stderr, "\ninterrupt; exiting")
			reportDrops(dropped)
			return
		}
	}
}

type kissFrame struct {
	ts   time.Time
	port byte
	cmd  byte
	ax25 []byte
}

func reportDrops(dropped uint64) {
	n := atomic.LoadUint64(&dropped)
	if n > 0 {
		fmt.Fprintf(os.Stderr, "dropped %d frames (print queue full)\n", n)
	}
}

func printOne(fr kissFrame, showHex, showInfo bool, maxInfo int) {
	parsed, err := ax25.ParseFrame(fr.ax25)
	if err != nil {
		fmt.Printf("%s KISS port=%d cmd=0x%02x AX25(len=%d) PARSE_ERROR: %v\n",
			fr.ts.Format(time.RFC3339Nano), fr.port, fr.cmd, len(fr.ax25), err)
		if showHex {
			fmt.Printf("  HEX %s\n", hex.EncodeToString(fr.ax25))
		}
		return
	}

	// These field names assume the go-ax25 Frame mirrors the earlier plan:
	// Dst, Src (ax25.Address), Digis []ax25.Address, Control byte, PID byte, Info []byte, Type ax25.FrameType.
	path := formatPath(*parsed)

	pf := ax25.HasPF(parsed.Control)
	line := fmt.Sprintf("%s %s  type=%s pf=%v",
		fr.ts.Format("15:04:05.000"), path, parsed.Type, pf)

	// Add NS/NR for I/S frames (harmless for UI; values may be 0).
	switch parsed.Type {
	case ax25.FrameI, ax25.FrameS:
		line += fmt.Sprintf(" ns=%d nr=%d", ax25.ExtractNS(parsed.Control), ax25.ExtractNR(parsed.Control))
	}

	// Add PID if it’s a UI/I frame with PID present (depends on your parser behavior).
	// If your Frame type uses a different “has PID” indicator, adjust accordingly.
	line += fmt.Sprintf(" len=%d", len(fr.ax25))

	fmt.Println(line)

	if showInfo && len(parsed.Payload) > 0 {
		info := parsed.Payload
		if maxInfo > 0 && len(info) > maxInfo {
			info = info[:maxInfo]
		}
		fmt.Printf("  INFO %s\n", sanitizeASCII(info))
	}

	if showHex {
		fmt.Printf("  HEX  %s\n", spacedHex(fr.ax25))
	}
}

func formatPath(f ax25.Frame) string {
	var b bytes.Buffer
	b.WriteString(f.Source.String())
	b.WriteString(">")
	b.WriteString(f.Destination.String())

	for _, d := range f.Digipeaters {
		b.WriteString(",")
		b.WriteString(d.String())
	}
	return b.String()
}

func sanitizeASCII(p []byte) string {
	out := make([]rune, 0, len(p))
	for _, c := range p {
		r := rune(c)
		if r == '\r' {
			out = append(out, '␍')
			continue
		}
		if r == '\n' {
			out = append(out, '␊')
			continue
		}
		if unicode.IsPrint(r) && r != '\u007f' {
			out = append(out, r)
		} else {
			out = append(out, '.')
		}
	}
	return string(out)
}

func spacedHex(b []byte) string {
	const cols = 32
	var out bytes.Buffer
	for i := 0; i < len(b); i++ {
		if i > 0 {
			if i%cols == 0 {
				out.WriteString("\n       ")
			} else {
				out.WriteByte(' ')
			}
		}
		fmt.Fprintf(&out, "%02x", b[i])
	}
	return out.String()
}
