package raft

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

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

// engines is every running engine, one per node, so a node's groups share one. A
// node is how a caller names its engine; the entry goes when its last group closes.
var engines = struct {
	sync.Mutex
	on map[taupeer.Node]*engine
}{on: map[taupeer.Node]*engine{}}

// engineFor is node's engine, started on first use, and holds it until release.
func engineFor(node taupeer.Node) *engine {
	engines.Lock()
	defer engines.Unlock()
	e, ok := engines.on[node]
	if !ok {
		e = newEngine(node)
		engines.on[node] = e
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
		delete(engines.on, e.node)
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

	// groups is the loop's own.
	groups map[string]*group

	// refs is under engines' lock.
	refs int
}

// delivery is one message a peer sent a group.
type delivery struct {
	from  peer.ID
	group string
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
				g.shut(ErrShutdown)
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
func (e *engine) cycle() {
	var out []readied
	for _, g := range e.groups {
		if g.rn.HasReady() {
			out = append(out, readied{g, g.rn.Ready()})
		}
	}
	if len(out) == 0 {
		return
	}

	if err := e.persist(out); err != nil {
		// What raft handed over cannot be handed back: a group whose state did not
		// reach disk stops rather than run on promises it did not keep.
		engineLogger.Errorf("[%s] writing raft state: %v", e.node.ID().ShortString(), err)
		for _, r := range out {
			r.g.shut(fmt.Errorf("raft: %s: writing its state: %w", r.g.namespace, err))
			delete(e.groups, r.g.namespace)
		}
		return
	}

	frames := map[peer.ID]*raftv2.Frame{}
	for _, r := range out {
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

	for _, r := range out {
		r.g.apply(r.rd)
		r.g.rn.Advance(r.rd)
		r.g.compact()
	}
}

func (e *engine) persist(out []readied) error {
	b, err := e.node.Store().Batch(e.ctx)
	if err != nil {
		return err
	}
	for _, r := range out {
		if err := r.g.log.save(e.ctx, b, r.rd.Snapshot, r.rd.HardState, r.rd.Entries); err != nil {
			return err
		}
	}
	return b.Commit(e.ctx)
}

// received is the wire handing over what a peer sent.
func (e *engine) received(from peer.ID, frame *raftv2.Frame) {
	e.book.learn(from)
	for _, env := range frame.GetEnvelopes() {
		m := &pb.Message{}
		if err := proto.Unmarshal(env.GetMessage(), m); err != nil {
			continue
		}
		select {
		case e.inbox <- delivery{from: from, group: env.GetGroup(), msg: m}:
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
