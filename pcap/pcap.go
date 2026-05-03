// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

// Package pcap provides read and write support for the classic libpcap file
// format (.pcap). Each record stores a timestamp and raw frame bytes; callers
// supply and interpret the bytes themselves.
//
// Supported link types: [LinkTypeAX25] (LINKTYPE_AX25, DLT 3) and
// [LinkTypeKISS] (LINKTYPE_AX25_KISS, DLT 202).
//
// The Writer always produces little-endian files with microsecond timestamp
// precision and snaplen 65535, compatible with tcpdump and Wireshark.
// The Reader accepts both little-endian and big-endian files, and both
// microsecond and nanosecond timestamp variants.
package pcap

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

// LinkType identifies the link-layer type stored in a pcap file.
type LinkType uint32

const (
	// LinkTypeAX25 is LINKTYPE_AX25 (DLT 3): raw AX.25 frame bytes.
	LinkTypeAX25 LinkType = 3
	// LinkTypeKISS is LINKTYPE_AX25_KISS (DLT 202): KISS-framed AX.25 bytes.
	LinkTypeKISS LinkType = 202
)

// Sentinel errors returned by this package.
var (
	// ErrInvalidPcap is returned when the global header is missing or unrecognised.
	ErrInvalidPcap = errors.New("pcap: invalid or unrecognised file header")
	// ErrUnsupportedLinkType is returned when the link type is not AX25 or KISS.
	ErrUnsupportedLinkType = errors.New("pcap: unsupported link type")
	// ErrRecordTooLarge is returned when a record's captured length exceeds maxSnapLen.
	ErrRecordTooLarge = errors.New("pcap: record length exceeds maximum")
)

// pcap magic numbers, always read from the first 4 bytes using LittleEndian.
const (
	magicMicrosecLE uint32 = 0xa1b2c3d4 // LE file, microsecond timestamps
	magicNanosecLE  uint32 = 0xa1b23c4d // LE file, nanosecond timestamps
	magicMicrosecBE uint32 = 0xd4c3b2a1 // BE file, microsecond timestamps
	magicNanosecBE  uint32 = 0x4d3cb2a1 // BE file, nanosecond timestamps
)

const (
	maxSnapLen       = 65535
	globalHeaderSize = 24
	recordHeaderSize = 16
)

// Record is a single captured frame with its timestamp.
type Record struct {
	Timestamp time.Time
	Data      []byte
}

// Writer writes pcap records to an underlying io.Writer.
type Writer struct {
	w        io.Writer
	linkType LinkType
}

// Create writes a pcap global header to w and returns a Writer ready to
// accept records. linkType must be [LinkTypeAX25] or [LinkTypeKISS].
// The file uses little-endian byte order, microsecond timestamp precision,
// and snaplen 65535.
func Create(w io.Writer, linkType LinkType) (*Writer, error) {
	if linkType != LinkTypeAX25 && linkType != LinkTypeKISS {
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedLinkType, uint32(linkType))
	}
	var hdr [globalHeaderSize]byte
	le := binary.LittleEndian
	le.PutUint32(hdr[0:], magicMicrosecLE)
	le.PutUint16(hdr[4:], 2)                 // version_major
	le.PutUint16(hdr[6:], 4)                 // version_minor
	le.PutUint32(hdr[8:], 0)                 // thiszone (UTC)
	le.PutUint32(hdr[12:], 0)                // sigfigs
	le.PutUint32(hdr[16:], maxSnapLen)       // snaplen
	le.PutUint32(hdr[20:], uint32(linkType)) // network
	if _, err := w.Write(hdr[:]); err != nil {
		return nil, fmt.Errorf("pcap: write global header: %w", err)
	}
	return &Writer{w: w, linkType: linkType}, nil
}

// WriteRecord writes a single record to the pcap stream.
// Returns [ErrRecordTooLarge] if len(r.Data) exceeds 65535.
func (pw *Writer) WriteRecord(r Record) error {
	if len(r.Data) > maxSnapLen {
		return fmt.Errorf("%w: %d", ErrRecordTooLarge, len(r.Data))
	}
	var hdr [recordHeaderSize]byte
	le := binary.LittleEndian
	le.PutUint32(hdr[0:], uint32(r.Timestamp.Unix()))
	le.PutUint32(hdr[4:], uint32(r.Timestamp.Nanosecond()/1000)) // microseconds
	le.PutUint32(hdr[8:], uint32(len(r.Data)))                   // incl_len
	le.PutUint32(hdr[12:], uint32(len(r.Data)))                  // orig_len
	if _, err := pw.w.Write(hdr[:]); err != nil {
		return fmt.Errorf("pcap: write record header: %w", err)
	}
	if len(r.Data) > 0 {
		if _, err := pw.w.Write(r.Data); err != nil {
			return fmt.Errorf("pcap: write record data: %w", err)
		}
	}
	return nil
}

// Reader reads pcap records from an underlying io.Reader.
type Reader struct {
	r        io.Reader
	linkType LinkType
	order    binary.ByteOrder
	nano     bool // true when the file uses nanosecond timestamps
}

// NewReader reads the pcap global header from r and returns a Reader.
// Byte order and timestamp precision are auto-detected from the magic number.
// Returns [ErrInvalidPcap] if the header is missing or its magic is unrecognised.
// Returns [ErrUnsupportedLinkType] if the file's link type is not AX25 or KISS.
func NewReader(r io.Reader) (*Reader, error) {
	var hdr [globalHeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, ErrInvalidPcap
	}

	// Detect byte order and timestamp precision from the magic number.
	// We always read the first four bytes with LittleEndian first, then
	// interpret which variant the file uses.
	magic := binary.LittleEndian.Uint32(hdr[0:])
	var (
		order binary.ByteOrder
		nano  bool
	)
	switch magic {
	case magicMicrosecLE:
		order = binary.LittleEndian
	case magicNanosecLE:
		order = binary.LittleEndian
		nano = true
	case magicMicrosecBE:
		order = binary.BigEndian
	case magicNanosecBE:
		order = binary.BigEndian
		nano = true
	default:
		return nil, ErrInvalidPcap
	}

	network := order.Uint32(hdr[20:])
	lt := LinkType(network)
	if lt != LinkTypeAX25 && lt != LinkTypeKISS {
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedLinkType, network)
	}

	return &Reader{
		r:        r,
		linkType: lt,
		order:    order,
		nano:     nano,
	}, nil
}

// LinkType returns the link-layer type declared in the pcap global header.
func (pr *Reader) LinkType() LinkType { return pr.linkType }

// ReadRecord reads the next record from the pcap stream.
// Returns [io.EOF] when the stream is cleanly exhausted.
// Returns a non-nil error (never [io.EOF]) for truncated or malformed records.
func (pr *Reader) ReadRecord() (Record, error) {
	var hdr [recordHeaderSize]byte
	_, err := io.ReadFull(pr.r, hdr[:])
	if err != nil {
		if err == io.EOF {
			return Record{}, io.EOF
		}
		if err == io.ErrUnexpectedEOF {
			return Record{}, fmt.Errorf("pcap: truncated record header")
		}
		return Record{}, fmt.Errorf("pcap: read record header: %w", err)
	}

	tsSec := pr.order.Uint32(hdr[0:])
	tsSubSec := pr.order.Uint32(hdr[4:])
	inclLen := pr.order.Uint32(hdr[8:])
	// orig_len at hdr[12:] is informational; we read exactly inclLen bytes.

	if inclLen > maxSnapLen {
		return Record{}, fmt.Errorf("%w: %d", ErrRecordTooLarge, inclLen)
	}

	data := make([]byte, inclLen)
	if inclLen > 0 {
		if _, err := io.ReadFull(pr.r, data); err != nil {
			return Record{}, fmt.Errorf("pcap: read record data: %w", err)
		}
	}

	var ts time.Time
	if pr.nano {
		ts = time.Unix(int64(tsSec), int64(tsSubSec)).UTC()
	} else {
		ts = time.Unix(int64(tsSec), int64(tsSubSec)*1000).UTC()
	}

	return Record{Timestamp: ts, Data: data}, nil
}
