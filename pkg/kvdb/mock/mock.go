package mock

import (
	"context"
	"errors"
	"regexp"
	"sync"

	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/core/kvdb"
)

// Error constants
var (
	ErrClosed   = errors.New("kvdb is closed")
	ErrNotFound = errors.New("key not found")
)

// KVDB implements the KVDB interface using an in-memory map
type KVDB struct {
	data    map[string][]byte
	mutex   sync.RWMutex
	logger  log.StandardLogger
	path    string
	closed  bool
	factory *Factory
	stats   *MockStats
}

// Factory implements the Factory interface
type Factory struct {
	databases map[string]*KVDB
	mutex     sync.RWMutex
}

// Batch implements the Batch interface
type Batch struct {
	kvdb       *KVDB
	operations []batchOp
	mutex      sync.Mutex
}

// MockStats implements the Stats interface
type MockStats struct {
	heads []cid.Cid
}

type batchOp struct {
	op    string // "put" or "delete"
	key   string
	value []byte
}

// New creates a new Factory instance
func New() kvdb.Factory {
	return &Factory{
		databases: make(map[string]*KVDB),
	}
}

// Close closes the factory and all databases
func (f *Factory) Close() {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	for _, db := range f.databases {
		db.Close()
	}
	f.databases = nil
}

// New creates a new KVDB instance
func (f *Factory) New(logger log.StandardLogger, path string, rebroadcastIntervalSec int) (kvdb.KVDB, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	if db, exists := f.databases[path]; exists {
		return db, nil
	}

	db := &KVDB{
		data:    make(map[string][]byte),
		logger:  logger,
		path:    path,
		factory: f,
		stats:   &MockStats{},
	}

	f.databases[path] = db
	return db, nil
}

// Get retrieves the key indexed data
func (m *KVDB) Get(ctx context.Context, key string) ([]byte, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if m.closed {
		return nil, ErrClosed
	}

	if data, exists := m.data[key]; exists {
		return data, nil
	}
	return nil, ErrNotFound
}

// Put inserts the data, indexed by key
func (m *KVDB) Put(ctx context.Context, key string, v []byte) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.closed {
		return ErrClosed
	}

	m.data[key] = v
	return nil
}

// Delete deletes the key and index data
func (m *KVDB) Delete(ctx context.Context, key string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.closed {
		return ErrClosed
	}

	delete(m.data, key)
	return nil
}

// List lists all keys with the given prefix
func (m *KVDB) List(ctx context.Context, prefix string) ([]string, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if m.closed {
		return nil, ErrClosed
	}

	var keys []string
	for key := range m.data {
		if prefix == "" || (len(key) >= len(prefix) && key[:len(prefix)] == prefix) {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

// ListAsync returns a channel to list listed keys
func (m *KVDB) ListAsync(ctx context.Context, prefix string) (chan string, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if m.closed {
		return nil, ErrClosed
	}

	ch := make(chan string, 1024) // Using default buffer size

	go func() {
		defer close(ch)

		// Check context before starting
		select {
		case <-ctx.Done():
			return
		default:
		}

		for key := range m.data {
			if prefix == "" || (len(key) >= len(prefix) && key[:len(prefix)] == prefix) {
				select {
				case ch <- key:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return ch, nil
}

// ListRegEx lists all keys matching the given prefix and regexs
func (m *KVDB) ListRegEx(ctx context.Context, prefix string, regexs ...string) ([]string, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if m.closed {
		return nil, ErrClosed
	}

	var keys []string
	for key := range m.data {
		// Check prefix first
		if prefix != "" && (len(key) < len(prefix) || key[:len(prefix)] != prefix) {
			continue
		}

		// Check regex patterns
		matches := true
		for _, regexStr := range regexs {
			re, err := regexp.Compile(regexStr)
			if err != nil {
				return nil, err
			}
			if !re.MatchString(key) {
				matches = false
				break
			}
		}

		if matches {
			keys = append(keys, key)
		}
	}

	return keys, nil
}

// ListRegExAsync returns a channel to list all regex matched keys
func (m *KVDB) ListRegExAsync(ctx context.Context, prefix string, regexs ...string) (chan string, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if m.closed {
		return nil, ErrClosed
	}

	ch := make(chan string, 1024) // Using default buffer size

	go func() {
		defer close(ch)

		// Check context before starting
		select {
		case <-ctx.Done():
			return
		default:
		}

		for key := range m.data {
			// Check prefix first
			if prefix != "" && (len(key) < len(prefix) || key[:len(prefix)] != prefix) {
				continue
			}

			// Check regex patterns
			matches := true
			for _, regexStr := range regexs {
				re, err := regexp.Compile(regexStr)
				if err != nil {
					// Log error but continue
					if m.logger != nil {
						m.logger.Error("Failed to compile regex:", regexStr, err)
					}
					matches = false
					break
				}
				if !re.MatchString(key) {
					matches = false
					break
				}
			}

			if matches {
				select {
				case ch <- key:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return ch, nil
}

// Batch creates a Batch interface of the current KVDB
func (m *KVDB) Batch(ctx context.Context) (kvdb.Batch, error) {
	if m.closed {
		return nil, ErrClosed
	}

	return &Batch{
		kvdb:       m,
		operations: make([]batchOp, 0),
	}, nil
}

// Sync syncs the KVDB key values
func (m *KVDB) Sync(ctx context.Context, key string) error {
	if m.closed {
		return ErrClosed
	}
	// Mock implementation - no actual syncing needed
	return nil
}

// Factory returns the factory that created this KVDB
func (m *KVDB) Factory() kvdb.Factory {
	return m.factory
}

// Stats returns the stats for this KVDB
func (m *KVDB) Stats(ctx context.Context) kvdb.Stats {
	return m.stats
}

// Close closes the KVDB
func (m *KVDB) Close() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if !m.closed {
		m.closed = true
		// Clear data
		m.data = nil
	}
}

// Batch implementation

// Put adds a put operation to the batch
func (b *Batch) Put(key string, value []byte) error {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	b.operations = append(b.operations, batchOp{
		op:    "put",
		key:   key,
		value: value,
	})
	return nil
}

// Delete adds a delete operation to the batch
func (b *Batch) Delete(key string) error {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	b.operations = append(b.operations, batchOp{
		op:  "delete",
		key: key,
	})
	return nil
}

// Commit executes all operations in the batch
func (b *Batch) Commit() error {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	for _, op := range b.operations {
		switch op.op {
		case "put":
			if err := b.kvdb.Put(context.Background(), op.key, op.value); err != nil {
				return err
			}
		case "delete":
			if err := b.kvdb.Delete(context.Background(), op.key); err != nil {
				return err
			}
		}
	}

	// Clear operations after commit
	b.operations = b.operations[:0]
	return nil
}

// MockStats implementation

// Type returns the type of stats
func (s *MockStats) Type() kvdb.Type {
	return kvdb.TypeCRDT
}

// Heads returns the heads
func (s *MockStats) Heads() []cid.Cid {
	return s.heads
}

// Encode returns CBOR encoding of stats
func (s *MockStats) Encode() []byte {
	// Mock implementation - return empty bytes for now
	return []byte{}
}

// Decode decodes CBOR data into stats
func (s *MockStats) Decode(data []byte) error {
	// Mock implementation - no actual decoding for now
	return nil
}
