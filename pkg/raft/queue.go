package raft

import (
	"encoding/binary"
	"fmt"
	"path"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const queueKeyPrefix = "_q"

type queue struct {
	cluster     Cluster
	name        string
	prefix      string
	counterKey  string
	itemsPrefix string
	indexPrefix string
	closed      atomic.Bool
	mu          sync.Mutex
}

// NewQueue returns a Queue backed by the cluster's KV primitives.
// The name identifies this queue within the cluster — the internal key prefix
// is derived from it automatically.
func NewQueue(cluster Cluster, name string) Queue {
	prefix := path.Join(queueKeyPrefix, name)
	return &queue{
		cluster:     cluster,
		name:        name,
		prefix:      prefix,
		counterKey:  path.Join(prefix, "_counter"),
		itemsPrefix: path.Join(prefix, "items") + "/",
		indexPrefix: path.Join(prefix, "idx") + "/",
	}
}

func (q *queue) indexKey(id string) string {
	return q.indexPrefix + id
}

func (q *queue) readCounter() uint64 {
	data, ok := q.cluster.Get(q.counterKey)
	if !ok || len(data) < 8 {
		return 0
	}
	return binary.BigEndian.Uint64(data)
}

func encodeCounter(seq uint64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, seq)
	return buf
}

func sequenceID(seq uint64) string {
	return fmt.Sprintf("%020d", seq)
}

func (q *queue) itemKey(seq string) string {
	return q.itemsPrefix + seq
}

// queueEntry stores (id, data) together in a single KV value so Pop can
// return the id without an extra index lookup.
type queueEntry struct {
	id   string
	data []byte
}

func encodeEntry(id string, data []byte) []byte {
	idLen := len(id)
	buf := make([]byte, 4+idLen+len(data))
	binary.BigEndian.PutUint32(buf[:4], uint32(idLen))
	copy(buf[4:4+idLen], id)
	copy(buf[4+idLen:], data)
	return buf
}

func decodeEntry(raw []byte) (queueEntry, error) {
	if len(raw) < 4 {
		return queueEntry{}, fmt.Errorf("entry too short")
	}
	idLen := int(binary.BigEndian.Uint32(raw[:4]))
	if len(raw) < 4+idLen {
		return queueEntry{}, fmt.Errorf("entry truncated")
	}
	return queueEntry{
		id:   string(raw[4 : 4+idLen]),
		data: raw[4+idLen:],
	}, nil
}

func (q *queue) Push(id string, data []byte, timeout time.Duration) error {
	if q.closed.Load() {
		return ErrShutdown
	}
	if id == "" {
		return fmt.Errorf("queue item id must be non-empty")
	}
	if timeout <= 0 || timeout > MaxApplyTimeout {
		timeout = MaxApplyTimeout
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if _, exists := q.cluster.Get(q.indexKey(id)); exists {
		return nil
	}

	seq := q.readCounter() + 1
	seqStr := sequenceID(seq)

	return q.cluster.Batch([]BatchOp{
		{Set: &SetCommand{Key: q.counterKey, Value: encodeCounter(seq)}},
		{Set: &SetCommand{Key: q.itemKey(seqStr), Value: encodeEntry(id, data)}},
		{Set: &SetCommand{Key: q.indexKey(id), Value: []byte(seqStr)}},
	}, timeout)
}

func (q *queue) Pop(timeout time.Duration) (string, []byte, error) {
	if q.closed.Load() {
		return "", nil, ErrShutdown
	}
	if timeout <= 0 || timeout > MaxApplyTimeout {
		timeout = MaxApplyTimeout
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	keys := q.sortedItemKeys()
	if len(keys) == 0 {
		return "", nil, ErrQueueEmpty
	}

	key := keys[0]
	raw, ok := q.cluster.Get(key)
	if !ok {
		return "", nil, ErrQueueEmpty
	}

	entry, err := decodeEntry(raw)
	if err != nil {
		return "", nil, fmt.Errorf("corrupt queue entry: %w", err)
	}

	err = q.cluster.Batch([]BatchOp{
		{Delete: &DeleteCommand{Key: key}},
		{Delete: &DeleteCommand{Key: q.indexKey(entry.id)}},
	}, timeout)
	if err != nil {
		return "", nil, err
	}
	return entry.id, entry.data, nil
}

func (q *queue) Peek() (string, []byte, bool) {
	if q.closed.Load() {
		return "", nil, false
	}

	keys := q.sortedItemKeys()
	if len(keys) == 0 {
		return "", nil, false
	}

	raw, ok := q.cluster.Get(keys[0])
	if !ok {
		return "", nil, false
	}

	entry, err := decodeEntry(raw)
	if err != nil {
		return "", nil, false
	}
	return entry.id, entry.data, true
}

func (q *queue) Len() int {
	if q.closed.Load() {
		return 0
	}
	return len(q.cluster.Keys(q.itemsPrefix))
}

func (q *queue) Close() error {
	q.closed.Store(true)
	return nil
}

func (q *queue) sortedItemKeys() []string {
	keys := q.cluster.Keys(q.itemsPrefix)
	sort.Strings(keys)
	return keys
}
