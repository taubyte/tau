package mock

import (
	"context"
	"errors"
	"regexp"
	"sync"

	"github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/core/kvdb"
)

var (
	ErrClosed = errors.New("kvdb is closed")

	// ErrNotFound is go-datastore's, as the real kvdb returns: a sentinel of the
	// mock's own would let code pass against the mock that fails in production.
	ErrNotFound = ds.ErrNotFound
)

type Instance struct {
	data    map[string][]byte
	mutex   sync.RWMutex
	logger  log.StandardLogger
	path    string
	closed  bool
	factory *Factory
	stats   *MockStats
}

type Factory struct {
	databases map[string]*Instance
	mutex     sync.RWMutex
}

type Batch struct {
	kvdb       *Instance
	operations []batchOp
	mutex      sync.Mutex
}

type MockStats struct {
	heads []cid.Cid
}

type batchOp struct {
	op    string // "put" or "delete"
	key   string
	value []byte
}

func New() *Factory {
	return &Factory{
		databases: make(map[string]*Instance),
	}
}

func (f *Factory) Close() {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	for _, db := range f.databases {
		db.Close()
	}
	f.databases = nil
}

func (f *Factory) New(logger log.StandardLogger, path string, rebroadcastIntervalSec int) (kvdb.Instance, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	if db, exists := f.databases[path]; exists {
		return db, nil
	}

	db := &Instance{
		data:    make(map[string][]byte),
		logger:  logger,
		path:    path,
		factory: f,
		stats:   &MockStats{},
	}

	f.databases[path] = db
	return db, nil
}

func (m *Instance) Get(ctx context.Context, key string) ([]byte, error) {
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

func (m *Instance) Put(ctx context.Context, key string, v []byte) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.closed {
		return ErrClosed
	}

	m.data[key] = v
	return nil
}

func (m *Instance) Delete(ctx context.Context, key string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.closed {
		return ErrClosed
	}

	delete(m.data, key)
	return nil
}

func (m *Instance) List(ctx context.Context, prefix string) ([]string, error) {
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

func (m *Instance) ListAsync(ctx context.Context, prefix string) (chan string, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if m.closed {
		return nil, ErrClosed
	}

	ch := make(chan string, 1024)

	go func() {
		defer close(ch)

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

func (m *Instance) ListRegEx(ctx context.Context, prefix string, regexs ...string) ([]string, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if m.closed {
		return nil, ErrClosed
	}

	var keys []string
	for key := range m.data {
		if prefix != "" && (len(key) < len(prefix) || key[:len(prefix)] != prefix) {
			continue
		}

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

func (m *Instance) ListRegExAsync(ctx context.Context, prefix string, regexs ...string) (chan string, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if m.closed {
		return nil, ErrClosed
	}

	ch := make(chan string, 1024)

	go func() {
		defer close(ch)

		select {
		case <-ctx.Done():
			return
		default:
		}

		for key := range m.data {
			if prefix != "" && (len(key) < len(prefix) || key[:len(prefix)] != prefix) {
				continue
			}

			matches := true
			for _, regexStr := range regexs {
				re, err := regexp.Compile(regexStr)
				if err != nil {
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

func (m *Instance) Batch(ctx context.Context) (kvdb.Batch, error) {
	if m.closed {
		return nil, ErrClosed
	}

	return &Batch{
		kvdb:       m,
		operations: make([]batchOp, 0),
	}, nil
}

func (m *Instance) Sync(ctx context.Context, key string) error {
	if m.closed {
		return ErrClosed
	}
	return nil
}

func (m *Instance) Factory() *Factory {
	return m.factory
}

func (m *Instance) Stats(ctx context.Context) kvdb.Stats {
	return m.stats
}

func (m *Instance) Close() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if !m.closed {
		m.closed = true
		m.data = nil
	}
}

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

func (b *Batch) Delete(key string) error {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	b.operations = append(b.operations, batchOp{
		op:  "delete",
		key: key,
	})
	return nil
}

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

	b.operations = b.operations[:0]
	return nil
}

func (s *MockStats) Type() kvdb.Type {
	return kvdb.TypeCRDT
}

func (s *MockStats) Heads() []cid.Cid {
	return s.heads
}
