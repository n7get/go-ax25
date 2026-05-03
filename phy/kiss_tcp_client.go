// Package phy provides physical-layer drivers for go-ax25.
package phy

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/n7get/go-ax25/ax25"
)

// KISSTCPClientConfig holds configuration for KISSTCPClientPHY.
type KISSTCPClientConfig struct {
	Host           string
	Port           uint16
	ConnectTimeout time.Duration
	ReconnectDelay time.Duration
	TXQueueDepth   int
	ReadBufSize    int
	OnRxKISS       func([]byte)
	OnTxKISS       func([]byte)
	OnRxFrame      ax25.FrameCallback
	OnError        ax25.ErrorCallback
}

// NewKISSTCPClientConfigFromConfig populates KISSTCPClientConfig from ax25.Config.
func NewKISSTCPClientConfigFromConfig(cfg *ax25.Config) KISSTCPClientConfig {
	return KISSTCPClientConfig{
		Host:           cfg.GetStr(ax25.KeyKissClientHost),
		Port:           uint16(cfg.GetInt(ax25.KeyKissClientPort)),
		ConnectTimeout: 6 * time.Second, // Could be made configurable
		ReconnectDelay: 5 * time.Second, // Could be made configurable
		TXQueueDepth:   cfg.GetInt(ax25.KeyKissClientTxQueueDepth),
		ReadBufSize:    cfg.GetInt(ax25.KeyKissClientReadBuf),
	}
}

// KISSTCPClientPHY is a KISS-over-TCP client PHY driver.
// It dials a remote KISS soundmodem, decodes incoming KISS frames, and
// delivers them via OnRxFrame. Outgoing frames are queued and sent by a
// dedicated TX goroutine. The driver reconnects automatically on disconnection.
// Send is non-blocking.
type KISSTCPClientPHY struct {
	cfg    KISSTCPClientConfig
	txCh   chan []byte
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu   sync.Mutex
	conn net.Conn
}

// NewKISSTCPClientPHY creates a new KISSTCPClientPHY. Call Start to connect.
func NewKISSTCPClientPHY(cfg KISSTCPClientConfig) (*KISSTCPClientPHY, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("phy: KISSTCPClientPHY: Host must not be empty")
	}
	if cfg.Port == 0 {
		return nil, fmt.Errorf("phy: KISSTCPClientPHY: Port must not be 0")
	}
	if cfg.OnRxFrame == nil {
		return nil, fmt.Errorf("phy: KISSTCPClientPHY: OnRxFrame must not be nil")
	}
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = 6 * time.Second
	}
	if cfg.ReconnectDelay == 0 {
		cfg.ReconnectDelay = 5 * time.Second
	}
	if cfg.TXQueueDepth == 0 {
		cfg.TXQueueDepth = 8
	}
	if cfg.ReadBufSize == 0 {
		cfg.ReadBufSize = 4096
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &KISSTCPClientPHY{
		cfg:    cfg,
		txCh:   make(chan []byte, cfg.TXQueueDepth),
		ctx:    ctx,
		cancel: cancel,
	}, nil
}

// Start launches the RX goroutine (which spawns TX goroutines per connection).
func (p *KISSTCPClientPHY) Start() {
	p.wg.Add(1)
	go p.rxLoop()
}

// Stop signals all goroutines to exit and waits for them to finish.
func (p *KISSTCPClientPHY) Stop() {
	p.cancel()
	p.mu.Lock()
	if p.conn != nil {
		_ = p.conn.Close()
	}
	p.mu.Unlock()
	p.wg.Wait()
}

// Send encodes frame as KISS and enqueues it for transmission.
// Returns ErrTXQueueFull if the queue is full, ErrNotConnected if not connected.
func (p *KISSTCPClientPHY) Send(frame *ax25.Frame) error {
	if frame == nil {
		return fmt.Errorf("phy: Send: nil frame")
	}
	p.mu.Lock()
	connected := p.conn != nil
	p.mu.Unlock()
	if !connected {
		return ax25.ErrNotConnected
	}
	raw, err := frame.Encode()
	if err != nil {
		return fmt.Errorf("phy: Send: encode: %w", err)
	}
	kissed := ax25.KISSEncode(0, 0, raw)
	if p.cfg.OnTxKISS != nil {
		p.cfg.OnTxKISS(append([]byte(nil), kissed...))
	}
	select {
	case p.txCh <- kissed:
		return nil
	default:
		return ax25.ErrTXQueueFull
	}
}

// IsConnected reports whether the TCP transport is currently connected.
func (p *KISSTCPClientPHY) IsConnected() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.conn != nil
}

func (p *KISSTCPClientPHY) addr() string {
	return fmt.Sprintf("%s:%d", p.cfg.Host, p.cfg.Port)
}

func (p *KISSTCPClientPHY) rxLoop() {
	defer p.wg.Done()

	for {
		select {
		case <-p.ctx.Done():
			return
		default:
		}

		dialer := net.Dialer{Timeout: p.cfg.ConnectTimeout}
		conn, err := dialer.DialContext(p.ctx, "tcp", p.addr())
		if err != nil {
			if p.cfg.OnError != nil {
				p.cfg.OnError(fmt.Errorf("phy: dial %s: %w", p.addr(), err))
			}
			select {
			case <-p.ctx.Done():
				return
			case <-time.After(p.cfg.ReconnectDelay):
			}
			continue
		}

		p.mu.Lock()
		p.conn = conn
		p.mu.Unlock()

		txDone := make(chan struct{})
		p.wg.Add(1)
		go p.txLoop(conn, txDone)

		decoder := ax25.NewKISSDecoder(func(portNum, cmd uint8, data []byte) {
			if cmd != 0 {
				return
			}
			if p.cfg.OnRxKISS != nil {
				kissed := ax25.KISSEncode(portNum, cmd, data)
				p.cfg.OnRxKISS(kissed)
			}
			frame, err := ax25.ParseFrame(data)
			if err != nil {
				if p.cfg.OnError != nil {
					p.cfg.OnError(fmt.Errorf("phy: parse frame: %w", err))
				}
				return
			}
			p.cfg.OnRxFrame(frame)
		})

		buf := make([]byte, p.cfg.ReadBufSize)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(time.Second))
			n, err := conn.Read(buf)
			if n > 0 {
				_, _ = decoder.Write(buf[:n])
			}
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					select {
					case <-p.ctx.Done():
						goto disconnected
					default:
					}
					continue
				}
				break
			}
			select {
			case <-p.ctx.Done():
				goto disconnected
			default:
			}
		}

	disconnected:
		p.mu.Lock()
		p.conn = nil
		p.mu.Unlock()
		_ = conn.Close()
		<-txDone

		select {
		case <-p.ctx.Done():
			return
		case <-time.After(p.cfg.ReconnectDelay):
		}
	}
}

func (p *KISSTCPClientPHY) txLoop(conn net.Conn, done chan struct{}) {
	defer p.wg.Done()
	defer close(done)

	for {
		select {
		case <-p.ctx.Done():
			return
		case pkt, ok := <-p.txCh:
			if !ok {
				return
			}
			if _, err := conn.Write(pkt); err != nil {
				if p.cfg.OnError != nil {
					p.cfg.OnError(fmt.Errorf("phy: tcp write: %w", err))
				}
				return
			}
		}
	}
}

const defaultKISSTCPClientTXQueueDepth = 8
