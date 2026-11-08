package raft

import (
	"sync"

	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
)

// A group applies what committed on a goroutine of its own, in order, as etcd's own
// server does: a machine slow to write, or stuck on its disk, holds up its group and
// no other on the node. Membership changes are applied by the loop, which alone may
// tell raft; snapshots are restored and taken here, between applies, so a snapshot
// is always the machine at the index it names.

// applyItem is one pass's committed work for the applier, in log order.
type applyItem struct {
	// snap is a snapshot raft installed: the machine is set back to it first.
	snap *pb.Snapshot
	// entries are the commands to apply; last is the index the pass reached,
	// membership changes included.
	entries []*pb.Entry
	last    uint64
	// conf is the membership as of last, which a snapshot taken at last names.
	conf        *pb.ConfState
	confChanged bool
	// snapshot is a snapshot asked for, answered once taken.
	snapshot chan error
}

// applier is a group's applying goroutine and what waits for it.
type applier struct {
	mu      sync.Mutex
	queue   []applyItem
	stopped bool
	wake    chan struct{}
	done    chan struct{}

	// The applier's own: what the machine holds, and the snapshot behind it.
	applied     uint64
	snapIndex   uint64
	confChanged bool
}

func newApplier(applied uint64) *applier {
	return &applier{wake: make(chan struct{}, 1), done: make(chan struct{}), applied: applied, snapIndex: applied}
}

// enqueue hands item over; it never blocks the loop.
func (a *applier) enqueue(item applyItem) {
	a.mu.Lock()
	if a.stopped {
		a.mu.Unlock()
		if item.snapshot != nil {
			item.snapshot <- ErrShutdown
		}
		return
	}
	a.queue = append(a.queue, item)
	a.mu.Unlock()
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

// stop lets the applier finish what it is applying and drop the rest; done closes
// once it has.
func (a *applier) stop() {
	a.mu.Lock()
	a.stopped = true
	dropped := a.queue
	a.queue = nil
	a.mu.Unlock()
	for _, item := range dropped {
		if item.snapshot != nil {
			item.snapshot <- ErrShutdown
		}
	}
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

func (a *applier) next() (applyItem, bool) {
	for {
		a.mu.Lock()
		if a.stopped {
			a.mu.Unlock()
			return applyItem{}, false
		}
		if len(a.queue) > 0 {
			item := a.queue[0]
			a.queue[0] = applyItem{}
			a.queue = a.queue[1:]
			a.mu.Unlock()
			return item, true
		}
		a.mu.Unlock()
		<-a.wake
	}
}

// runApplier applies what the loop hands over until the group stops.
func (g *group) runApplier() {
	defer close(g.applier.done)
	for {
		item, ok := g.applier.next()
		if !ok {
			return
		}
		g.applyItem(item)
	}
}

func (g *group) applyItem(item applyItem) {
	a := g.applier
	if snap := item.snap; snap != nil {
		if err := g.fsm.Restore(snap.GetData()); err != nil {
			engineLogger.Errorf("[%s] %s: restoring a snapshot: %v", g.e.node.ID().ShortString(), g.namespace, err)
		}
		a.applied = snap.GetMetadata().GetIndex()
		a.snapIndex = a.applied
	}
	for _, ent := range item.entries {
		p := &raftv2.Proposal{}
		if err := proto.Unmarshal(ent.GetData(), p); err != nil {
			continue
		}
		g.resolve(p.GetId(), g.fsm.Apply(ent.GetIndex(), p.GetCommand()))
	}
	if item.last > a.applied {
		a.applied = item.last
	}
	if item.confChanged {
		a.confChanged = true
	}

	g.mu.Lock()
	g.appliedCache = a.applied
	kept := g.waits[:0]
	for _, w := range g.waits {
		if w.index <= a.applied {
			close(w.done)
		} else {
			kept = append(kept, w)
		}
	}
	g.waits = kept
	g.mu.Unlock()

	if item.snapshot != nil {
		item.snapshot <- g.snapshotAt(item.conf)
		return
	}
	// A MEMBER JOINING is sent a snapshot that names it, or it refuses it: once the
	// log has been compacted, a membership change is snapshotted at once.
	due := g.snapshotEvery > 0 && a.applied >= a.snapIndex+g.snapshotEvery
	if a.confChanged && a.snapIndex > 0 && a.applied > a.snapIndex {
		due = true
	}
	if due && item.conf != nil {
		if err := g.snapshotAt(item.conf); err != nil {
			engineLogger.Errorf("[%s] %s: %v", g.e.node.ID().ShortString(), g.namespace, err)
		}
	}
}
