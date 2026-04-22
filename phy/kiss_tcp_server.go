package phy

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/n7get/go-ax25/ax25"
)

// KISSTCPServerConn represents a single client connection to the KISS TCP server.
type KISSTCPServerConn struct {
	conn     net.Conn
	server   *KISSTCPServerPHY
	txCh     chan []byte
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	UserData interface{}
}

// Send encodes frame as KISS and enqueues it for transmission to this client.
// Non-blocking: returns ErrTXQueueFull if the queue is full.
func (c *KISSTCPServerConn) Send(frame *ax25.Frame) error {
	if frame == nil {
		return fmt.Errorf("phy: KISSTCPServerConn.Send: nil frame")
	}
	raw, err := frame.Encode()
	if err != nil {
		return fmt.Errorf("phy: KISSTCPServerConn.Send: encode: %w", err)
	}
	kissed := ax25.KISSEncode(0, 0, raw)
	select {
	case c.txCh <- kissed:
		return nil
	default:
		return ax25.ErrTXQueueFull
	}
}

// RemoteAddr returns the remote address of the client.
func (c *KISSTCPServerConn) RemoteAddr() net.Addr { return c.conn.RemoteAddr() }

// KISSTCPServerConfig holds configuration for KISSTCPServerPHY.
type KISSTCPServerConfig struct {
	Port           uint16
	TXQueueDepth   int
	ReadBufSize    int
	OnConnected    func(conn *KISSTCPServerConn)
	OnDisconnected func(conn *KISSTCPServerConn)
	OnRxFrame      func(conn *KISSTCPServerConn, frame *ax25.Frame)
	OnError        ax25.ErrorCallback
}

// NewKISSTCPServerConfigFromConfig populates KISSTCPServerConfig from ax25.Config.
func NewKISSTCPServerConfigFromConfig(cfg *ax25.Config) KISSTCPServerConfig {
	return KISSTCPServerConfig{
		Port:         uint16(cfg.GetInt(ax25.KeyKissServerPort)),
		TXQueueDepth: cfg.GetInt(ax25.KeyKissServerTxQueueDepth),
		ReadBufSize:  cfg.GetInt(ax25.KeyKissServerReadBuf),
	}
}

// KISSTCPServerPHY is a KISS-over-TCP server PHY driver.
// It listens for incoming TCP connections, decodes KISS frames from each
// client, and delivers them via OnRxFrame. Each client gets its own TX
// goroutine. Send is non-blocking per client.
type KISSTCPServerPHY struct {
	cfg    KISSTCPServerConfig
	ln     net.Listener
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewKISSTCPServerPHY creates a new KISSTCPServerPHY. Call Start to listen.
func NewKISSTCPServerPHY(cfg KISSTCPServerConfig) (*KISSTCPServerPHY, error) {
	if cfg.Port == 0 {
		return nil, fmt.Errorf("phy: KISSTCPServerPHY: Port must not be 0")
	}
	if cfg.OnRxFrame == nil {
		return nil, fmt.Errorf("phy: KISSTCPServerPHY: OnRxFrame must not be nil")
	}
	if cfg.TXQueueDepth == 0 {
		cfg.TXQueueDepth = 8
	}
	if cfg.ReadBufSize == 0 {
		cfg.ReadBufSize = 4096
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &KISSTCPServerPHY{cfg: cfg, ctx: ctx, cancel: cancel}, nil
}

// Start begins listening and accepting connections.
func (p *KISSTCPServerPHY) Start() error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", p.cfg.Port))
	if err != nil {
		return fmt.Errorf("phy: KISSTCPServerPHY: listen :%d: %w", p.cfg.Port, err)
	}
	p.ln = ln
	p.wg.Add(1)
	go p.acceptLoop()
	return nil
}

// Stop closes the listener and waits for all goroutines to exit.
func (p *KISSTCPServerPHY) Stop() {
	p.cancel()
	if p.ln != nil {
		_ = p.ln.Close()
	}
	p.wg.Wait()
}

func (p *KISSTCPServerPHY) acceptLoop() {
	defer p.wg.Done()
	for {
		conn, err := p.ln.Accept()
		if err != nil {
			select {
			case <-p.ctx.Done():
				return
			default:
				if p.cfg.OnError != nil {
					p.cfg.OnError(fmt.Errorf("phy: accept: %w", err))
				}
				return
			}
		}
		p.wg.Add(1)
		go p.handleConn(conn)
	}
}

func (p *KISSTCPServerPHY) handleConn(netConn net.Conn) {
	defer p.wg.Done()

	ctx, cancel := context.WithCancel(p.ctx)
	c := &KISSTCPServerConn{
		conn:   netConn,
		server: p,
		txCh:   make(chan []byte, p.cfg.TXQueueDepth),
		ctx:    ctx,
		cancel: cancel,
	}

	if p.cfg.OnConnected != nil {
		p.cfg.OnConnected(c)
	}

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case pkt, ok := <-c.txCh:
				if !ok {
					return
				}
				if _, err := netConn.Write(pkt); err != nil {
					if p.cfg.OnError != nil {
						p.cfg.OnError(fmt.Errorf("phy: tcp server write: %w", err))
					}
					return
				}
			}
		}
	}()

	decoder := ax25.NewKISSDecoder(func(portNum, cmd uint8, data []byte) {
		if cmd != 0 {
			return
		}
		frame, err := ax25.ParseFrame(data)
		if err != nil {
			if p.cfg.OnError != nil {
				p.cfg.OnError(fmt.Errorf("phy: server parse frame: %w", err))
			}
			return
		}
		p.cfg.OnRxFrame(c, frame)
	})

	buf := make([]byte, p.cfg.ReadBufSize)
	for {
		n, err := netConn.Read(buf)
		if n > 0 {
			_, _ = decoder.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}

	cancel()
	_ = netConn.Close()
	c.wg.Wait()

	if p.cfg.OnDisconnected != nil {
		p.cfg.OnDisconnected(c)
	}
}
