package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/n7get/go-ax25/ax25"
	"github.com/n7get/go-ax25/pcap"
)

const (
	monitorTypeAX25 = "ax25"
	monitorTypeKISS = "kiss"
)

var (
	errMonitorVVExhausted = errors.New("monitor: daily file index exhausted (vv > 99)")
)

type frameMonitor struct {
	mu sync.Mutex

	typ      string
	prefix   string
	linkType pcap.LinkType
	nowFn    func() time.Time

	file       *os.File
	writer     *pcap.Writer
	currentDay string
	currentVV  int
	currentLog string

	stopCh chan struct{}
	doneCh chan struct{}
}

func newFrameMonitor(typ, prefix string) (*frameMonitor, error) {
	if typ != monitorTypeAX25 && typ != monitorTypeKISS {
		return nil, fmt.Errorf("config error: monitor.type must be ax25 or kiss")
	}
	if prefix == "" {
		return nil, fmt.Errorf("config error: monitor.prefix must not be empty")
	}

	linkType := pcap.LinkTypeAX25
	if typ == monitorTypeKISS {
		linkType = pcap.LinkTypeKISS
	}

	m := &frameMonitor{
		typ:      typ,
		prefix:   prefix,
		linkType: linkType,
		nowFn:    time.Now,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}

	if err := m.openInitial(); err != nil {
		return nil, err
	}

	go m.midnightLoop()
	return m, nil
}

func (m *frameMonitor) openInitial() error {
	now := m.nowFn()
	day := yymmdd(now)
	vv, err := firstAvailableVV(m.prefix, day)
	if err != nil {
		return err
	}
	return m.swapTo(day, vv, "startup")
}

func (m *frameMonitor) Close() {
	close(m.stopCh)
	<-m.doneCh

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.file != nil {
		_ = m.file.Close()
		m.file = nil
		m.writer = nil
	}
}

func (m *frameMonitor) LogAX25(frame *ax25.Frame) error {
	if frame == nil {
		return nil
	}
	data, err := frame.Encode()
	if err != nil {
		return fmt.Errorf("monitor: encode ax25 frame: %w", err)
	}
	return m.writeData(data)
}

func (m *frameMonitor) LogKISS(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	copied := append([]byte(nil), data...)
	return m.writeData(copied)
}

func (m *frameMonitor) writeData(data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.writer == nil {
		return errors.New("monitor: writer is not open")
	}
	return m.writer.WriteRecord(pcap.Record{Timestamp: m.nowFn(), Data: data})
}

func (m *frameMonitor) RotateSIGHUP() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.nowFn()
	day := yymmdd(now)
	if day != m.currentDay {
		vv, err := firstAvailableVV(m.prefix, day)
		if err != nil {
			return err
		}
		return m.swapTo(day, vv, "sighup/day-change")
	}

	nextVV := m.currentVV + 1
	if nextVV > 99 {
		return errMonitorVVExhausted
	}
	return m.swapTo(day, nextVV, "sighup")
}

func (m *frameMonitor) rotateMidnight(now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	day := yymmdd(now)
	if day == m.currentDay {
		return nil
	}
	vv, err := firstAvailableVV(m.prefix, day)
	if err != nil {
		return err
	}
	return m.swapTo(day, vv, "midnight")
}

func (m *frameMonitor) swapTo(day string, vv int, reason string) error {
	path := captureFileName(m.prefix, day, vv)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("monitor: open capture file %q: %w", path, err)
	}

	w, err := pcap.Create(f, m.linkType)
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("monitor: create pcap writer for %q: %w", path, err)
	}

	if m.file != nil {
		_ = m.file.Close()
	}
	m.file = f
	m.writer = w
	m.currentDay = day
	m.currentVV = vv
	m.currentLog = path

	slog.Info("monitor: capture file active", "path", path, "type", m.typ, "reason", reason)
	return nil
}

func (m *frameMonitor) midnightLoop() {
	defer close(m.doneCh)

	for {
		now := m.nowFn()
		next := nextMidnight(now)
		timer := time.NewTimer(time.Until(next))
		select {
		case <-m.stopCh:
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
			if err := m.rotateMidnight(m.nowFn()); err != nil {
				slog.Error("monitor: midnight rotation failed", "err", err)
			}
		}
	}
}

func firstAvailableVV(prefix, day string) (int, error) {
	for i := 0; i <= 99; i++ {
		path := captureFileName(prefix, day, i)
		_, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			return i, nil
		}
		if err != nil {
			return -1, fmt.Errorf("monitor: stat %q: %w", path, err)
		}
	}
	return -1, errMonitorVVExhausted
}

func captureFileName(prefix, day string, vv int) string {
	return fmt.Sprintf("%s-%s%02d.pcap", prefix, day, vv)
}

func yymmdd(t time.Time) string {
	return t.Format("060102")
}

func nextMidnight(now time.Time) time.Time {
	loc := now.Location()
	y, m, d := now.Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, loc)
}
