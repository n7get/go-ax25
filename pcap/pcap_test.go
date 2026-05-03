// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package pcap_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/n7get/go-ax25/pcap"
)

// mustTS builds a UTC time with microsecond precision from sec + usec.
func mustTS(sec int64, usec int64) time.Time {
	return time.Unix(sec, usec*1000).UTC()
}

// --- Round-trip tests ---

func TestRoundTrip_AX25(t *testing.T) {
	records := []pcap.Record{
		{Timestamp: mustTS(1_000_000, 500_000), Data: []byte{0x01, 0x02, 0x03}},
		{Timestamp: mustTS(1_000_001, 0), Data: []byte{0xAA, 0xBB, 0xCC, 0xDD}},
		{Timestamp: mustTS(1_000_002, 999_999), Data: []byte{0xFF}},
	}

	var buf bytes.Buffer
	w, err := pcap.Create(&buf, pcap.LinkTypeAX25)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for i, r := range records {
		if err := w.WriteRecord(r); err != nil {
			t.Fatalf("WriteRecord[%d]: %v", i, err)
		}
	}

	r, err := pcap.NewReader(&buf)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	if r.LinkType() != pcap.LinkTypeAX25 {
		t.Errorf("LinkType = %d, want LinkTypeAX25 (%d)", r.LinkType(), pcap.LinkTypeAX25)
	}

	for i, want := range records {
		got, err := r.ReadRecord()
		if err != nil {
			t.Fatalf("ReadRecord[%d]: %v", i, err)
		}
		if !got.Timestamp.Equal(want.Timestamp) {
			t.Errorf("record[%d] Timestamp = %v, want %v", i, got.Timestamp, want.Timestamp)
		}
		if !bytes.Equal(got.Data, want.Data) {
			t.Errorf("record[%d] Data = %v, want %v", i, got.Data, want.Data)
		}
	}

	_, err = r.ReadRecord()
	if err != io.EOF {
		t.Errorf("after last record: got %v, want io.EOF", err)
	}
}

func TestRoundTrip_KISS(t *testing.T) {
	// A KISS frame: FEND + cmd byte + AX.25 bytes + FEND.
	kissFrame := []byte{0xC0, 0x00, 0x82, 0x84, 0x86, 0x88, 0x8A, 0x60, 0xC0}
	rec := pcap.Record{
		Timestamp: mustTS(2_000_000, 123_456),
		Data:      kissFrame,
	}

	var buf bytes.Buffer
	w, err := pcap.Create(&buf, pcap.LinkTypeKISS)
	if err != nil {
		t.Fatalf("Create KISS: %v", err)
	}
	if err := w.WriteRecord(rec); err != nil {
		t.Fatalf("WriteRecord KISS: %v", err)
	}

	r, err := pcap.NewReader(&buf)
	if err != nil {
		t.Fatalf("NewReader KISS: %v", err)
	}
	if r.LinkType() != pcap.LinkTypeKISS {
		t.Errorf("LinkType = %d, want LinkTypeKISS (%d)", r.LinkType(), pcap.LinkTypeKISS)
	}

	got, err := r.ReadRecord()
	if err != nil {
		t.Fatalf("ReadRecord KISS: %v", err)
	}
	if !got.Timestamp.Equal(rec.Timestamp) {
		t.Errorf("Timestamp = %v, want %v", got.Timestamp, rec.Timestamp)
	}
	if !bytes.Equal(got.Data, rec.Data) {
		t.Errorf("Data mismatch: got %v, want %v", got.Data, rec.Data)
	}
}

// --- Boundary tests ---

func TestWriteRecord_emptyData(t *testing.T) {
	var buf bytes.Buffer
	w, err := pcap.Create(&buf, pcap.LinkTypeAX25)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rec := pcap.Record{Timestamp: mustTS(1, 0), Data: []byte{}}
	if err := w.WriteRecord(rec); err != nil {
		t.Fatalf("WriteRecord empty: %v", err)
	}

	r, err := pcap.NewReader(&buf)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	got, err := r.ReadRecord()
	if err != nil {
		t.Fatalf("ReadRecord: %v", err)
	}
	if len(got.Data) != 0 {
		t.Errorf("expected empty Data, got %d bytes", len(got.Data))
	}
}

func TestWriteRecord_nilData(t *testing.T) {
	var buf bytes.Buffer
	w, err := pcap.Create(&buf, pcap.LinkTypeAX25)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rec := pcap.Record{Timestamp: mustTS(1, 0), Data: nil}
	if err := w.WriteRecord(rec); err != nil {
		t.Fatalf("WriteRecord nil: %v", err)
	}

	r, err := pcap.NewReader(&buf)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	got, err := r.ReadRecord()
	if err != nil {
		t.Fatalf("ReadRecord: %v", err)
	}
	if len(got.Data) != 0 {
		t.Errorf("expected empty Data for nil input, got %d bytes", len(got.Data))
	}
}

func TestWriteRecord_maxLen(t *testing.T) {
	data := make([]byte, 65535)
	for i := range data {
		data[i] = byte(i & 0xFF)
	}

	var buf bytes.Buffer
	w, err := pcap.Create(&buf, pcap.LinkTypeAX25)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rec := pcap.Record{Timestamp: mustTS(1, 0), Data: data}
	if err := w.WriteRecord(rec); err != nil {
		t.Fatalf("WriteRecord max: %v", err)
	}

	r, err := pcap.NewReader(&buf)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	got, err := r.ReadRecord()
	if err != nil {
		t.Fatalf("ReadRecord max: %v", err)
	}
	if !bytes.Equal(got.Data, data) {
		t.Error("Data mismatch for max-length record")
	}
}

// --- Malformed input tests ---

func TestWriteRecord_tooLarge(t *testing.T) {
	var buf bytes.Buffer
	w, err := pcap.Create(&buf, pcap.LinkTypeAX25)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	err = w.WriteRecord(pcap.Record{Timestamp: mustTS(1, 0), Data: make([]byte, 65536)})
	if !errors.Is(err, pcap.ErrRecordTooLarge) {
		t.Errorf("expected ErrRecordTooLarge, got %v", err)
	}
}

func TestCreate_unsupportedLinkType(t *testing.T) {
	var buf bytes.Buffer
	_, err := pcap.Create(&buf, pcap.LinkType(1)) // DLT_EN10MB, not supported
	if !errors.Is(err, pcap.ErrUnsupportedLinkType) {
		t.Errorf("expected ErrUnsupportedLinkType, got %v", err)
	}
}

func TestNewReader_truncatedHeader(t *testing.T) {
	_, err := pcap.NewReader(bytes.NewReader([]byte{0x01, 0x02, 0x03}))
	if !errors.Is(err, pcap.ErrInvalidPcap) {
		t.Errorf("expected ErrInvalidPcap for truncated header, got %v", err)
	}
}

func TestNewReader_badMagic(t *testing.T) {
	hdr := make([]byte, 24)
	binary.LittleEndian.PutUint32(hdr[0:], 0xDEADBEEF)
	_, err := pcap.NewReader(bytes.NewReader(hdr))
	if !errors.Is(err, pcap.ErrInvalidPcap) {
		t.Errorf("expected ErrInvalidPcap for bad magic, got %v", err)
	}
}

func TestNewReader_unsupportedLinkType(t *testing.T) {
	hdr := buildGlobalHeader(t, binary.LittleEndian, 0xa1b2c3d4, 1 /* DLT_EN10MB */)
	_, err := pcap.NewReader(bytes.NewReader(hdr))
	if !errors.Is(err, pcap.ErrUnsupportedLinkType) {
		t.Errorf("expected ErrUnsupportedLinkType, got %v", err)
	}
}

func TestReadRecord_EOF(t *testing.T) {
	var buf bytes.Buffer
	if _, err := pcap.Create(&buf, pcap.LinkTypeAX25); err != nil {
		t.Fatalf("Create: %v", err)
	}
	r, err := pcap.NewReader(&buf)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	_, err = r.ReadRecord()
	if err != io.EOF {
		t.Errorf("expected io.EOF on empty stream, got %v", err)
	}
}

func TestReadRecord_truncatedRecordHeader(t *testing.T) {
	var buf bytes.Buffer
	if _, err := pcap.Create(&buf, pcap.LinkTypeAX25); err != nil {
		t.Fatalf("Create: %v", err)
	}
	buf.Write([]byte{0x01, 0x02, 0x03}) // only 3 of the required 16 header bytes
	r, err := pcap.NewReader(&buf)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	_, err = r.ReadRecord()
	if err == nil || err == io.EOF {
		t.Errorf("expected truncation error, got %v", err)
	}
}

func TestReadRecord_tooLarge(t *testing.T) {
	var buf bytes.Buffer
	if _, err := pcap.Create(&buf, pcap.LinkTypeAX25); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Inject a record header claiming incl_len = 65536.
	var rhdr [16]byte
	le := binary.LittleEndian
	le.PutUint32(rhdr[0:], 1)     // ts_sec
	le.PutUint32(rhdr[4:], 0)     // ts_usec
	le.PutUint32(rhdr[8:], 65536) // incl_len — over limit
	le.PutUint32(rhdr[12:], 65536)
	buf.Write(rhdr[:])

	r, err := pcap.NewReader(&buf)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	_, err = r.ReadRecord()
	if !errors.Is(err, pcap.ErrRecordTooLarge) {
		t.Errorf("expected ErrRecordTooLarge, got %v", err)
	}
}

// --- Big-endian and nanosecond reader tests ---

func TestNewReader_bigEndian(t *testing.T) {
	be := binary.BigEndian
	hdr := buildGlobalHeader(t, be, 0xa1b2c3d4, 3 /* LinkTypeAX25 */)

	data := []byte{0x01, 0x02, 0x03, 0x04}
	var rhdr [16]byte
	be.PutUint32(rhdr[0:], 9999)
	be.PutUint32(rhdr[4:], 500_000)
	be.PutUint32(rhdr[8:], uint32(len(data)))
	be.PutUint32(rhdr[12:], uint32(len(data)))

	var buf bytes.Buffer
	buf.Write(hdr)
	buf.Write(rhdr[:])
	buf.Write(data)

	r, err := pcap.NewReader(&buf)
	if err != nil {
		t.Fatalf("NewReader BE: %v", err)
	}
	if r.LinkType() != pcap.LinkTypeAX25 {
		t.Errorf("LinkType = %d, want LinkTypeAX25", r.LinkType())
	}

	rec, err := r.ReadRecord()
	if err != nil {
		t.Fatalf("ReadRecord BE: %v", err)
	}
	if !bytes.Equal(rec.Data, data) {
		t.Errorf("Data mismatch in BE file")
	}
	wantTS := time.Unix(9999, 500_000*1000).UTC()
	if !rec.Timestamp.Equal(wantTS) {
		t.Errorf("Timestamp = %v, want %v", rec.Timestamp, wantTS)
	}
}

func TestNewReader_nanosecond(t *testing.T) {
	le := binary.LittleEndian
	hdr := buildGlobalHeader(t, le, 0xa1b23c4d /* LE nanosec magic */, 3 /* AX25 */)

	data := []byte{0xDE, 0xAD}
	var rhdr [16]byte
	le.PutUint32(rhdr[0:], 12345)
	le.PutUint32(rhdr[4:], 999_999_999) // nanoseconds
	le.PutUint32(rhdr[8:], uint32(len(data)))
	le.PutUint32(rhdr[12:], uint32(len(data)))

	var buf bytes.Buffer
	buf.Write(hdr)
	buf.Write(rhdr[:])
	buf.Write(data)

	r, err := pcap.NewReader(&buf)
	if err != nil {
		t.Fatalf("NewReader nanosec: %v", err)
	}

	rec, err := r.ReadRecord()
	if err != nil {
		t.Fatalf("ReadRecord nanosec: %v", err)
	}
	wantTS := time.Unix(12345, 999_999_999).UTC()
	if !rec.Timestamp.Equal(wantTS) {
		t.Errorf("nanosec Timestamp = %v, want %v", rec.Timestamp, wantTS)
	}
}

// --- Helpers ---

// buildGlobalHeader builds a 24-byte pcap global header using the given byte
// order, magic number, and network/link type value.
func buildGlobalHeader(t *testing.T, order binary.ByteOrder, magic uint32, network uint32) []byte {
	t.Helper()
	hdr := make([]byte, 24)
	// magic is written as raw bytes — for LE files the magic is already in LE
	// form; for BE files, we write it with BE so Wireshark reads it correctly.
	// We always store the canonical magic (e.g. 0xa1b2c3d4) and let the byte
	// order determine how it appears on disk.
	order.PutUint32(hdr[0:], magic)
	order.PutUint16(hdr[4:], 2)
	order.PutUint16(hdr[6:], 4)
	order.PutUint32(hdr[8:], 0)
	order.PutUint32(hdr[12:], 0)
	order.PutUint32(hdr[16:], 65535)
	order.PutUint32(hdr[20:], network)
	return hdr
}
