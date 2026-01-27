package raft

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/hashicorp/raft"
	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
)

// datastoreLogStore implements raft.LogStore using a datastore
type datastoreLogStore struct {
	store  datastore.Batching
	prefix string
	mu     sync.RWMutex
}

// newLogStore creates a new LogStore backed by the given datastore
func newLogStore(store datastore.Batching, prefix string) *datastoreLogStore {
	return &datastoreLogStore{
		store:  store,
		prefix: prefix,
	}
}

func (l *datastoreLogStore) logKey(index uint64) datastore.Key {
	return datastore.NewKey(path.Join(l.prefix, fmt.Sprintf("%020d", index)))
}

// FirstIndex returns the first index written
func (l *datastoreLogStore) FirstIndex() (uint64, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	results, err := l.store.Query(context.Background(), query.Query{
		Prefix:   l.prefix,
		KeysOnly: true,
		Orders:   []query.Order{query.OrderByKey{}},
		Limit:    1,
	})
	if err != nil {
		return 0, err
	}
	defer results.Close()

	entries, err := results.Rest()
	if err != nil {
		return 0, err
	}
	if len(entries) == 0 {
		return 0, nil
	}
	return l.parseIndex(entries[0].Key)
}

// LastIndex returns the last index written
func (l *datastoreLogStore) LastIndex() (uint64, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	results, err := l.store.Query(context.Background(), query.Query{
		Prefix:   l.prefix,
		KeysOnly: true,
	})
	if err != nil {
		return 0, err
	}
	defer results.Close()

	var maxIndex uint64
	for result := range results.Next() {
		if result.Error != nil {
			return 0, result.Error
		}
		idx, err := l.parseIndex(result.Key)
		if err != nil {
			continue
		}
		if idx > maxIndex {
			maxIndex = idx
		}
	}

	return maxIndex, nil
}

func (l *datastoreLogStore) parseIndex(key string) (uint64, error) {
	key = strings.TrimPrefix(key, l.prefix+"/")
	return strconv.ParseUint(key, 10, 64)
}

// GetLog retrieves a log entry at a given index
func (l *datastoreLogStore) GetLog(index uint64, log *raft.Log) error {
	l.mu.RLock()
	defer l.mu.RUnlock()

	data, err := l.store.Get(context.Background(), l.logKey(index))
	if err != nil {
		if err == datastore.ErrNotFound {
			return raft.ErrLogNotFound
		}
		return err
	}

	return cbor.Unmarshal(data, log)
}

// StoreLog stores a single log entry
func (l *datastoreLogStore) StoreLog(log *raft.Log) error {
	return l.StoreLogs([]*raft.Log{log})
}

// StoreLogs stores multiple log entries
func (l *datastoreLogStore) StoreLogs(logs []*raft.Log) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	batch, err := l.store.Batch(context.Background())
	if err != nil {
		return err
	}

	for _, log := range logs {
		data, err := cbor.Marshal(log)
		if err != nil {
			return err
		}
		if err := batch.Put(context.Background(), l.logKey(log.Index), data); err != nil {
			return err
		}
	}

	return batch.Commit(context.Background())
}

// DeleteRange deletes a range of log entries
func (l *datastoreLogStore) DeleteRange(min, max uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	batch, err := l.store.Batch(context.Background())
	if err != nil {
		return err
	}

	for i := min; i <= max; i++ {
		if err := batch.Delete(context.Background(), l.logKey(i)); err != nil {
			return err
		}
	}

	return batch.Commit(context.Background())
}

// datastoreStableStore implements raft.StableStore using a datastore
type datastoreStableStore struct {
	store  datastore.Batching
	prefix string
	mu     sync.RWMutex
}

// newStableStore creates a new StableStore backed by the given datastore
func newStableStore(store datastore.Batching, prefix string) *datastoreStableStore {
	return &datastoreStableStore{
		store:  store,
		prefix: prefix,
	}
}

func (s *datastoreStableStore) stableKey(key []byte) datastore.Key {
	return datastore.NewKey(path.Join(s.prefix, string(key)))
}

// Set stores a key-value pair
func (s *datastoreStableStore) Set(key []byte, val []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.store.Put(context.Background(), s.stableKey(key), val)
}

// Get retrieves a value by key
func (s *datastoreStableStore) Get(key []byte) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	val, err := s.store.Get(context.Background(), s.stableKey(key))
	if err != nil {
		if err == datastore.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return val, nil
}

// SetUint64 stores a uint64 value
func (s *datastoreStableStore) SetUint64(key []byte, val uint64) error {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, val)
	return s.Set(key, buf)
}

// GetUint64 retrieves a uint64 value
func (s *datastoreStableStore) GetUint64(key []byte) (uint64, error) {
	val, err := s.Get(key)
	if err != nil {
		return 0, err
	}
	if len(val) == 0 {
		return 0, nil
	}
	if len(val) != 8 {
		return 0, fmt.Errorf("invalid uint64 value length: %d", len(val))
	}
	return binary.BigEndian.Uint64(val), nil
}

// fileSnapshotStore implements raft.SnapshotStore using the filesystem
type fileSnapshotStore struct {
	dir    string
	retain int
	mu     sync.Mutex
}

// newSnapshotStore creates a new file-based snapshot store
func newSnapshotStore(dir string, retain int) (*fileSnapshotStore, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	return &fileSnapshotStore{
		dir:    dir,
		retain: retain,
	}, nil
}

// snapshotMeta stores metadata about a snapshot
type snapshotMeta struct {
	ID                 string               `cbor:"1,keyasint"`
	Index              uint64               `cbor:"2,keyasint"`
	Term               uint64               `cbor:"3,keyasint"`
	Configuration      raft.Configuration   `cbor:"4,keyasint"`
	ConfigurationIndex uint64               `cbor:"5,keyasint"`
	Size               int64                `cbor:"6,keyasint"`
	Version            raft.SnapshotVersion `cbor:"7,keyasint"`
}

func (f *fileSnapshotStore) snapshotDir(id string) string {
	return filepath.Join(f.dir, id)
}

// Create creates a new snapshot
func (f *fileSnapshotStore) Create(version raft.SnapshotVersion, index, term uint64,
	configuration raft.Configuration, configurationIndex uint64,
	trans raft.Transport) (raft.SnapshotSink, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	id := fmt.Sprintf("%d-%d-%d", term, index, time.Now().UnixNano())
	dir := f.snapshotDir(id)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	meta := &snapshotMeta{
		ID:                 id,
		Index:              index,
		Term:               term,
		Configuration:      configuration,
		ConfigurationIndex: configurationIndex,
		Version:            version,
	}

	return &fileSnapshotSink{
		store: f,
		dir:   dir,
		meta:  meta,
	}, nil
}

// List returns available snapshots
func (f *fileSnapshotStore) List() ([]*raft.SnapshotMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	entries, err := os.ReadDir(f.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var snapshots []*raft.SnapshotMeta
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		metaPath := filepath.Join(f.dir, entry.Name(), "meta.cbor")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}

		var meta snapshotMeta
		if err := cbor.Unmarshal(data, &meta); err != nil {
			continue
		}

		snapshots = append(snapshots, &raft.SnapshotMeta{
			ID:                 meta.ID,
			Index:              meta.Index,
			Term:               meta.Term,
			Configuration:      meta.Configuration,
			ConfigurationIndex: meta.ConfigurationIndex,
			Size:               meta.Size,
			Version:            meta.Version,
		})
	}

	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Index > snapshots[j].Index
	})

	return snapshots, nil
}

// Open opens a snapshot for reading
func (f *fileSnapshotStore) Open(id string) (*raft.SnapshotMeta, io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	dir := f.snapshotDir(id)

	metaPath := filepath.Join(dir, "meta.cbor")
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, nil, err
	}

	var meta snapshotMeta
	if err := cbor.Unmarshal(metaData, &meta); err != nil {
		return nil, nil, err
	}

	dataPath := filepath.Join(dir, "state.bin")
	file, err := os.Open(dataPath)
	if err != nil {
		return nil, nil, err
	}

	return &raft.SnapshotMeta{
		ID:                 meta.ID,
		Index:              meta.Index,
		Term:               meta.Term,
		Configuration:      meta.Configuration,
		ConfigurationIndex: meta.ConfigurationIndex,
		Size:               meta.Size,
		Version:            meta.Version,
	}, file, nil
}

// fileSnapshotSink implements raft.SnapshotSink
type fileSnapshotSink struct {
	store  *fileSnapshotStore
	dir    string
	meta   *snapshotMeta
	buf    bytes.Buffer
	closed bool
}

func (s *fileSnapshotSink) ID() string {
	return s.meta.ID
}

func (s *fileSnapshotSink) Write(p []byte) (n int, err error) {
	return s.buf.Write(p)
}

func (s *fileSnapshotSink) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true

	// Write state data
	statePath := filepath.Join(s.dir, "state.bin")
	if err := os.WriteFile(statePath, s.buf.Bytes(), 0644); err != nil {
		return err
	}

	// Update meta with size
	s.meta.Size = int64(s.buf.Len())

	// Write metadata
	metaData, err := cbor.Marshal(s.meta)
	if err != nil {
		return err
	}

	metaPath := filepath.Join(s.dir, "meta.cbor")
	if err := os.WriteFile(metaPath, metaData, 0644); err != nil {
		return err
	}

	return s.store.reap()
}

func (s *fileSnapshotSink) Cancel() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return os.RemoveAll(s.dir)
}

// reap removes old snapshots beyond the retain count
func (f *fileSnapshotStore) reap() error {
	snapshots, err := f.List()
	if err != nil {
		return err
	}

	for i := f.retain; i < len(snapshots); i++ {
		dir := f.snapshotDir(snapshots[i].ID)
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}

	return nil
}
