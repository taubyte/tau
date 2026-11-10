package database

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/taubyte/tau/pkg/database"
)

// A database's current version is an open engine (a sql pool, a vector index), costly
// to build, so it is served where the engine already is, and this table owns the
// engines. It is bounded, expires idle engines, and reopens on a newer revision so
// changed hosts and rotated credentials take effect without a restart.

var (
	// ErrTooManyResident is a node asked to hold more engines than it may.
	ErrTooManyResident = errors.New("database: this node holds as many databases as it may")
)

const (
	// DefaultMaxResident bounds the table. It exists because an unbounded one is
	// what v1 shipped, not because this number is knowable in advance.
	DefaultMaxResident = 256

	// DefaultIdle is how long an engine with no traffic stays open; holding it costs a
	// connection on somebody else's server.
	DefaultIdle = 10 * time.Minute
)

// engines holds what this node has open, least recently used first.
type engines struct {
	registry *database.Registry
	now      func() time.Time
	max      int
	idle     time.Duration

	mu    sync.Mutex
	byRef map[string]*list.Element
	order *list.List
}

// resident is one open engine and what it was opened from.
type resident struct {
	ref string

	// revision is the deployment the declaration came from. An engine from an older one
	// is closed, not reused, so a rotated credential takes effect on the next call.
	revision uint64

	conn database.Conn
	used time.Time
}

func newEngines(registry *database.Registry, now func() time.Time, max int, idle time.Duration) *engines {
	if now == nil {
		now = time.Now
	}
	if max < 1 {
		max = DefaultMaxResident
	}
	if idle <= 0 {
		idle = DefaultIdle
	}
	return &engines{
		registry: registry, now: now, max: max, idle: idle,
		byRef: map[string]*list.Element{}, order: list.New(),
	}
}

// open answers the engine for one database, opening it if this node does not
// hold it and replacing it if what it holds came from an older deployment.
func (e *engines) open(ctx context.Context, key string, revision uint64, spec database.Spec) (database.Conn, error) {
	e.mu.Lock()

	if el, held := e.byRef[key]; held {
		r := el.Value.(*resident)
		if r.revision == revision {
			r.used = e.now()
			e.order.MoveToBack(el)
			conn := r.conn
			e.mu.Unlock()
			return conn, nil
		}

		// A newer deployment is a different database: the id is stable across edits.
		e.drop(el)
	}
	e.mu.Unlock()

	// Opened outside the lock: opening may dial a remote server.
	conn, err := e.registry.Open(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("database: opening %s %q: %w", spec.Engine, spec.Name, err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Somebody else won the race while this dialled; ours is closed rather than leaked.
	if el, held := e.byRef[key]; held {
		r := el.Value.(*resident)
		if r.revision == revision {
			e.order.MoveToBack(el)
			r.used = e.now()
			go conn.Close()
			return r.conn, nil
		}
		e.drop(el)
	}

	e.evictIdle()
	if e.order.Len() >= e.max {
		if oldest := e.order.Front(); oldest != nil {
			e.drop(oldest)
		}
	}
	if e.order.Len() >= e.max {
		go conn.Close()
		return nil, fmt.Errorf("%w: %d", ErrTooManyResident, e.max)
	}

	e.byRef[key] = e.order.PushBack(&resident{ref: key, revision: revision, conn: conn, used: e.now()})
	return conn, nil
}

// evictIdle closes what nobody has asked for. Called with e.mu held.
func (e *engines) evictIdle() {
	cutoff := e.now().Add(-e.idle)
	for {
		oldest := e.order.Front()
		if oldest == nil || oldest.Value.(*resident).used.After(cutoff) {
			return
		}
		e.drop(oldest)
	}
}

// drop closes one and forgets it. Called with e.mu held.
func (e *engines) drop(el *list.Element) {
	r := el.Value.(*resident)
	delete(e.byRef, r.ref)
	e.order.Remove(el)

	// Closed off the caller's path: a close on a dead connection blocks until timeout.
	go r.conn.Close()
}

// forget drops one database's engine because the node stopped holding it and the
// store was destroyed: serving from it would answer not-found for rows another node
// holds, and a write would recreate bookkeeping under the removed prefix.
func (e *engines) forget(key string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if el, held := e.byRef[key]; held {
		e.drop(el)
	}
}

// held is how many engines this node has open, for an operator asking and for a
// test of the bound.
func (e *engines) held() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.order.Len()
}

// closeAll releases everything, for the service's own shutdown.
func (e *engines) closeAll() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	var err error
	for el := e.order.Front(); el != nil; el = el.Next() {
		err = errors.Join(err, el.Value.(*resident).conn.Close())
	}
	e.byRef = map[string]*list.Element{}
	e.order.Init()
	return err
}
