package agwpe

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/n7get/go-ax25/ax25"
)

// ClientConfig holds configuration for Client.
type ClientConfig struct {
	Host           string
	Port           uint16
	ConnectTimeout time.Duration
	ReconnectDelay time.Duration
	TXQueueDepth   int
	OnRxFrame      FrameCallback
	OnError        ax25.ErrorCallback
}

func (c *ClientConfig) withDefaults() ClientConfig {
	out := *c
	if out.ConnectTimeout == 0 {
		out.ConnectTimeout = 6 * time.Second
	}
	if out.ReconnectDelay == 0 {
		out.ReconnectDelay = 5 * time.Second
	}
	if out.TXQueueDepth == 0 {
		out.TXQueueDepth = 8
	}
	return out
}

// Client is a non-blocking AGWPE TCP client.
// It dials an AGWPE server, decodes incoming AGWPE frames, and delivers them
// via OnRxFrame. Outgoing frames are queued and sent by a dedicated TX
// goroutine. The client reconnects automatically on disconnection.
type Client struct {
	cfg    ClientConfig
	txCh   chan []byte
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu   sync.Mutex
	conn net.Conn
}

// NewClient creates a new AGWPE Client. Call Start to begin connecting.
func NewClient(cfg ClientConfig) (*Client, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("agwpe: Client: Host must not be empty")
	}
	if cfg.Port == 0 {
		return nil, fmt.Errorf("agwpe: Client: Port must not be 0")
	}
	if cfg.OnRxFrame == nil {
		return nil, fmt.Errorf("agwpe: Client: OnRxFrame must not be nil")
	}
	cfg = cfg.withDefaults()
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		cfg:    cfg,
		txCh:   make(chan []byte, cfg.TXQueueDepth),
		ctx:    ctx,
		cancel: cancel,
	}, nil
}

// Start launches the RX goroutine.
func (c *Client) Start() {
	c.wg.Add(1)
	go c.rxLoop()
}

// Stop signals all goroutines to exit and waits for them to finish.
func (c *Client) Stop() {
	c.cancel()
	c.mu.Lock()
	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.mu.Unlock()
	c.wg.Wait()
}

// SendFrame encodes f and enqueues it for transmission.
func (c *Client) SendFrame(f *Frame) error {
	if f == nil {
		return fmt.Errorf("agwpe: Client.SendFrame: nil frame")
	}
	c.mu.Lock()
	connected := c.conn != nil
	c.mu.Unlock()
	if !connected {
		return ax25.ErrNotConnected
	}
	enc, err := f.Encode()
	if err != nil {
		return fmt.Errorf("agwpe: Client.SendFrame: encode: %w", err)
	}
	select {
	case c.txCh <- enc:
		return nil
	default:
		return ax25.ErrTXQueueFull
	}
}

func (c *Client) RequestVersion() error        { return c.SendFrame(BuildVersionReq()) }
func (c *Client) RequestPortInfo() error        { return c.SendFrame(BuildPortInfoReq()) }
func (c *Client) RequestPortCap(port uint8) error { return c.SendFrame(BuildPortCapReq(port)) }
func (c *Client) EnableMonitor() error          { return c.SendFrame(BuildEnableMonitor()) }
func (c *Client) EnableRaw() error              { return c.SendFrame(BuildEnableRaw()) }

// SendUnproto sends an AX.25 UI frame via AGWPE unproto.
func (c *Client) SendUnproto(frame *ax25.Frame, port uint8) error {
	agf, err := FromAX25Unproto(frame, port)
	if err != nil {
		return err
	}
	return c.SendFrame(agf)
}

// SendRaw sends an AX.25 frame via AGWPE raw.
func (c *Client) SendRaw(frame *ax25.Frame, port uint8) error {
	agf, err := FromAX25Raw(frame, port)
	if err != nil {
		return err
	}
	return c.SendFrame(agf)
}

func (c *Client) addr() string {
	return fmt.Sprintf("%s:%d", c.cfg.Host, c.cfg.Port)
}

func (c *Client) rxLoop() {
	defer c.wg.Done()

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		dialer := net.Dialer{Timeout: c.cfg.ConnectTimeout}
		conn, err := dialer.DialContext(c.ctx, "tcp", c.addr())
		if err != nil {
			if c.cfg.OnError != nil {
				c.cfg.OnError(fmt.Errorf("agwpe: dial %s: %w", c.addr(), err))
			}
			select {
			case <-c.ctx.Done():
				return
			case <-time.After(c.cfg.ReconnectDelay):
			}
			continue
		}

		c.mu.Lock()
		c.conn = conn
		c.mu.Unlock()

		txDone := make(chan struct{})
		c.wg.Add(1)
		go c.txLoop(conn, txDone)

		dec := NewDecoder(func(f *Frame) {
			c.cfg.OnRxFrame(f)
		})

		buf := make([]byte, 4096)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(time.Second))
			n, err := conn.Read(buf)
			if n > 0 {
				_, _ = dec.Write(buf[:n])
			}
			if err != nil {
				break
			}
			select {
			case <-c.ctx.Done():
				goto disconnected
			default:
			}
		}

	disconnected:
		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()
		_ = conn.Close()
		<-txDone

		select {
		case <-c.ctx.Done():
			return
		case <-time.After(c.cfg.ReconnectDelay):
		}
	}
}

func (c *Client) txLoop(conn net.Conn, done chan struct{}) {
	defer c.wg.Done()
	defer close(done)

	for {
		select {
		case <-c.ctx.Done():
			return
		case pkt, ok := <-c.txCh:
			if !ok {
				return
			}
			if _, err := conn.Write(pkt); err != nil {
				if c.cfg.OnError != nil {
					c.cfg.OnError(fmt.Errorf("agwpe: tcp write: %w", err))
				}
				return
			}
		}
	}
}
