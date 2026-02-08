package raft

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path"
	"strings"
	"sync"

	"github.com/fxamacker/cbor/v2"
	"github.com/hashicorp/raft"
	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
)

// CommandType represents the type of FSM command
type CommandType uint8

const (
	// CommandSet stores a key-value pair
	CommandSet CommandType = iota + 1
	// CommandDelete removes a key
	CommandDelete
	// CommandEnqueue adds an item to a queue
	CommandEnqueue
	// CommandDequeue removes and returns the next item from a queue (pop)
	CommandDequeue
)

// SetCommand represents a set operation
type SetCommand struct {
	Key   string `cbor:"1,keyasint"`
	Value []byte `cbor:"2,keyasint"`
}

// DeleteCommand represents a delete operation
type DeleteCommand struct {
	Key string `cbor:"1,keyasint"`
}

// EnqueueCommand represents a queue enqueue
type EnqueueCommand struct {
	QueuePrefix string `cbor:"1,keyasint"`
	ID          string `cbor:"2,keyasint"`
	Data        []byte `cbor:"3,keyasint"`
}

// DequeueCommand represents a queue dequeue (pop: remove and return next item)
type DequeueCommand struct {
	QueuePrefix string `cbor:"1,keyasint"`
}

// Command is the structure replicated via Raft
type Command struct {
	Type    CommandType     `cbor:"1,keyasint"`
	Set     *SetCommand     `cbor:"2,keyasint,omitempty"`
	Delete  *DeleteCommand  `cbor:"3,keyasint,omitempty"`
	Enqueue *EnqueueCommand `cbor:"4,keyasint,omitempty"`
	Dequeue *DequeueCommand `cbor:"5,keyasint,omitempty"`
}

// kvFSM implements the FSM interface using the node's datastore
type kvFSM struct {
	store  datastore.Batching
	prefix string
	mu     sync.RWMutex
}

// newKVFSM creates a new key-value FSM using the given datastore
func newKVFSM(store datastore.Batching, prefix string) FSM {
	return &kvFSM{
		store:  store,
		prefix: prefix,
	}
}

// dataKey returns the datastore key for a given user key
func (f *kvFSM) dataKey(key string) datastore.Key {
	return datastore.NewKey(path.Join(f.prefix, "data", key))
}

// queueItemKey returns the datastore key for a queue item
func (f *kvFSM) queueItemKey(queuePrefix, id string) datastore.Key {
	return f.dataKey(path.Join(queuePrefix, "item", id))
}

// queuePendingKey returns the datastore key for the pending list
func (f *kvFSM) queuePendingKey(queuePrefix string) datastore.Key {
	return f.dataKey(path.Join(queuePrefix, "pending"))
}

// Apply implements raft.FSM.Apply
func (f *kvFSM) Apply(log *raft.Log) interface{} {
	var cmd Command
	if err := cbor.Unmarshal(log.Data, &cmd); err != nil {
		return FSMResponse{Error: ErrInvalidCommand}
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	ctx := context.Background()

	switch cmd.Type {
	case CommandSet:
		if cmd.Set == nil {
			return FSMResponse{Error: ErrInvalidCommand}
		}
		err := f.store.Put(ctx, f.dataKey(cmd.Set.Key), cmd.Set.Value)
		return FSMResponse{Error: err}

	case CommandDelete:
		if cmd.Delete == nil {
			return FSMResponse{Error: ErrInvalidCommand}
		}
		err := f.store.Delete(ctx, f.dataKey(cmd.Delete.Key))
		return FSMResponse{Error: err}

	case CommandEnqueue:
		if cmd.Enqueue == nil {
			return FSMResponse{Error: ErrInvalidCommand}
		}
		return f.applyEnqueue(ctx, cmd.Enqueue)

	case CommandDequeue:
		if cmd.Dequeue == nil {
			return FSMResponse{Error: ErrInvalidCommand}
		}
		return f.applyDequeue(ctx, cmd.Dequeue)

	default:
		return FSMResponse{Error: ErrInvalidCommand}
	}
}

// queueList reads a stored list of ids (pending or inflight)
func (f *kvFSM) queueList(ctx context.Context, key datastore.Key) ([]string, error) {
	val, err := f.store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, datastore.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var list []string
	if len(val) == 0 {
		return nil, nil
	}
	if err := cbor.Unmarshal(val, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// queueListWrite writes a list of ids
func (f *kvFSM) queueListWrite(ctx context.Context, key datastore.Key, list []string) error {
	if list == nil {
		list = []string{}
	}
	data, err := cbor.Marshal(list)
	if err != nil {
		return err
	}
	return f.store.Put(ctx, key, data)
}

func (f *kvFSM) applyEnqueue(ctx context.Context, c *EnqueueCommand) FSMResponse {
	pre := c.QueuePrefix
	if err := f.store.Put(ctx, f.queueItemKey(pre, c.ID), c.Data); err != nil {
		return FSMResponse{Error: err}
	}
	pendingKey := f.queuePendingKey(pre)
	list, err := f.queueList(ctx, pendingKey)
	if err != nil {
		return FSMResponse{Error: err}
	}
	list = append(list, c.ID)
	if err := f.queueListWrite(ctx, pendingKey, list); err != nil {
		return FSMResponse{Error: err}
	}
	return FSMResponse{}
}

// dequeueResponse is the payload returned in FSMResponse.Data for a dequeue
type dequeueResponse struct {
	ID   string `cbor:"1,keyasint"`
	Data []byte `cbor:"2,keyasint"`
}

func (f *kvFSM) applyDequeue(ctx context.Context, c *DequeueCommand) FSMResponse {
	pre := c.QueuePrefix
	pendingKey := f.queuePendingKey(pre)
	list, err := f.queueList(ctx, pendingKey)
	if err != nil {
		return FSMResponse{Error: err}
	}
	if len(list) == 0 {
		return FSMResponse{Data: nil}
	}
	id := list[0]
	list = list[1:]
	if err := f.queueListWrite(ctx, pendingKey, list); err != nil {
		return FSMResponse{Error: err}
	}
	itemKey := f.queueItemKey(pre, id)
	data, err := f.store.Get(ctx, itemKey)
	if err != nil {
		return FSMResponse{Error: err}
	}
	if err := f.store.Delete(ctx, itemKey); err != nil {
		return FSMResponse{Error: err}
	}
	resp := dequeueResponse{ID: id, Data: data}
	dataEnc, err := cbor.Marshal(resp)
	if err != nil {
		return FSMResponse{Error: err}
	}
	return FSMResponse{Data: dataEnc}
}

// Snapshot implements raft.FSM.Snapshot
func (f *kvFSM) Snapshot() (raft.FSMSnapshot, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	prefix := path.Join(f.prefix, "data")
	results, err := f.store.Query(context.Background(), query.Query{
		Prefix: prefix,
	})
	if err != nil {
		return nil, err
	}
	defer results.Close()

	trimPrefix := prefix + "/"
	data := make(map[string][]byte)
	for result := range results.Next() {
		if result.Error != nil {
			return nil, result.Error
		}
		key := strings.TrimPrefix(result.Key, trimPrefix)
		data[key] = result.Value
	}

	return &kvSnapshot{data: data}, nil
}

// Restore implements FSM.Restore
func (f *kvFSM) Restore(snapshot io.ReadCloser) error {
	defer snapshot.Close()

	data, err := io.ReadAll(snapshot)
	if err != nil {
		return err
	}

	var restored map[string][]byte
	if err := cbor.Unmarshal(data, &restored); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	ctx := context.Background()

	// Clear existing data
	prefix := path.Join(f.prefix, "data")
	results, err := f.store.Query(ctx, query.Query{
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
		if err := f.store.Delete(ctx, datastore.NewKey(result.Key)); err != nil {
			results.Close()
			return err
		}
	}
	results.Close()

	for key, value := range restored {
		if err := f.store.Put(ctx, f.dataKey(key), value); err != nil {
			return err
		}
	}

	return nil
}

// Get retrieves a value from the FSM
func (f *kvFSM) Get(key string) ([]byte, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	val, err := f.store.Get(context.Background(), f.dataKey(key))
	if err != nil {
		return nil, false
	}
	return val, true
}

// Keys returns all keys matching a prefix
func (f *kvFSM) Keys(prefix string) []string {
	f.mu.RLock()
	defer f.mu.RUnlock()

	dataPrefix := path.Join(f.prefix, "data")
	results, err := f.store.Query(context.Background(), query.Query{
		Prefix:   dataPrefix,
		KeysOnly: true,
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
		key := strings.TrimPrefix(result.Key, trimPrefix)
		if prefix == "" || strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	return keys
}

// kvSnapshot implements raft.FSMSnapshot
type kvSnapshot struct {
	data map[string][]byte
}

// Persist implements raft.FSMSnapshot.Persist
func (s *kvSnapshot) Persist(sink raft.SnapshotSink) error {
	data, err := cbor.Marshal(s.data)
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

// encodeEnqueueCommand encodes an enqueue command
func encodeEnqueueCommand(queuePrefix, id string, data []byte) ([]byte, error) {
	cmd := Command{
		Type: CommandEnqueue,
		Enqueue: &EnqueueCommand{
			QueuePrefix: queuePrefix,
			ID:          id,
			Data:        data,
		},
	}
	return cbor.Marshal(cmd)
}

// encodeDequeueCommand encodes a dequeue command
func encodeDequeueCommand(queuePrefix string) ([]byte, error) {
	cmd := Command{
		Type:    CommandDequeue,
		Dequeue: &DequeueCommand{QueuePrefix: queuePrefix},
	}
	return cbor.Marshal(cmd)
}
