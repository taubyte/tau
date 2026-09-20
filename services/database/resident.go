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

// resident.go is what makes this a service rather than a library the worker
// links.
//
// # The mechanism the note asked for, instead of the word "hot"
//
// A bucket's current version is DERIVED from its keys, so whichever node holds
// the blocks can answer. A database's is an open engine — a sql pool, a vector
// index, a document collection — which costs more to build than the request
// that needs it. So a database has to be served where its engine already is,
// and something has to own the engines to be able to say where that is.
//
// # What v1 did, and what node/database.go still does
//
// Opened on first use, keyed by handle, held until the process stopped. No
// bound, no expiry, and — because the key carries no revision — no reopen: an
// author who changed a database's host or rotated its credentials was ignored
// by every node that had already opened it, until that node restarted.
//
// All three are fixed here by the same table, and none of them is a wire
// concern: a caller names a database per call and never holds one, so warming
// policy changes without a proto change.

var (
	// ErrTooManyResident is a node asked to hold more engines than it may.
	ErrTooManyResident = errors.New("database: this node holds as many databases as it may")

	// DefaultMaxResident bounds the table. It exists because an unbounded one is
	// what v1 shipped, not because this number is knowable in advance.
	DefaultMaxResident = 256

	// DefaultIdle is how long an engine with no traffic stays open. A database
	// nobody has asked for in this long is one whose next request can afford to
	// open it; holding it costs a connection on somebody else's server.
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

	// revision is the deployment the declaration came from. An engine opened
	// from an older one is CLOSED rather than reused: that is the reopen v1
	// never did, and it is why a rotated credential takes effect on the next
	// call instead of on the next restart.
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

		// A NEWER DEPLOYMENT IS A DIFFERENT DATABASE. The id is stable across
		// an edit — "stable across renames" is the vocabulary's promise — so a
		// changed host, a rotated credential or a new tls mode arrives under
		// the same key and would otherwise be served by the connection it
		// replaced.
		e.drop(el)
	}
	e.mu.Unlock()

	// OPENED OUTSIDE THE LOCK, because opening dials somebody else's server and
	// a node holding this mutex through a network timeout would stop answering
	// for every other database it holds.
	conn, err := e.registry.Open(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("database: opening %s %q: %w", spec.Engine, spec.Name, err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// SOMEBODY ELSE MAY HAVE WON THE RACE while this was dialling. Theirs is as
	// good as ours and is already in the table, so ours is closed rather than
	// leaked — two connections under one key is the leak with extra steps.
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

	// CLOSED OFF THE CALLER'S PATH. A close on a dead connection blocks until
	// its own timeout, and the request that happened to evict it is not the
	// request that should pay for that.
	go r.conn.Close()
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
