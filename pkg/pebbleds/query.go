package pebbleds

import (
	"context"
	"fmt"
	"sync"

	"github.com/cockroachdb/pebble/v2"
	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
)

// Query runs q on a pebble iterator, pulled by the caller: there is no
// goroutine behind the results. Ordering by key, either way, is the
// iterator's own; any other order sorts every match in memory.
func (s *Store) Query(_ context.Context, q query.Query) (query.Results, error) {
	switch {
	case len(q.Orders) == 0:
		return s.query(q, false)
	case len(q.Orders) == 1:
		switch q.Orders[0].(type) {
		case query.OrderByKey, *query.OrderByKey:
			return s.query(q, false)
		case query.OrderByKeyDescending, *query.OrderByKeyDescending:
			return s.query(q, true)
		}
	}
	base := q
	base.Orders, base.Offset, base.Limit = nil, 0, 0
	res, err := s.query(base, false)
	if err != nil {
		return nil, err
	}
	rest := q
	rest.Prefix, rest.Filters = "", nil
	return query.NaiveQueryApply(rest, query.ResultsReplaceQuery(res, q)), nil
}

func (s *Store) query(q query.Query, desc bool) (query.Results, error) {
	if !s.enter() {
		return nil, ErrClosed
	}
	defer s.leave()
	prefix := ds.NewKey(q.Prefix).String()
	if prefix != "/" {
		prefix += "/"
	}
	it := &queryIter{
		s:        s,
		desc:     desc,
		keysOnly: q.KeysOnly,
		sizes:    q.ReturnsSizes,
		filters:  q.Filters,
		offset:   q.Offset,
		limit:    q.Limit,
	}
	it.opts.LowerBound = []byte(prefix)
	if upper, ok := successor(nil, it.opts.LowerBound); ok {
		it.opts.UpperBound = upper
	}
	var err error
	if it.it, err = s.db.NewIter(&it.opts); err != nil {
		return nil, fmt.Errorf("pebbleds: iterate: %w", err)
	}
	s.mu.Lock()
	s.queries[it] = struct{}{}
	s.mu.Unlock()
	return query.ResultsFromIterator(q, query.Iterator{Next: it.next, Close: it.close}), nil
}

// queryIter is one query's position. Its lock orders the caller's pulls
// against Close ending it.
type queryIter struct {
	s        *Store
	mu       sync.Mutex
	opts     pebble.IterOptions
	it       *pebble.Iterator
	started  bool
	desc     bool
	keysOnly bool
	sizes    bool
	filters  []query.Filter
	offset   int
	limit    int
	sent     int
	// ended is set when Close ended the query; the next pull reports it.
	ended bool
}

func (q *queryIter) next() (query.Result, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.it == nil {
		if q.ended {
			q.ended = false
			return query.Result{Error: ErrClosed}, true
		}
		return query.Result{}, false
	}
	for {
		if q.limit > 0 && q.sent >= q.limit {
			return q.finish()
		}
		if !q.move() {
			return q.finish()
		}
		e, err := q.entry()
		if err != nil {
			q.release()
			return query.Result{Error: err}, true
		}
		if !q.match(e) {
			continue
		}
		if q.offset > 0 {
			q.offset--
			continue
		}
		q.sent++
		return query.Result{Entry: e}, true
	}
}

func (q *queryIter) move() bool {
	switch {
	case !q.started && q.desc:
		q.started = true
		return q.it.Last()
	case !q.started:
		q.started = true
		return q.it.First()
	case q.desc:
		return q.it.Prev()
	default:
		return q.it.Next()
	}
}

func (q *queryIter) entry() (query.Entry, error) {
	e := query.Entry{Key: string(q.it.Key())}
	if q.keysOnly && !q.sizes {
		return e, nil
	}
	v, err := q.it.ValueAndErr()
	if err != nil {
		return e, fmt.Errorf("pebbleds: read: %w", err)
	}
	if !q.keysOnly {
		e.Value = append(make([]byte, 0, len(v)), v...)
	}
	if q.sizes {
		e.Size = len(v)
	}
	return e, nil
}

func (q *queryIter) match(e query.Entry) bool {
	for _, f := range q.filters {
		if !f.Filter(e) {
			return false
		}
	}
	return true
}

// finish ends the query at its last match, reporting the iterator's error
// if it stopped on one.
func (q *queryIter) finish() (query.Result, bool) {
	if err := q.release(); err != nil {
		return query.Result{Error: fmt.Errorf("pebbleds: iterate: %w", err)}, true
	}
	return query.Result{}, false
}

// release closes the iterator and forgets the query. q.mu is held.
func (q *queryIter) release() error {
	if q.it == nil {
		return nil
	}
	err := q.it.Close()
	q.it = nil
	q.s.mu.Lock()
	delete(q.s.queries, q)
	q.s.mu.Unlock()
	return err
}

func (q *queryIter) close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.release()
}

// end is Close ending a query its caller has not finished.
func (q *queryIter) end() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.it != nil {
		q.ended = true
		if err := q.release(); err != nil {
			logger.Errorf("closing a query: %v", err)
		}
	}
}
