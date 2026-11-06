package register

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	register "github.com/taubyte/tau/pkg/register"
)

// watch.go is the register's half of the readers' cache: a session per reader, and
// an index from what an answer was read from to the entries holding it.
//
// A BINDING FIRES ONCE. A change to anything an entry was read from names the entry
// on its session and releases it; the reader drops the answer and binds afresh when
// it reads again. Nothing here keeps an answer, so an entry costs only the keys it
// was read from.
//
// A CHANGE IS NEVER DROPPED SILENTLY. The database's observer hands keys to a bounded
// queue; one that cannot be queued drops every session instead, and a reader whose
// session ends drops what it bound there.

// The headers a read binds with: the session, the reader's entry, and the answer's
// mark that it was bound. A read without them is answered and kept by nobody.
const (
	sessionHeader = "Tau-Watch-Session"
	entryHeader   = "Tau-Watch-Entry"
	boundHeader   = "Tau-Watch-Bound"
)

// watchLimits bound one node's sessions.
type watchLimits struct {
	// entries is how many entries one session holds bound at once.
	entries int
	// beat is how often a quiet session says it is there.
	beat time.Duration
	// flush is how long a change waits for others to share its message.
	flush time.Duration
	// batch is the most entries one message names.
	batch int
	// queue is how many committed changes may wait to be matched.
	queue int
}

var defaultWatchLimits = watchLimits{entries: 1 << 16, beat: 10 * time.Second, flush: 20 * time.Millisecond, batch: 1024, queue: 1 << 14}

type binding struct {
	s     *session
	entry uint64
}

type watches struct {
	limits  watchLimits
	changes chan string
	// lost is set when a change could not be queued; the matcher then drops every
	// session, since one of them may have needed it.
	lost atomic.Bool
	done chan struct{}
	stop sync.Once

	mu       sync.Mutex
	sessions map[string]*session
	keys     map[string]map[binding]struct{}
	prefixes map[string]map[binding]struct{}
}

// session is one reader's stream. Its fields other than id, wake and gone are
// guarded by watches.mu.
type session struct {
	id      string
	entries map[uint64]register.Depends
	pending []uint64
	dropped bool
	wake    chan struct{}
	gone    chan struct{}
}

func newWatches(limits watchLimits) *watches {
	w := &watches{
		limits:   limits,
		changes:  make(chan string, limits.queue),
		done:     make(chan struct{}),
		sessions: map[string]*session{},
		keys:     map[string]map[binding]struct{}{},
		prefixes: map[string]map[binding]struct{}{},
	}
	go w.match()
	return w
}

// Put and Delete are the database's observer, and a local write's: both only queue.
func (w *watches) Put(key string, _ []byte) { w.changed(key) }
func (w *watches) Delete(key string)        { w.changed(key) }

func (w *watches) changed(key string) {
	select {
	case w.changes <- key:
	default:
		w.lost.Store(true)
	}
}

func (w *watches) close() { w.stop.Do(func() { close(w.done) }) }

func (w *watches) match() {
	for {
		select {
		case <-w.done:
			w.dropAll()
			return
		case key := <-w.changes:
			if w.lost.Swap(false) {
				w.dropAll()
				continue
			}
			w.fire(key)
		}
	}
}

// fire releases every entry read from key, naming it on its session.
func (w *watches) fire(key string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var fired []binding
	for b := range w.keys[key] {
		fired = append(fired, b)
	}
	// Every prefix a key falls under ends at one of its separators.
	for i := 0; i < len(key); i++ {
		if key[i] == '/' {
			for b := range w.prefixes[key[:i+1]] {
				fired = append(fired, b)
			}
		}
	}
	for _, b := range fired {
		d, ok := b.s.entries[b.entry]
		if !ok {
			continue
		}
		w.unbindLocked(b, d)
		b.s.pending = append(b.s.pending, b.entry)
		select {
		case b.s.wake <- struct{}{}:
		default:
		}
	}
}

func (w *watches) open() *session {
	var id [16]byte
	rand.Read(id[:])
	s := &session{id: hex.EncodeToString(id[:]), entries: map[uint64]register.Depends{}, wake: make(chan struct{}, 1), gone: make(chan struct{})}
	w.mu.Lock()
	w.sessions[s.id] = s
	w.mu.Unlock()
	return s
}

// end releases everything s holds.
func (w *watches) end(s *session) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.dropLocked(s)
}

func (w *watches) dropAll() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, s := range w.sessions {
		w.dropLocked(s)
	}
}

func (w *watches) dropLocked(s *session) {
	if s.dropped {
		return
	}
	s.dropped = true
	for entry, d := range s.entries {
		w.unbindLocked(binding{s: s, entry: entry}, d)
	}
	delete(w.sessions, s.id)
	close(s.gone)
}

// named is the session and entry a request binds to, when it names a session this
// node holds.
func (w *watches) named(h http.Header) (*session, uint64, bool) {
	id := h.Get(sessionHeader)
	if id == "" {
		return nil, 0, false
	}
	entry, err := strconv.ParseUint(h.Get(entryHeader), 10, 64)
	if err != nil {
		return nil, 0, false
	}
	s, ok := w.find(id)
	return s, entry, ok
}

func (w *watches) find(id string) (*session, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, ok := w.sessions[id]
	return s, ok
}

// bind holds entry on everything d was read from, replacing what it held. It is
// false for a session gone or full.
func (w *watches) bind(s *session, entry uint64, d register.Depends) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if s.dropped {
		return false
	}
	b := binding{s: s, entry: entry}
	if old, ok := s.entries[entry]; ok {
		w.unbindLocked(b, old)
	} else if len(s.entries) >= w.limits.entries {
		return false
	}
	d = covered(d)
	for _, k := range d.Keys {
		add(w.keys, k, b)
	}
	for _, p := range d.Prefixes {
		add(w.prefixes, p, b)
	}
	s.entries[entry] = d
	return true
}

// forget releases entries a reader no longer keeps.
func (w *watches) forget(s *session, entries ...uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, entry := range entries {
		if d, ok := s.entries[entry]; ok {
			w.unbindLocked(binding{s: s, entry: entry}, d)
		}
	}
}

func (w *watches) unbindLocked(b binding, d register.Depends) {
	for _, k := range d.Keys {
		remove(w.keys, k, b)
	}
	for _, p := range d.Prefixes {
		remove(w.prefixes, p, b)
	}
	delete(b.s.entries, b.entry)
}

// take is up to a message's worth of what s has to be told.
func (w *watches) take(s *session) []uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := min(len(s.pending), w.limits.batch)
	out := s.pending[:n:n]
	s.pending = s.pending[n:]
	if len(s.pending) == 0 {
		s.pending = nil
	}
	return out
}

func (w *watches) bound(s *session) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(s.entries)
}

// covered is d without the keys its own prefixes already hold: one change, one
// match.
func covered(d register.Depends) register.Depends {
	if len(d.Prefixes) == 0 {
		return d
	}
	out := register.Depends{Prefixes: d.Prefixes}
	for _, k := range d.Keys {
		if !(register.Depends{Prefixes: d.Prefixes}).Covers(k) {
			out.Keys = append(out.Keys, k)
		}
	}
	return out
}

func add(index map[string]map[binding]struct{}, at string, b binding) {
	held := index[at]
	if held == nil {
		held = map[binding]struct{}{}
		index[at] = held
	}
	held[b] = struct{}{}
}

func remove(index map[string]map[binding]struct{}, at string, b binding) {
	if held := index[at]; held != nil {
		delete(held, b)
		if len(held) == 0 {
			delete(index, at)
		}
	}
}

// union is a and what b adds, sharing neither's slices.
func union(a, b register.Depends) register.Depends {
	out := register.Depends{Keys: slices.Clone(a.Keys), Prefixes: slices.Clone(a.Prefixes)}
	for _, p := range b.Prefixes {
		if !slices.Contains(out.Prefixes, p) {
			out.Prefixes = append(out.Prefixes, p)
		}
	}
	for _, k := range b.Keys {
		if !out.Covers(k) {
			out.Keys = append(out.Keys, k)
		}
	}
	return out
}

// answers says whether err is an answer a reader may keep, an absence, rather than a
// failure to look.
func answers(err error) bool {
	return err == nil ||
		errors.Is(err, register.ErrNoHost) ||
		errors.Is(err, register.ErrNotPublished) ||
		errors.Is(err, register.ErrNoView) ||
		errors.Is(err, register.ErrNoObject)
}

// boundRead answers read and, when the request names a session here, binds the
// answer to what it was read from. It reads, binds, and reads again until a read
// touches nothing the binding does not hold: a change after the binding then names
// the entry, and one before it is in the answer.
func boundRead[T any](s *server, h http.Header, read func(*register.Register) (T, error)) (T, bool, error) {
	sess, entry, ok := s.watches.named(h)
	if !ok {
		v, err := read(s.reg)
		return v, false, err
	}
	var held register.Depends
	for attempt := range 3 {
		rec, d := s.reg.Recorded()
		v, err := read(rec)
		if !answers(err) {
			s.watches.forget(sess, entry)
			return v, false, err
		}
		if attempt > 0 && d.Within(held) {
			return v, true, err
		}
		held = union(held, *d)
		if !s.watches.bind(sess, entry, held) {
			return v, false, err
		}
	}
	s.watches.forget(sess, entry)
	v, err := read(s.reg)
	return v, false, err
}

// observed is a store whose writes are heard by the watches as they are made: a
// register over a database also hears them from its observer, which only fires an
// entry already released, and one over a MemStore hears them nowhere else.
type observed struct {
	register.Store
	w *watches
}

func (o observed) Put(ctx context.Context, key string, value []byte) error {
	err := o.Store.Put(ctx, key, value)
	if err == nil {
		o.w.changed(key)
	}
	return err
}
