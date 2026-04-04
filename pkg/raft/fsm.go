package raft

import (
	"bytes"
	"context"
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
		if len(cmd.Batch) == 0 {
			return FSMResponse{Error: ErrInvalidCommand}
		}
		for i := range cmd.Batch {
			if cmd.Batch[i].Type == CommandBatch {
				return FSMResponse{Error: ErrInvalidCommand}
			}
			if resp := f.applyCommand(ctx, &cmd.Batch[i]); resp.Error != nil {
				return resp
			}
		}
		return FSMResponse{}

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

// Get retrieves a value from the FSM
func (f *kvFSM) Get(key string) ([]byte, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	entry, err := f.getEntry(f.ctx, key)
	if err != nil {
		return nil, false
	}
	if entry.Deleted {
		return nil, false
	}
	return entry.Value, true
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
