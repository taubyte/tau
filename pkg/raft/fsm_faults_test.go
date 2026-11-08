package raft

import (
	"bytes"
	"errors"
	"testing"

	ds "github.com/ipfs/go-datastore"
)

const faultPrefix = "/raft/test"

func applied(t *testing.T, fsm FSM, cmd Command) FSMResponse {
	t.Helper()

	data, err := encodeCommand(cmd)
	if err != nil {
		t.Fatalf("encoding the command: %v", err)
	}
	resp, ok := fsm.Apply(1, data), true
	if !ok {
		t.Fatalf("the FSM answered something other than an FSMResponse")
	}
	return resp
}

func TestAStoreErrorDoesNotReadAsAnAbsentKey(t *testing.T) {
	store := newFaultStore()
	fsm := newKVFSM(t.Context(), store, faultPrefix)

	if resp := applied(t, fsm, Command{Type: CommandSet, Set: &SetCommand{Key: "head", Value: []byte("5")}}); resp.Error != nil {
		t.Fatalf("writing the key the store will later refuse to read: %v", resp.Error)
	}

	disk := errors.New("pebble error during get: input/output error")
	store.failGet(faultPrefix+"/data/head", disk)

	value, found, err := fsm.TryGet("head")
	if err == nil {
		t.Fatalf("a failed read answered found=%v, value=%q and no error", found, value)
	}
	if !errors.Is(err, disk) {
		t.Fatalf("the error lost the store's: %v", err)
	}
	if found {
		t.Fatalf("a failed read answered found")
	}
}

func TestAKeyNobodyWroteIsAbsentAndNotAnError(t *testing.T) {
	fsm := newKVFSM(t.Context(), newFaultStore(), faultPrefix)

	value, found, err := fsm.TryGet("head")
	if err != nil {
		t.Fatalf("a key nobody wrote answered an error: %v", err)
	}
	if found || value != nil {
		t.Fatalf("a key nobody wrote answered found=%v, value=%q", found, value)
	}

	if resp := applied(t, fsm, Command{Type: CommandDelete, Delete: &DeleteCommand{Key: "gone"}}); resp.Error != nil {
		t.Fatalf("writing the tombstone: %v", resp.Error)
	}
	if _, found, err = fsm.TryGet("gone"); err != nil || found {
		t.Fatalf("a tombstone answered found=%v, err=%v; it is an absence, not a failure", found, err)
	}
}

func TestAnEntryThatWillNotDecodeIsAnErrorAndNotAnAbsence(t *testing.T) {
	store := newFaultStore()
	fsm := newKVFSM(t.Context(), store, faultPrefix)

	if err := store.Put(t.Context(), ds.NewKey(faultPrefix+"/data/head"), []byte("not an entry")); err != nil {
		t.Fatalf("planting the corrupt entry: %v", err)
	}

	if _, found, err := fsm.TryGet("head"); err == nil || found {
		t.Fatalf("a corrupt entry answered found=%v, err=%v", found, err)
	}
}

// No store has to fail: the third member is refused after the first two were
// staged.
func TestABatchWithAMalformedMemberWritesNoneOfIt(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(t.Context(), store, faultPrefix)

	resp := applied(t, fsm, Command{
		Type: CommandBatch,
		Batch: []Command{
			{Type: CommandSet, Set: &SetCommand{Key: "a", Value: []byte("1")}},
			{Type: CommandSet, Set: &SetCommand{Key: "b", Value: []byte("2")}},
			{Type: CommandSet},
		},
	})
	if !errors.Is(resp.Error, ErrInvalidCommand) {
		t.Fatalf("a batch with a malformed member answered %v", resp.Error)
	}

	for _, key := range []string{"a", "b"} {
		if !absent(t, store, faultPrefix, key) {
			t.Fatalf("%q survived a batch that was refused; the batch applied part of itself", key)
		}
	}
}

func TestABatchThatCannotCommitWritesNoneOfIt(t *testing.T) {
	store := newFaultStore()
	fsm := newKVFSM(t.Context(), store, faultPrefix)

	disk := errors.New("pebble error during set: no space left on device")
	store.failPut(faultPrefix+"/data/b", disk)

	resp := applied(t, fsm, Command{
		Type: CommandBatch,
		Batch: []Command{
			{Type: CommandSet, Set: &SetCommand{Key: "a", Value: []byte("1")}},
			{Type: CommandSet, Set: &SetCommand{Key: "b", Value: []byte("2")}},
		},
	})
	if !errors.Is(resp.Error, disk) {
		t.Fatalf("a batch the store refused answered %v", resp.Error)
	}

	if !absent(t, store, faultPrefix, "a") {
		t.Fatalf("%q survived a batch that never committed; the batch applied part of itself", "a")
	}
}

// The Lamport clock is state too: the healing merge arbitrates on timestamps.
func TestARefusedBatchDoesNotSpendTheClock(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(t.Context(), store, faultPrefix)

	resp := applied(t, fsm, Command{
		Type: CommandBatch,
		Batch: []Command{
			{Type: CommandSet, Set: &SetCommand{Key: "a", Value: []byte("1")}},
			{Type: CommandSet, Set: &SetCommand{Key: "b", Value: []byte("2")}},
			{Type: CommandDelete},
		},
	})
	if !errors.Is(resp.Error, ErrInvalidCommand) {
		t.Fatalf("a batch with a malformed member answered %v", resp.Error)
	}

	if resp := applied(t, fsm, Command{Type: CommandSet, Set: &SetCommand{Key: "z", Value: []byte("3")}}); resp.Error != nil {
		t.Fatalf("writing after the refused batch: %v", resp.Error)
	}

	raw, err := store.Get(t.Context(), ds.NewKey(faultPrefix+"/data/z"))
	if err != nil {
		t.Fatalf("reading back the entry: %v", err)
	}
	entry, err := decodeFSMEntry(raw)
	if err != nil {
		t.Fatalf("decoding the entry: %v", err)
	}
	if entry.Timestamp != 1 {
		t.Fatalf("the first entry after a refused batch carries timestamp %d; the refused batch spent %d", entry.Timestamp, entry.Timestamp-1)
	}
	if !bytes.Equal(entry.Value, []byte("3")) {
		t.Fatalf("the entry carries %q", entry.Value)
	}
}

func TestAMergeIsNotABatchMember(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(t.Context(), store, faultPrefix)

	resp := applied(t, fsm, Command{
		Type: CommandBatch,
		Batch: []Command{
			{Type: CommandSet, Set: &SetCommand{Key: "a", Value: []byte("1")}},
			{Type: CommandMerge, Merge: &MergeCommand{Delta: map[string]CRDTEntry{
				"b": {Value: []byte("2"), Timestamp: 9},
			}}},
		},
	})
	if !errors.Is(resp.Error, ErrInvalidCommand) {
		t.Fatalf("a merge inside a batch answered %v", resp.Error)
	}

	for _, key := range []string{"a", "b"} {
		if !absent(t, store, faultPrefix, key) {
			t.Fatalf("%q survived a batch that was refused", key)
		}
	}
}
