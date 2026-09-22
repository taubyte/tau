package raft

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/hashicorp/raft"
	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
)

// CommandType represents the type of FSM command
type CommandType uint8

const (
	// CommandSet stores a key-value pair
	CommandSet CommandType = iota + 1
	// CommandDelete removes a key
	CommandDelete
	// CommandBatch atomically applies multiple Set/Delete commands in a single Raft log entry
	CommandBatch
	// CommandMerge applies a merged CRDT delta (split-brain healing)
	CommandMerge
)

// CRDTEntry is LWW with Lamport Timestamp; WallClock breaks ties on equal Timestamp.
type CRDTEntry struct {
	Value     []byte `cbor:"1,keyasint"`
	Timestamp uint64 `cbor:"2,keyasint"`
	WallClock int64  `cbor:"3,keyasint"`
	Deleted   bool   `cbor:"4,keyasint"`
}

func crdtEntryWins(a, b CRDTEntry) bool {
	if a.Timestamp != b.Timestamp {
		return a.Timestamp > b.Timestamp
	}
	return a.WallClock > b.WallClock
}

// SetCommand represents a set operation
type SetCommand struct {
	Key   string `cbor:"1,keyasint"`
	Value []byte `cbor:"2,keyasint"`
}

// DeleteCommand represents a delete operation
type DeleteCommand struct {
	Key string `cbor:"1,keyasint"`
}

// MergeCommand is the payload for CommandMerge
type MergeCommand struct {
	Delta map[string]CRDTEntry `cbor:"1,keyasint"`
}

// Command is the structure replicated via Raft
type Command struct {
	Type   CommandType    `cbor:"1,keyasint"`
	Set    *SetCommand    `cbor:"2,keyasint,omitempty"`
	Delete *DeleteCommand `cbor:"3,keyasint,omitempty"`
	Batch  []Command      `cbor:"4,keyasint,omitempty"`
	Merge  *MergeCommand  `cbor:"5,keyasint,omitempty"`
}

// kvFSM implements the FSM interface using the node's datastore
type kvFSM struct {
	ctx    context.Context
	store  ds.Batching
	prefix string
	mu     sync.RWMutex
	clock  uint64
}

// newKVFSM creates a new key-value FSM using the given datastore
func newKVFSM(ctx context.Context, store ds.Batching, prefix string) FSM {
	return &kvFSM{
		ctx:    ctx,
		store:  store,
		prefix: prefix,
	}
}

// dataKey returns the datastore key for a given user key
func (f *kvFSM) dataKey(key string) ds.Key {
	return ds.NewKey(path.Join(f.prefix, "data", key))
}

// Apply implements raft.FSM.Apply — returns interface{} per the raft.FSM contract.
func (f *kvFSM) Apply(log *raft.Log) interface{} {
	var cmd Command
	if err := cbor.Unmarshal(log.Data, &cmd); err != nil {
		return FSMResponse{Error: ErrInvalidCommand}
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	return f.applyCommand(f.ctx, &cmd)
}

// applyCommand dispatches a single command (including batch).
func (f *kvFSM) applyCommand(ctx context.Context, cmd *Command) FSMResponse {
	switch cmd.Type {
	case CommandSet:
		if cmd.Set == nil {
			return FSMResponse{Error: ErrInvalidCommand}
		}
		f.clock++
		entry := CRDTEntry{
			Value:     cmd.Set.Value,
			Timestamp: f.clock,
			WallClock: time.Now().UnixNano(),
			Deleted:   false,
		}
		raw, err := cbor.Marshal(entry)
		if err != nil {
			return FSMResponse{Error: err}
		}
		err = f.store.Put(ctx, f.dataKey(cmd.Set.Key), raw)
		return FSMResponse{Error: err}

	case CommandDelete:
		if cmd.Delete == nil {
			return FSMResponse{Error: ErrInvalidCommand}
		}
		f.clock++
		entry := CRDTEntry{
			Timestamp: f.clock,
			WallClock: time.Now().UnixNano(),
			Deleted:   true,
		}
		raw, err := cbor.Marshal(entry)
		if err != nil {
			return FSMResponse{Error: err}
		}
		err = f.store.Put(ctx, f.dataKey(cmd.Delete.Key), raw)
		return FSMResponse{Error: err}

	case CommandBatch:
		return f.applyBatch(ctx, cmd.Batch)

	case CommandMerge:
		if cmd.Merge == nil || len(cmd.Merge.Delta) == 0 {
			return FSMResponse{Error: ErrInvalidCommand}
		}
		for key, incoming := range cmd.Merge.Delta {
			existing, err := f.getEntry(ctx, key)
			if err == nil && !crdtEntryWins(incoming, existing) {
				continue
			}
			raw, err := cbor.Marshal(incoming)
			if err != nil {
				return FSMResponse{Error: err}
			}
			if err := f.store.Put(ctx, f.dataKey(key), raw); err != nil {
				return FSMResponse{Error: err}
			}
			if incoming.Timestamp > f.clock {
				f.clock = incoming.Timestamp
			}
		}
		return FSMResponse{}

	default:
		return FSMResponse{Error: ErrInvalidCommand}
	}
}

// applyBatch applies a batch as ONE STORE BATCH, so that a batch which fails
// anywhere leaves nothing behind.
//
// A BATCH THAT WROTE HALF OF ITSELF IS WORSE THAN A BATCH THAT WROTE NOTHING.
// Its callers pair keys that only mean anything together — an op and the head
// that covers it, an item and the queue position it sits at — and a member
// that landed without its siblings is a hole nothing repairs: raft records the
// error as the future's response, advances lastApplied regardless, and never
// offers the entry again.
//
// WHAT IS GUARANTEED AND WHAT IS NOT, precisely, because the comment this
// replaces claimed more than the code delivered. Everything that can be
// refused is refused before anything is written: every member is validated and
// every entry encoded first, so what is left once the store batch is open is
// staging the writes and committing them — and a staging Put that fails
// returns without committing, which abandons the whole batch rather than
// leaving part of it. The commit is atomic wherever the store's Batch is:
// pebble's, which is what a node runs on, applies as one WAL record under one
// sequence number. go-datastore's Batching interface promises grouping and
// explicitly NOT transactions, so a datastore whose batch replays operations
// one at a time can still apply part of this one; atomicity comes from the
// implementation, never from the interface.
//
// WHAT IT BUYS IS CRASH ATOMICITY AND NOT READER ATOMICITY. No reader in this
// process ever saw half a batch, before this change or after: Apply holds the
// state machine's write lock for the whole of it and every reader takes the
// read lock. What one store batch adds is that a machine which dies mid-apply
// cannot come back holding half. Durability is a separate question with a
// separate answer: the store is opened with pebble's default NoSync, so a
// machine that dies can lose a committed batch ENTIRELY and the log replays
// it — what it cannot do is lose half.
func (f *kvFSM) applyBatch(ctx context.Context, cmds []Command) FSMResponse {
	if len(cmds) == 0 {
		return FSMResponse{Error: ErrInvalidCommand}
	}

	type write struct {
		key ds.Key
		raw []byte
	}

	// The clock advances on a copy and is published only once the batch has
	// landed, so a refused batch leaves no trace at all — not even Lamport
	// timestamps that no entry carries.
	clock := f.clock
	writes := make([]write, 0, len(cmds))

	for i := range cmds {
		var key string
		entry := CRDTEntry{WallClock: time.Now().UnixNano()}

		switch cmds[i].Type {
		case CommandSet:
			if cmds[i].Set == nil {
				return FSMResponse{Error: ErrInvalidCommand}
			}
			key, entry.Value = cmds[i].Set.Key, cmds[i].Set.Value

		case CommandDelete:
			if cmds[i].Delete == nil {
				return FSMResponse{Error: ErrInvalidCommand}
			}
			key, entry.Deleted = cmds[i].Delete.Key, true

		default:
			// A MEMBER IS A SET OR A DELETE AND NOTHING ELSE. A nested batch
			// says nothing a flat one does not, and a merge reads the entry it
			// arbitrates against — a read the open batch cannot serve, because
			// a go-datastore batch is write-only and cannot see its own staged
			// writes. Cluster.Batch builds neither, but Cluster.Apply carries
			// whatever a caller encoded.
			return FSMResponse{Error: ErrInvalidCommand}
		}

		clock++
		entry.Timestamp = clock

		raw, err := cbor.Marshal(entry)
		if err != nil {
			return FSMResponse{Error: err}
		}
		writes = append(writes, write{key: f.dataKey(key), raw: raw})
	}

	batch, err := f.store.Batch(ctx)
	if err != nil {
		return FSMResponse{Error: err}
	}
	for _, w := range writes {
		if err := batch.Put(ctx, w.key, w.raw); err != nil {
			return FSMResponse{Error: err}
		}
	}
	if err := batch.Commit(ctx); err != nil {
		return FSMResponse{Error: err}
	}

	f.clock = clock

	return FSMResponse{}
}

func (f *kvFSM) getEntry(ctx context.Context, key string) (CRDTEntry, error) {
	raw, err := f.store.Get(ctx, f.dataKey(key))
	if err != nil {
		return CRDTEntry{}, err
	}
	var entry CRDTEntry
	if err := cbor.Unmarshal(raw, &entry); err != nil {
		return CRDTEntry{}, err
	}
	return entry, nil
}

// Snapshot implements raft.FSM.Snapshot
func (f *kvFSM) Snapshot() (raft.FSMSnapshot, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	data, err := f.exportStateLocked()
	if err != nil {
		return nil, err
	}

	return &kvSnapshot{data: data, clock: f.clock}, nil
}

// Restore implements FSM.Restore
func (f *kvFSM) Restore(snapshot io.ReadCloser) error {
	defer snapshot.Close()

	raw, err := io.ReadAll(snapshot)
	if err != nil {
		return err
	}

	var snap snapshotPayload
	if err := cbor.Unmarshal(raw, &snap); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	prefix := path.Join(f.prefix, "data")
	results, err := f.store.Query(f.ctx, query.Query{
		Prefix:   prefix,
		KeysOnly: true,
	})
	if err != nil {
		return err
	}

	for result := range results.Next() {
		if result.Error != nil {
			results.Close()
			return result.Error
		}
		if err := f.store.Delete(f.ctx, ds.NewKey(result.Key)); err != nil {
			results.Close()
			return err
		}
	}
	results.Close()

	for key, entry := range snap.Data {
		raw, err := cbor.Marshal(entry)
		if err != nil {
			return err
		}
		if err := f.store.Put(f.ctx, f.dataKey(key), raw); err != nil {
			return err
		}
	}

	f.clock = snap.Clock

	return nil
}

// TryGet retrieves a value and says why it is not there when it is not.
//
// A STORE THAT FAILED IS NOT A KEY THAT IS ABSENT. Get has nowhere to put the
// difference, so it answers false for both, and a caller that reads a failed
// read as "nothing written yet" then writes over state it could not see. Every
// caller that has an error to return should call this one instead.
//
// Three answers, not two: the value; absent, which is a key never written or a
// tombstone; and an error, which is the store failing or an entry that will
// not decode. The second of those is permanent — retrying will not mend a
// corrupt entry — and it is still an error rather than an absence, because a
// key whose bytes are unreadable is not a key nobody wrote.
func (f *kvFSM) TryGet(key string) ([]byte, bool, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	entry, err := f.getEntry(f.ctx, key)
	if err != nil {
		if errors.Is(err, ds.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if entry.Deleted {
		return nil, false, nil
	}
	return entry.Value, true, nil
}

// Get retrieves a value from the FSM. It cannot tell an absent key from a
// store that failed — both answer false; TryGet is the one that can.
func (f *kvFSM) Get(key string) ([]byte, bool) {
	value, found, _ := f.TryGet(key)
	return value, found
}

// Keys returns all keys matching a prefix
func (f *kvFSM) Keys(prefix string) []string {
	f.mu.RLock()
	defer f.mu.RUnlock()

	dataPrefix := path.Join(f.prefix, "data")
	results, err := f.store.Query(f.ctx, query.Query{
		Prefix: dataPrefix,
	})
	if err != nil {
		return nil
	}
	defer results.Close()

	trimPrefix := dataPrefix + "/"
	var keys []string
	for result := range results.Next() {
		if result.Error != nil {
			break
		}
		var entry CRDTEntry
		if err := cbor.Unmarshal(result.Value, &entry); err != nil {
			continue
		}
		if entry.Deleted {
			continue
		}
		key := strings.TrimPrefix(result.Key, trimPrefix)
		if prefix == "" || strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	return keys
}

// ExportState returns all entries (including tombstones) and is used for healing merge.
func (f *kvFSM) ExportState() (map[string]CRDTEntry, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.exportStateLocked()
}

func (f *kvFSM) exportStateLocked() (map[string]CRDTEntry, error) {
	prefix := path.Join(f.prefix, "data")
	results, err := f.store.Query(f.ctx, query.Query{
		Prefix: prefix,
	})
	if err != nil {
		return nil, err
	}
	defer results.Close()

	trimPrefix := prefix + "/"
	data := make(map[string]CRDTEntry)
	for result := range results.Next() {
		if result.Error != nil {
			return nil, result.Error
		}
		var entry CRDTEntry
		if err := cbor.Unmarshal(result.Value, &entry); err != nil {
			continue
		}
		key := strings.TrimPrefix(result.Key, trimPrefix)
		data[key] = entry
	}
	return data, nil
}

type snapshotPayload struct {
	Data  map[string]CRDTEntry `cbor:"1,keyasint"`
	Clock uint64               `cbor:"2,keyasint"`
}

// kvSnapshot implements raft.FSMSnapshot
type kvSnapshot struct {
	data  map[string]CRDTEntry
	clock uint64
}

// Persist implements raft.FSMSnapshot.Persist
func (s *kvSnapshot) Persist(sink raft.SnapshotSink) error {
	payload := snapshotPayload{Data: s.data, Clock: s.clock}
	data, err := cbor.Marshal(payload)
	if err != nil {
		sink.Cancel()
		return err
	}

	if _, err := io.Copy(sink, bytes.NewReader(data)); err != nil {
		sink.Cancel()
		return err
	}

	return sink.Close()
}

// Release implements raft.FSMSnapshot.Release
func (s *kvSnapshot) Release() {}

// encodeSetCommand encodes a set command
func encodeSetCommand(key string, value []byte) ([]byte, error) {
	cmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: key, Value: value},
	}
	return cbor.Marshal(cmd)
}

// encodeDeleteCommand encodes a delete command
func encodeDeleteCommand(key string) ([]byte, error) {
	cmd := Command{
		Type:   CommandDelete,
		Delete: &DeleteCommand{Key: key},
	}
	return cbor.Marshal(cmd)
}

// encodeBatchCommand encodes a batch of commands into a single Raft entry
func encodeBatchCommand(cmds []Command) ([]byte, error) {
	cmd := Command{
		Type:  CommandBatch,
		Batch: cmds,
	}
	return cbor.Marshal(cmd)
}

func encodeMergeCommand(delta map[string]CRDTEntry) ([]byte, error) {
	cmd := Command{
		Type:  CommandMerge,
		Merge: &MergeCommand{Delta: delta},
	}
	return cbor.Marshal(cmd)
}
