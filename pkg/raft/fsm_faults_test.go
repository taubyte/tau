package raft

import (
	"bytes"
	"errors"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/hashicorp/raft"
	ds "github.com/ipfs/go-datastore"
)

const faultPrefix = "/raft/test"

func applied(t *testing.T, fsm FSM, cmd Command) FSMResponse {
	t.Helper()

	data, err := cbor.Marshal(cmd)
	if err != nil {
		t.Fatalf("encoding the command: %v", err)
	}
	resp, ok := fsm.Apply(&raft.Log{Data: data, Index: 1}).(FSMResponse)
	if !ok {
		t.Fatalf("the FSM answered something other than an FSMResponse")
	}
	return resp
}

// TestAStoreErrorDoesNotReadAsAnAbsentKey. A read that could not reach the
// store says so, because a caller that allocates from an absent key — the
// authority reading its head is the one that does — allocates over state it
// could not see.
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

// TestAKeyNobodyWroteIsAbsentAndNotAnError. The other half of the same split:
// an absent head is a database nobody has written to, and it has to stay
// distinguishable from one that could not be read.
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

// TestAnEntryThatWillNotDecodeIsAnErrorAndNotAnAbsence. A key whose bytes are
// not an entry is corruption, and corruption reported as "nothing here" is a
// key the next writer will overwrite without ever knowing it was occupied.
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

// TestABatchWithAMalformedMemberWritesNoneOfIt. The cheapest way to see a
// half-applied batch: no store has to fail, only the third member has to be
// refused after the first two were already durable.
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

// TestABatchThatCannotCommitWritesNoneOfIt. The same guarantee against the
// store failing rather than the command being malformed — the case the
// authority's head and its ops depend on, since a head that moved without its
// ops is a hole nothing repairs.
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

// TestARefusedBatchDoesNotSpendTheClock. A batch that wrote nothing must leave
// nothing, and the Lamport clock is state too: timestamps that no entry
// carries are what the healing merge arbitrates on.
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
	var entry CRDTEntry
	if err := cbor.Unmarshal(raw, &entry); err != nil {
		t.Fatalf("decoding the entry: %v", err)
	}
	if entry.Timestamp != 1 {
		t.Fatalf("the first entry after a refused batch carries timestamp %d; the refused batch spent %d", entry.Timestamp, entry.Timestamp-1)
	}
	if !bytes.Equal(entry.Value, []byte("3")) {
		t.Fatalf("the entry carries %q", entry.Value)
	}
}

// TestAMergeIsNotABatchMember. A merge arbitrates against the entry it is
// replacing, and the open store batch cannot serve that read — a go-datastore
// batch is write-only and cannot see its own staged writes. Cluster.Batch
// builds no merge, but Cluster.Apply carries whatever a caller encoded.
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
