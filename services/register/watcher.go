package register

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/service"
	registerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/register/v2"
)

// watcher.go keeps a reader's sessions: one on each of several register nodes, so no
// one node is what every kept answer rests on.
//
// A SESSION THAT ENDS TAKES ITS ANSWERS WITH IT, and no other session's. The watcher
// then opens one on another node. A session that misses three beats is ended: a
// stream can stay open to a node that no longer answers on it.

var errNoRegister = fmt.Errorf("%w: %s", service.ErrNoneRuns, Descriptor.Name)

// registers is where a watcher finds register nodes, or why it found none.
type registers interface {
	registers() ([]libp2ppeer.ID, error)
}

// discovered is the registers the directory on a node knows of, other than the node.
type discovered struct{ h service.Host }

func (d discovered) registers() ([]libp2ppeer.ID, error) {
	dir, ok := service.Get[service.Directory](d.h)
	if !ok {
		return nil, fmt.Errorf("%w: %s, and no directory runs here to ask", service.ErrNoneRuns, Descriptor.Name)
	}
	self := d.h.Peer().Host().ID()
	var out []libp2ppeer.ID
	for _, id := range dir.Peers(Descriptor.Name) {
		if id != self {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return nil, errNoRegister
	}
	return out, nil
}

// watcherLimits shape a reader's sessions.
type watcherLimits struct {
	// sessions is how many register nodes a reader watches through at once.
	sessions int
	// retry is how long a node a session could not be opened on, or that ended one,
	// is passed over.
	retry time.Duration
	// misses is how many beats a session may miss before it is ended.
	misses int
	// forget is how often releases of evicted answers are sent.
	forget time.Duration
	// open bounds opening a session.
	open time.Duration
}

var defaultWatcherLimits = watcherLimits{sessions: 2, retry: 5 * time.Second, misses: 3, forget: 100 * time.Millisecond, open: 10 * time.Second}

type watcher struct {
	h      host.Host
	from   registers
	limits watcherLimits

	ids atomic.Uint64
	rr  atomic.Uint64

	ctx    context.Context
	cancel context.CancelFunc
	wake   chan struct{}
	start  sync.Once

	mu       sync.Mutex
	live     []*watchSession
	opening  map[libp2ppeer.ID]bool
	failed   map[libp2ppeer.ID]time.Time
	fallback *Client
	closed   bool
}

func newWatcher(h host.Host, from registers, limits watcherLimits) *watcher {
	ctx, cancel := context.WithCancel(context.Background())
	return &watcher{
		h: h, from: from, limits: limits, ctx: ctx, cancel: cancel,
		wake: make(chan struct{}, 1), opening: map[libp2ppeer.ID]bool{}, failed: map[libp2ppeer.ID]time.Time{},
	}
}

// pick is a live session, the next in turn, or nil when none is.
func (w *watcher) pick() *watchSession {
	w.start.Do(func() { go w.keep() })
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(w.live)
	for range n {
		s := w.live[int(w.rr.Add(1)%uint64(n))]
		if !s.dead.Load() {
			return s
		}
	}
	return nil
}

// clientFor is the client a read goes through: the session's own, or, with none, one
// to any register node.
func (w *watcher) clientFor(s *watchSession) (*Client, error) {
	if s != nil {
		return s.client, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fallback != nil {
		return w.fallback, nil
	}
	ids, err := w.from.registers()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if c, err := NewClient(w.h, id); err == nil {
			w.fallback = c
			return c, nil
		}
	}
	return nil, errNoRegister
}

// failedThrough drops c as the fallback after a read through it could not look, so
// the next read tries another node.
func (w *watcher) failedThrough(c *Client) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fallback == c {
		w.fallback = nil
		c.Close()
	}
}

func (w *watcher) close() {
	w.mu.Lock()
	w.closed = true
	live := w.live
	fallback := w.fallback
	w.live, w.fallback = nil, nil
	w.mu.Unlock()
	w.cancel()
	for _, s := range live {
		s.die()
	}
	if fallback != nil {
		fallback.Close()
	}
}

// keep holds the watcher at its sessions, opening one whenever it is short.
func (w *watcher) keep() {
	tick := time.NewTicker(w.limits.retry / 5)
	defer tick.Stop()
	for {
		w.top()
		select {
		case <-w.ctx.Done():
			return
		case <-w.wake:
		case <-tick.C:
		}
	}
}

func (w *watcher) top() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	want := w.limits.sessions - len(w.live) - len(w.opening)
	if want <= 0 {
		return
	}
	used := map[libp2ppeer.ID]bool{}
	for _, s := range w.live {
		used[s.peer] = true
	}
	ids, err := w.from.registers()
	if err != nil {
		// No register is known yet: nothing to open a session on.
		return
	}
	now := time.Now()
	for _, id := range ids {
		if want == 0 {
			break
		}
		if used[id] || w.opening[id] || now.Sub(w.failed[id]) < w.limits.retry {
			continue
		}
		w.opening[id] = true
		want--
		go w.open(id)
	}
}

// open opens a session on id and follows it until it ends.
func (w *watcher) open(id libp2ppeer.ID) {
	s, err := w.dial(id)
	w.mu.Lock()
	delete(w.opening, id)
	if err != nil || w.closed {
		w.failed[id] = time.Now()
		w.mu.Unlock()
		if s != nil {
			s.die()
		}
		return
	}
	w.live = append(w.live, s)
	w.mu.Unlock()

	go s.forgets(w.limits.forget)
	s.follow(w.limits.misses)

	w.mu.Lock()
	for i, held := range w.live {
		if held == s {
			w.live = append(w.live[:i], w.live[i+1:]...)
			break
		}
	}
	w.failed[id] = time.Now()
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *watcher) dial(id libp2ppeer.ID) (*watchSession, error) {
	c, err := NewClient(w.h, id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(w.ctx)
	s := &watchSession{peer: id, client: c, cancel: cancel, entries: map[uint64]keeper{}}

	opened := make(chan error, 1)
	go func() {
		stream, err := c.watch(ctx)
		if err != nil {
			opened <- err
			return
		}
		if !stream.Receive() {
			opened <- errors.Join(errSessionNotOpened, stream.Err())
			return
		}
		ready := stream.Msg().GetReady()
		if ready == nil || ready.GetSession() == "" {
			opened <- errSessionNotOpened
			return
		}
		s.id, s.beat, s.stream = ready.GetSession(), time.Duration(ready.GetBeatMs())*time.Millisecond, stream
		opened <- nil
	}()

	select {
	case err = <-opened:
	case <-time.After(w.limits.open):
		err = errSessionNotOpened
	}
	if err != nil {
		s.die()
		return nil, err
	}
	return s, nil
}

var errSessionNotOpened = errors.New("register: the session did not open")

// keeper is a kept answer as its session holds it.
type keeper interface{ told() }

// watchSession is one session on one register node.
type watchSession struct {
	peer   libp2ppeer.ID
	client *Client
	id     string
	beat   time.Duration
	stream interface {
		Receive() bool
		Msg() *registerv2.WatchEvent
		Err() error
	}
	cancel context.CancelFunc
	dead   atomic.Bool
	heard  atomic.Int64

	mu      sync.Mutex
	entries map[uint64]keeper
	forgot  []uint64
}

// hold records that entry is about to be read bound on this session, so a change
// told before the answer comes back still reaches it. It is false for a dead session.
func (s *watchSession) hold(entry uint64, k keeper) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead.Load() {
		return false
	}
	s.entries[entry] = k
	return true
}

// release lets entry go, saying whether it was still held.
func (s *watchSession) release(entry uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, held := s.entries[entry]
	delete(s.entries, entry)
	return held
}

func (s *watchSession) forgetLater(entry uint64) {
	s.mu.Lock()
	s.forgot = append(s.forgot, entry)
	s.mu.Unlock()
}

// forgets sends the releases of evicted answers, a batch at a time, until the
// session ends.
func (s *watchSession) forgets(every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for !s.dead.Load() {
		<-tick.C
		s.mu.Lock()
		batch := s.forgot
		s.forgot = nil
		s.mu.Unlock()
		if len(batch) > 0 {
			ctx, cancel := context.WithTimeout(context.Background(), every*10)
			s.client.forget(ctx, s.id, batch)
			cancel()
		}
	}
}

// follow reads the session until it ends, or until it misses its beats.
func (s *watchSession) follow(misses int) {
	defer s.die()
	s.heard.Store(time.Now().UnixNano())
	if s.beat > 0 {
		go func() {
			tick := time.NewTicker(s.beat)
			defer tick.Stop()
			for !s.dead.Load() {
				<-tick.C
				if time.Since(time.Unix(0, s.heard.Load())) > time.Duration(misses)*s.beat {
					s.die()
					return
				}
			}
		}()
	}
	for s.stream.Receive() {
		s.heard.Store(time.Now().UnixNano())
		if c := s.stream.Msg().GetChanged(); c != nil {
			s.told(c.GetEntries())
		}
	}
}

func (s *watchSession) told(entries []uint64) {
	s.mu.Lock()
	var told []keeper
	for _, e := range entries {
		if k, ok := s.entries[e]; ok {
			told = append(told, k)
			delete(s.entries, e)
		}
	}
	s.mu.Unlock()
	for _, k := range told {
		k.told()
	}
}

// die ends the session and everything kept through it.
func (s *watchSession) die() {
	if s.dead.Swap(true) {
		return
	}
	s.cancel()
	s.mu.Lock()
	held := s.entries
	s.entries = map[uint64]keeper{}
	s.mu.Unlock()
	for _, k := range held {
		k.told()
	}
	s.client.Close()
}
