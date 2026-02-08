package raft

import (
	"path"
	"sync/atomic"
	"time"

	"github.com/fxamacker/cbor/v2"
	idutil "github.com/taubyte/tau/utils/id"
)

type queue struct {
	cluster Cluster
	prefix  string
	closed  atomic.Bool
}

// NewQueue returns a Queue that uses the given cluster and key prefix for storage.
func NewQueue(cluster Cluster, queuePrefix string) Queue {
	return &queue{
		cluster: cluster,
		prefix:  queuePrefix,
	}
}

func (q *queue) Enqueue(data []byte, timeout time.Duration) (id string, err error) {
	if q.closed.Load() {
		return "", ErrShutdown
	}
	if timeout <= 0 || timeout > MaxApplyTimeout {
		return "", ErrInvalidTimeout
	}
	itemID := idutil.Generate()
	cmd, err := encodeEnqueueCommand(q.prefix, itemID, data)
	if err != nil {
		return "", err
	}
	resp, err := q.cluster.Apply(cmd, timeout)
	if err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", resp.Error
	}
	return itemID, nil
}

func (q *queue) Dequeue(timeout time.Duration) (id string, data []byte, err error) {
	if q.closed.Load() {
		return "", nil, ErrShutdown
	}
	if timeout <= 0 || timeout > MaxApplyTimeout {
		return "", nil, ErrInvalidTimeout
	}
	cmd, err := encodeDequeueCommand(q.prefix)
	if err != nil {
		return "", nil, err
	}
	resp, err := q.cluster.Apply(cmd, timeout)
	if err != nil {
		return "", nil, err
	}
	if resp.Error != nil {
		return "", nil, resp.Error
	}
	if len(resp.Data) == 0 {
		return "", nil, nil
	}
	var dr dequeueResponse
	if err := cbor.Unmarshal(resp.Data, &dr); err != nil {
		return "", nil, err
	}
	return dr.ID, dr.Data, nil
}

func (q *queue) Peek() (id string, data []byte, ok bool) {
	if q.closed.Load() {
		return "", nil, false
	}
	pendingKey := path.Join(q.prefix, "pending")
	val, ok := q.cluster.Get(pendingKey)
	if !ok || len(val) == 0 {
		return "", nil, false
	}
	var list []string
	if err := cbor.Unmarshal(val, &list); err != nil || len(list) == 0 {
		return "", nil, false
	}
	id = list[0]
	itemKey := path.Join(q.prefix, "item", id)
	data, ok = q.cluster.Get(itemKey)
	if !ok {
		return "", nil, false
	}
	return id, data, true
}

func (q *queue) Len() int {
	if q.closed.Load() {
		return 0
	}
	pendingKey := path.Join(q.prefix, "pending")
	val, ok := q.cluster.Get(pendingKey)
	if !ok || len(val) == 0 {
		return 0
	}
	var list []string
	if err := cbor.Unmarshal(val, &list); err != nil {
		return 0
	}
	return len(list)
}

func (q *queue) Close() error {
	q.closed.Store(true)
	return nil
}
