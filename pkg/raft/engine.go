package raft

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sync"
	"time"

	ds "github.com/ipfs/go-datastore"
	logging "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/peer"
	etcdraft "go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	taupeer "github.com/taubyte/tau/p2p/peer"
	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
)

// The engine is one node's raft: every group the services on a node open runs in
// it, on one loop. Each pass of the loop takes what every group has ready, writes it
// in one batch, then sends it, applies what committed, and advances, so a node's
// groups share one write and one stream to each peer however many there are.

var engineLogger = logging.Logger("raft-engine")

const (
	// engineResolution is how often the loop looks at the clock; each group ticks
	// at its own interval, a multiple of it.
	engineResolution = 5 * time.Millisecond

	// electionTicks and heartbeatTicks are raft's timing in ticks: a group's tick is
	// its heartbeat timeout over electionTicks.
	electionTicks  = 10
	heartbeatTicks = 1

	// inboxDepth is how many received messages wait for the loop before the
	// streams reading them wait too.
	inboxDepth = 4096
)

var (
	// ErrLeadershipLost is a write its leader stopped leading before it was known
	// to commit; the next leader may still commit it.
	ErrLeadershipLost = errors.New("leadership lost while committing")

	// ErrApplyTimeout is a write or a barrier that did not apply in its time.
	ErrApplyTimeout = errors.New("timed out waiting to apply")

	// ErrCantBootstrap is a group asked to bootstrap that already holds a log.
	ErrCantBootstrap = errors.New("bootstrap only works on a group with no log")

	// ErrTransferFailed is leadership that did not move when asked to.
	ErrTransferFailed = errors.New("leadership transfer did not complete")

	// ErrGroupOpen is a namespace a node already runs a group for.
	ErrGroupOpen = errors.New("this node already runs a group for the namespace")
)

// engines is every running engine, one per host, so a node's groups share one and
// the host's engine stream has one reader. The entry goes when its last group
// closes.
var engines = struct {
	sync.Mutex
	on map[peer.ID]*engine
}{on: map[peer.ID]*engine{}}

// engineFor is node's engine, started on first use, and holds it until release.
func engineFor(node taupeer.Node) *engine {
	engines.Lock()
	defer engines.Unlock()
	e, ok := engines.on[node.ID()]
	if !ok {
		e = newEngine(node)
		engines.on[node.ID()] = e
	}
	e.refs++
	return e
}

// release lets go of e; the last group to let go stops it.
func (e *engine) release() {
	engines.Lock()
	e.refs--
	last := e.refs == 0
	if last {
		delete(engines.on, e.node.ID())
	}
	engines.Unlock()
	if last {
		e.stop()
	}
}

type engine struct {
	node   taupeer.Node
	self   uint64
	ctx    context.Context
	cancel context.CancelFunc
	wire   *engineWire
	book   peerBook

	inbox chan delivery
	asks  chan ask
	done  chan struct{}

	// groups is the loop's own; stopping is what it stopped on its way out, whose
	// appliers stop waits for.
	groups   map[string]*group
	stopping []*group

	// refs is under engines' lock.
	refs int
}

// delivery is one message a peer sent a group, as it arrived: the group opens and
// reads it, being the one that holds the key.
type delivery struct {
	from  peer.ID
	group string
	raw   []byte
	msg   *pb.Message
}

// ask is work for the loop, which alone touches a group's raft.
type ask interface {
	run(e *engine)
}

func newEngine(node taupeer.Node) *engine {
	ctx, cancel := context.WithCancel(node.Context())
	e := &engine{
		node:   node,
		self:   raftID(node.ID()),
		ctx:    ctx,
		cancel: cancel,
		inbox:  make(chan delivery, inboxDepth),
		asks:   make(chan ask),
		done:   make(chan struct{}),
		groups: map[string]*group{},
	}
	e.book.learn(node.ID())
	e.wire = newEngineWire(ctx, node.Host(), e)
	go e.loop()
	return e
}

func (e *engine) stop() {
	e.cancel()
	<-e.done
	for _, g := range e.stopping {
		<-g.applier.done
	}
	e.wire.close()
}

// do runs a on the loop and waits for it, or for the engine to stop.
func (e *engine) do(a ask) error {
	select {
	case e.asks <- a:
		return nil
	case <-e.done:
		return ErrShutdown
	}
}

func (e *engine) loop() {
	defer close(e.done)
	ticker := time.NewTicker(engineResolution)
	defer ticker.Stop()
	for {
		select {
		case <-e.ctx.Done():
			for _, g := range e.groups {
				g.answerDurable(ErrShutdown)
				g.applier.stop()
				g.shut(ErrShutdown)
				e.stopping = append(e.stopping, g)
			}
			e.groups = nil
			return
		case now := <-ticker.C:
			for _, g := range e.groups {
				g.tick(now)
			}
		case d := <-e.inbox:
			e.deliver(d)
			e.drain()
		case a := <-e.asks:
			a.run(e)
		}
		e.cycle()
	}
}

// drain takes what else arrived, so one pass handles a burst.
func (e *engine) drain() {
	for range inboxDepth {
		select {
		case d := <-e.inbox:
			e.deliver(d)
		default:
			return
		}
	}
}

func (e *engine) deliver(d delivery) {
	g, ok := e.groups[d.group]
	if !ok {
		return
	}
	raw := d.raw
	if g.cipher != nil {
		plain, err := unseal(g.cipher, raw)
		if err != nil {
			engineLogger.Debugf("[%s] %s: a message from %s that does not open: %v", e.node.ID().ShortString(), d.group, d.from.ShortString(), err)
			return
		}
		raw = plain
	}
	d.msg = &pb.Message{}
	if err := proto.Unmarshal(raw, d.msg); err != nil {
		return
	}
	g.heard(d)
	if err := g.rn.Step(d.msg); err != nil && !errors.Is(err, etcdraft.ErrStepPeerNotFound) && !errors.Is(err, etcdraft.ErrStepLocalMsg) {
		engineLogger.Debugf("[%s] %s: step %s from %s: %v", e.node.ID().ShortString(), d.group, d.msg.GetType(), d.from.ShortString(), err)
	}
}

// readied is one group's output of one pass.
type readied struct {
	g  *group
	rd etcdraft.Ready
}

// cycle writes, sends, applies and advances what every group has ready. Nothing is
// sent before it is written: a vote or an acknowledgement a crash could take back
// is a broken promise.
//
// A WRITE THE STORE REFUSES IS MADE AGAIN, not given up on: the group keeps what it
// could not write and stalls, sending and applying nothing, until a retry lands. A
// disk that comes back brings the group back with it, with nobody to restart it.
func (e *engine) cycle() {
	now := time.Now()
	var out []readied
	for _, g := range e.groups {
		switch {
		case g.stalled != nil:
			if now.Before(g.retryAt) {
				continue
			}
			out = append(out, readied{g, *g.stalled})
		case g.rn.HasReady():
			out = append(out, readied{g, g.rn.Ready()})
		}
	}
	if len(out) == 0 {
		return
	}

	written := e.persist(out)

	frames := map[peer.ID]*raftv2.Frame{}
	for _, r := range written {
		for _, m := range r.rd.Messages {
			to, ok := e.book.peer(m.GetTo())
			if !ok {
				r.g.rn.ReportUnreachable(m.GetTo())
				continue
			}
			raw, err := proto.Marshal(m)
			if err != nil {
				continue
			}
			if r.g.cipher != nil {
				if raw, err = seal(r.g.cipher, raw); err != nil {
					continue
				}
			}
			f, ok := frames[to]
			if !ok {
				f = &raftv2.Frame{}
				frames[to] = f
			}
			f.Envelopes = append(f.Envelopes, &raftv2.Envelope{
				Group:    r.g.namespace,
				Message:  raw,
				Snapshot: m.GetType() == pb.MessageType_MsgSnap,
			})
		}
	}
	for to, f := range frames {
		e.wire.send(to, f)
	}

	for _, r := range written {
		r.g.apply(r.rd)
		r.g.rn.Advance(r.rd)
	}
}

// persist writes every group's pass in one batch, and on a refusal each group's in
// a batch of its own, so one group the store refuses holds up no other. It answers
// the passes that were written; the rest stall and are tried again.
func (e *engine) persist(out []readied) []readied {
	if err := e.write(out); err == nil {
		for _, r := range out {
			r.g.written(r.rd, nil)
		}
		return out
	}
	var written []readied
	for _, r := range out {
		err := e.write([]readied{r})
		r.g.written(r.rd, err)
		if err == nil {
			written = append(written, r)
		}
	}
	return written
}

// write commits out in one batch per store: a node keeps one, so in practice one
// batch, but a group is written where its own log lives.
func (e *engine) write(out []readied) error {
	batches := map[ds.Batching]ds.Batch{}
	for _, r := range out {
		b, ok := batches[r.g.log.store]
		if !ok {
			var err error
			if b, err = r.g.log.store.Batch(e.ctx); err != nil {
				return err
			}
			batches[r.g.log.store] = b
		}
		if err := r.g.log.save(e.ctx, b, r.rd.Snapshot, r.rd.HardState, r.rd.Entries); err != nil {
			return err
		}
	}
	for _, b := range batches {
		if err := b.Commit(e.ctx); err != nil {
			return err
		}
	}
	for _, r := range out {
		if err := r.g.log.kept(r.rd.Snapshot, r.rd.HardState, r.rd.Entries); err != nil {
			return err
		}
	}
	return nil
}

// received is the wire handing over what a peer sent.
func (e *engine) received(from peer.ID, frame *raftv2.Frame) {
	e.book.learn(from)
	for _, env := range frame.GetEnvelopes() {
		select {
		case e.inbox <- delivery{from: from, group: env.GetGroup(), raw: env.GetMessage()}:
		case <-e.ctx.Done():
			return
		}
	}
}

// unreachable is the wire failing to hand a frame to its peer: raft hears so for
// each group in it, and a snapshot in it failed.
func (e *engine) unreachable(to peer.ID, frame *raftv2.Frame) {
	e.report(to, frame, false)
}

// sent is a frame handed to its peer: a snapshot in it was delivered.
func (e *engine) sent(to peer.ID, frame *raftv2.Frame) {
	for _, env := range frame.GetEnvelopes() {
		if env.GetSnapshot() {
			e.report(to, frame, true)
			return
		}
	}
}

func (e *engine) report(to peer.ID, frame *raftv2.Frame, delivered bool) {
	r := wireReport{to: raftID(to), delivered: delivered}
	for _, env := range frame.GetEnvelopes() {
		r.groups = append(r.groups, env.GetGroup())
		r.snapshots = append(r.snapshots, env.GetSnapshot())
	}
	select {
	case e.asks <- r:
	case <-e.done:
	}
}

// wireReport tells raft what became of a frame.
type wireReport struct {
	to        uint64
	delivered bool
	groups    []string
	snapshots []bool
}

func (r wireReport) run(e *engine) {
	for i, ns := range r.groups {
		g, ok := e.groups[ns]
		if !ok {
			continue
		}
		switch {
		case r.snapshots[i] && r.delivered:
			g.rn.ReportSnapshot(r.to, etcdraft.SnapshotFinish)
		case r.snapshots[i]:
			g.rn.ReportSnapshot(r.to, etcdraft.SnapshotFailure)
		case !r.delivered:
			g.rn.ReportUnreachable(r.to)
		}
	}
}

// raftID is the number raft knows a peer by: the first eight bytes of its id's
// hash, never zero, which raft reserves for none.
func raftID(p peer.ID) uint64 {
	sum := sha256.Sum256([]byte(p))
	if id := binary.BigEndian.Uint64(sum[:8]); id != 0 {
		return id
	}
	return 1
}

// peerBook is which peer each raft number is, learned from the peers that speak
// and the members a configuration names.
type peerBook struct {
	mu  sync.RWMutex
	ids map[uint64]peer.ID
}

func (b *peerBook) learn(p peer.ID) uint64 {
	id := raftID(p)
	b.mu.Lock()
	if b.ids == nil {
		b.ids = map[uint64]peer.ID{}
	}
	b.ids[id] = p
	b.mu.Unlock()
	return id
}

func (b *peerBook) peer(id uint64) (peer.ID, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	p, ok := b.ids[id]
	return p, ok
}
