// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package bbs

import (
	"errors"
	"testing"

	"github.com/n7get/go-ax25/cmd/bbs/store"
)

type mockMessageStore struct {
	findResult    *store.MessageIndex
	findErr       error
	deleteErr     error
	readResult    *store.Message
	readErr       error
	lastRead      uint32
	setLastReadID uint32
	deleteCalls   int
	deletedID     uint32
}

func (m *mockMessageStore) Close() error { return nil }

func (m *mockMessageStore) Store(from, to, subject, body string, flags uint8) (uint32, error) {
	return 0, errors.New("not implemented")
}

func (m *mockMessageStore) Read(id uint32) (*store.Message, error) {
	if m.readErr != nil {
		return nil, m.readErr
	}
	return m.readResult, nil
}

func (m *mockMessageStore) Delete(id uint32) error {
	m.deleteCalls++
	m.deletedID = id
	return m.deleteErr
}

func (m *mockMessageStore) Find(id uint32) (*store.MessageIndex, error) {
	if m.findErr != nil {
		return nil, m.findErr
	}
	return m.findResult, nil
}

func (m *mockMessageStore) List(limit int, mineOnly bool, callsign string, isSysop bool) ([]store.MessageIndex, error) {
	return nil, errors.New("not implemented")
}

func (m *mockMessageStore) GetLastRead(callsign string) (uint32, error) {
	return m.lastRead, nil
}

func (m *mockMessageStore) SetLastRead(callsign string, id uint32) error {
	m.setLastReadID = id
	return nil
}

func TestCmdKill_AllowsRecipient(t *testing.T) {
	st := &mockMessageStore{
		findResult: &store.MessageIndex{ID: 7, From: "ALICE", To: "N7GET"},
	}
	m := &SessionManager{store: st}
	sess := &Session{BaseCall: "N7GET"}

	m.cmdKill(sess, "7")

	if st.deleteCalls != 1 {
		t.Fatalf("expected delete to be called once, got %d", st.deleteCalls)
	}
	if st.deletedID != 7 {
		t.Fatalf("expected delete id 7, got %d", st.deletedID)
	}
}

func TestCmdKill_DeniesUnrelatedNonSysop(t *testing.T) {
	st := &mockMessageStore{
		findResult: &store.MessageIndex{ID: 9, From: "ALICE", To: "BOB"},
	}
	m := &SessionManager{store: st}
	sess := &Session{BaseCall: "N7GET", IsSysop: false}

	m.cmdKill(sess, "9")

	if st.deleteCalls != 0 {
		t.Fatalf("expected delete not to be called, got %d", st.deleteCalls)
	}
}

func TestCmdRead_PrivateMessageUpdatesLastReadForRecipient(t *testing.T) {
	st := &mockMessageStore{
		readResult: &store.Message{
			MessageIndex: store.MessageIndex{ID: 11, From: "ALICE", To: "N7GET", Flags: FlagPrivate},
			Body:         "test",
		},
		lastRead: 2,
	}
	m := &SessionManager{store: st}
	sess := &Session{BaseCall: "N7GET"}

	m.cmdRead(sess, "11")

	if st.setLastReadID != 11 {
		t.Fatalf("expected last read to be updated to 11, got %d", st.setLastReadID)
	}
}

func TestCmdRead_PrivateMessageDeniedForUnrelatedUser(t *testing.T) {
	st := &mockMessageStore{
		readResult: &store.Message{
			MessageIndex: store.MessageIndex{ID: 12, From: "ALICE", To: "BOB", Flags: FlagPrivate},
			Body:         "test",
		},
		lastRead: 4,
	}
	m := &SessionManager{store: st}
	sess := &Session{BaseCall: "N7GET"}

	m.cmdRead(sess, "12")

	if st.setLastReadID != 0 {
		t.Fatalf("expected last read to remain unchanged, got %d", st.setLastReadID)
	}
	if st.deleteCalls != 0 {
		t.Fatalf("expected no delete calls, got %d", st.deleteCalls)
	}
}
