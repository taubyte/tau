package raft

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/hashicorp/raft"
	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
)

type CommandType uint8

const (
	CommandSet CommandType = iota + 1
	CommandDelete
	CommandBatch
	// CommandMerge applies a merged CRDT delta when a split brain heals.
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

type SetCommand struct {
	Key   string `cbor:"1,keyasint"`
	Value []byte `cbor:"2,keyasint"`
}

type DeleteCommand struct {
	Key string `cbor:"1,keyasint"`
}

type MergeCommand struct {
	Delta map[string]CRDTEntry `cbor:"1,keyasint"`
}

type Command struct {
	Type   CommandType    `cbor:"1,keyasint"`
	Set    *SetCommand    `cbor:"2,keyasint,omitempty"`
	Delete *DeleteCommand `cbor:"3,keyasint,omitempty"`
	Batch  []Command      `cbor:"4,keyasint,omitempty"`
	Merge  *MergeCommand  `cbor:"5,keyasint,omitempty"`
}

type kvFSM struct {
	ctx    context.Context
	store  ds.Batching
	prefix string
	mu     sync.RWMutex
	clock  uint64
	// applied is the index of the last log Apply was given.
	applied atomic.Uint64
}

// A log raft committed is applied whole: cancelling ctx, as Close does, stops
// raft, not an apply under way.
func newKVFSM(ctx context.Context, store ds.Batching, prefix string) FSM {
	return &kvFSM{
		ctx:    context.WithoutCancel(ctx),
		store:  store,
		prefix: prefix,
	}
}

func (f *kvFSM) dataKey(key string) ds.Key {
	return ds.NewKey(path.Join(f.prefix, "data", key))
}

func (f *kvFSM) Apply(log *raft.Log) interface{} {
	defer f.applied.Store(log.Index)

	var cmd Command
	if err := cbor.Unmarshal(log.Data, &cmd); err != nil {
		return FSMResponse{Error: ErrInvalidCommand}
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	return f.applyCommand(f.ctx, &cmd)
}

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
		return f.applyMerge(ctx, cmd.Merge.Delta)

	default:
		return FSMResponse{Error: ErrInvalidCommand}
	}
}

// applyBatch applies a batch as one store batch: raft records a failed apply
// as the future's response and never offers the entry again, so a batch that
// landed half of itself would leave a hole nothing repairs. Everything that can
// be refused is refused before anything is staged. The commit is atomic where
// the store's batch is (pebble's: one WAL record); go-datastore promises
// grouping, not transactions. Readers never see half a batch either way: Apply
// holds the write lock throughout. This buys crash atomicity, not durability:
// the store writes NoSync, so a crash can lose a whole batch, which the log
// replays.
func (f *kvFSM) applyBatch(ctx context.Context, cmds []Command) FSMResponse {
	if len(cmds) == 0 {
		return FSMResponse{Error: ErrInvalidCommand}
	}

	type write struct {
		key ds.Key
		raw []byte
	}

	// The clock advances on a copy, published once the batch lands, so a refused
	// batch leaves no trace.
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
			// A member is a set or a delete: a merge reads the entry it arbitrates
			// against, which a write-only batch cannot serve. Cluster.Apply carries
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

// applyMerge lands the winning entries of a merge in one store batch, for
// applyBatch's reason. Only an absent key loses to whatever comes in: an entry
// the store cannot read, or that will not decode, refuses the whole merge
// rather than land over an entry it never saw.
func (f *kvFSM) applyMerge(ctx context.Context, delta map[string]CRDTEntry) FSMResponse {
	clock := f.clock
	batch, err := f.store.Batch(ctx)
	if err != nil {
		return FSMResponse{Error: err}
	}
	for key, incoming := range delta {
		existing, err := f.getEntry(ctx, key)
		switch {
		case err == nil:
			if !crdtEntryWins(incoming, existing) {
				continue
			}
		case !errors.Is(err, ds.ErrNotFound):
			return FSMResponse{Error: err}
		}
		raw, err := cbor.Marshal(incoming)
		if err != nil {
			return FSMResponse{Error: err}
		}
		if err := batch.Put(ctx, f.dataKey(key), raw); err != nil {
			return FSMResponse{Error: err}
		}
		clock = max(clock, incoming.Timestamp)
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

func (f *kvFSM) Snapshot() (raft.FSMSnapshot, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	data, err := f.exportStateLocked()
	if err != nil {
		return nil, err
	}

	return &kvSnapshot{data: data, clock: f.clock}, nil
}

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

// TryGet tells absent (never written, or a tombstone) from an error (the
// store failing, or an entry that will not decode): a caller reading a failed
// read as "nothing written yet" writes over state it could not see. A corrupt
// entry is an error, not an absence, though retrying will not mend it.
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

type kvSnapshot struct {
	data  map[string]CRDTEntry
	clock uint64
}

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

func (s *kvSnapshot) Release() {}

func encodeSetCommand(key string, value []byte) ([]byte, error) {
	cmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: key, Value: value},
	}
	return cbor.Marshal(cmd)
}

func encodeDeleteCommand(key string) ([]byte, error) {
	cmd := Command{
		Type:   CommandDelete,
		Delete: &DeleteCommand{Key: key},
	}
	return cbor.Marshal(cmd)
}

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
