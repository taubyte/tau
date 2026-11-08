package raft

import (
	"bytes"
	"context"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"path"
	"slices"
	"sync"
	"time"

	ds "github.com/ipfs/go-datastore"
	"github.com/libp2p/go-libp2p/core/peer"
	etcdraft "go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
)

// groupObserver hears what the healer watches for: the leader changing, and a vote
// asked of this node by one its group does not hold.
type groupObserver interface {
	leaderChanged(leader peer.ID)
	foreignVote(from peer.ID)
}

// groupConfig is how a group runs. cipher, when set, seals what the group sends:
// a peer without the key reads none of it.
type groupConfig struct {
	timeouts TimeoutConfig
	machine  FSM
	cipher   cipher.AEAD
	// store is where the group keeps its log: its own node's, which need not be
	// the store of the node that started the host's engine.
	store ds.Batching
}

// group is one raft group on a node's engine. Its raft belongs to the loop; what
// other goroutines read of it is cached under mu after each pass.
type group struct {
	e         *engine
	namespace string
	rn        *etcdraft.RawNode
	log       *engineLog
	fsm       FSM
	cipher    cipher.AEAD

	tickEvery     time.Duration
	nextTick      time.Time
	snapshotEvery uint64
	trailing      uint64
	conf          *pb.ConfState
	applier       *applier

	// durable is who waits for what raft took to reach disk: a bootstrap answers
	// once its first entries are written.
	durable []chan error

	// stalled is a pass the store refused, tried again from retryAt, backing off
	// up to maxRetry; nothing else is taken from raft meanwhile.
	stalled *etcdraft.Ready
	retryAt time.Time
	backoff time.Duration

	mu           sync.Mutex
	state        State
	lead         uint64
	term         uint64
	commit       uint64
	last         uint64
	appliedCache uint64
	members      []Member
	pending      map[uint64]chan FSMResponse
	reads        map[uint64]chan uint64
	waits        []indexWait
	observers    []groupObserver
	err          error
	formation    []byte
	frozenUntil  time.Time
}

type indexWait struct {
	index uint64
	done  chan struct{}
}

// open starts namespace's group on the engine, from what the node kept of it.
func (e *engine) open(namespace string, cfg groupConfig) (*group, error) {
	a := openAsk{namespace: namespace, cfg: cfg, reply: make(chan openResult, 1)}
	if err := e.do(a); err != nil {
		return nil, err
	}
	r := <-a.reply
	return r.g, r.err
}

type openResult struct {
	g   *group
	err error
}

type openAsk struct {
	namespace string
	cfg       groupConfig
	reply     chan openResult
}

func (a openAsk) run(e *engine) {
	if _, ok := e.groups[a.namespace]; ok {
		a.reply <- openResult{err: fmt.Errorf("%w: %s", ErrGroupOpen, a.namespace)}
		return
	}
	g, err := e.newGroup(a.namespace, a.cfg)
	if err == nil {
		e.groups[a.namespace] = g
	}
	a.reply <- openResult{g: g, err: err}
}

func (e *engine) newGroup(namespace string, cfg groupConfig) (*group, error) {
	log, err := openEngineLog(e.ctx, cfg.store, path.Join(RaftStoragePrefix, namespace, "engine"))
	if err != nil {
		return nil, err
	}
	g := &group{
		e:             e,
		namespace:     namespace,
		log:           log,
		fsm:           cfg.machine,
		cipher:        cfg.cipher,
		tickEvery:     max(time.Millisecond, cfg.timeouts.HeartbeatTimeout/electionTicks),
		snapshotEvery: cfg.timeouts.SnapshotThreshold,
		trailing:      cfg.timeouts.TrailingLogs,
		pending:       map[uint64]chan FSMResponse{},
		reads:         map[uint64]chan uint64{},
		state:         Follower,
	}
	g.nextTick = e.clock.Now().Add(g.tickEvery)
	g.formation = log.formation

	// WHAT THE SNAPSHOT HOLDS IS WHERE APPLYING RESUMES: the machine is set back to
	// it, and raft hands over every committed entry after it.
	snap, err := log.Snapshot()
	if err != nil {
		return nil, err
	}
	g.conf = &pb.ConfState{}
	var applied uint64
	if !etcdraft.IsEmptySnap(snap) {
		machine, err := g.openSnapshot(snap)
		if err != nil {
			return nil, fmt.Errorf("raft: %s: reading its snapshot: %w", namespace, err)
		}
		if err := g.fsm.Restore(machine); err != nil {
			return nil, fmt.Errorf("raft: %s: restoring its machine: %w", namespace, err)
		}
		applied = snap.GetMetadata().GetIndex()
		g.conf = proto.Clone(snap.GetMetadata().GetConfState()).(*pb.ConfState)
	} else if _, cs, err := log.InitialState(); err == nil && cs != nil {
		g.conf = proto.Clone(cs).(*pb.ConfState)
	}

	rn, err := etcdraft.NewRawNode(&etcdraft.Config{
		ID:                        e.self,
		ElectionTick:              electionTicks,
		HeartbeatTick:             heartbeatTicks,
		Storage:                   log,
		Applied:                   applied,
		MaxSizePerMsg:             1 << 20,
		MaxInflightMsgs:           256,
		CheckQuorum:               true,
		PreVote:                   true,
		ReadOnlyOption:            etcdraft.ReadOnlySafe,
		DisableProposalForwarding: true,
		StepDownOnRemoval:         true,
		Logger:                    etcdLogger{},
	})
	if err != nil {
		return nil, err
	}
	g.rn = rn
	g.refreshMembers()
	hs, _, _ := log.InitialState()
	g.term, g.commit = hs.GetTerm(), hs.GetCommit()
	g.last, _ = log.LastIndex()
	g.appliedCache = applied
	g.applier = newApplier(applied)
	e.applying.start(g)
	return g, nil
}

// tick advances raft's clock once the group's interval has passed.
func (g *group) tick(now time.Time) {
	for !now.Before(g.nextTick) {
		g.rn.Tick()
		g.nextTick = g.nextTick.Add(g.tickEvery)
	}
}

// heard looks at a message before raft does: a vote asked by a node the group does
// not hold is how a second group under the same name shows itself.
func (g *group) heard(d delivery) {
	switch d.msg.GetType() {
	case pb.MessageType_MsgVote, pb.MessageType_MsgPreVote:
	default:
		return
	}
	if len(g.conf.GetVoters()) == 0 || slices.Contains(g.conf.GetVoters(), d.msg.GetFrom()) {
		return
	}
	g.mu.Lock()
	observers := slices.Clone(g.observers)
	g.mu.Unlock()
	for _, o := range observers {
		o.foreignVote(d.from)
	}
}

// foreignFormation is a message from a member bound to another formation of the
// group: two groups formed under one name, which the healer reconciles.
func (g *group) foreignFormation(from peer.ID) {
	g.mu.Lock()
	observers := slices.Clone(g.observers)
	g.mu.Unlock()
	for _, o := range observers {
		o.foreignVote(from)
	}
}

// apply hands the applier what committed, applies membership changes itself, and
// caches what other goroutines read.
func (g *group) apply(rd etcdraft.Ready) {
	var item applyItem
	if snap := rd.Snapshot; snap != nil && !etcdraft.IsEmptySnap(snap) {
		machine, err := g.openSnapshot(snap)
		if err != nil {
			engineLogger.Errorf("[%s] %s: reading a snapshot: %v", g.e.id.ShortString(), g.namespace, err)
		}
		item.snap, item.machine = snap, machine
		g.conf = proto.Clone(snap.GetMetadata().GetConfState()).(*pb.ConfState)
		g.refreshMembers()
	}

	for _, ent := range rd.CommittedEntries {
		switch ent.GetType() {
		case pb.EntryType_EntryNormal:
			if len(ent.GetData()) > 0 {
				item.entries = append(item.entries, ent)
			}
		case pb.EntryType_EntryConfChange, pb.EntryType_EntryConfChangeV2:
			cc, id, err := confChangeOf(ent)
			if err != nil {
				break
			}
			if ctx := cc.AsV2().GetContext(); len(ctx) > 0 {
				if p, err := peer.IDFromBytes(ctx); err == nil {
					g.e.book.learn(p)
				}
			}
			g.conf = g.rn.ApplyConfChange(cc)
			item.confChanged = true
			g.refreshMembers()
			g.resolve(id, FSMResponse{})
		}
		item.last = ent.GetIndex()
	}
	if item.snap != nil || item.last > 0 {
		item.conf = proto.Clone(g.conf).(*pb.ConfState)
		g.applier.enqueue(item)
	}

	g.mu.Lock()
	for _, rs := range rd.ReadStates {
		if len(rs.RequestCtx) == 8 {
			// A read asked again can be answered twice; the first answer is the one
			// waited for, and the loop never waits on a reader.
			if ch, ok := g.reads[binary.BigEndian.Uint64(rs.RequestCtx)]; ok {
				select {
				case ch <- rs.Index:
				default:
				}
			}
		}
	}
	leaderMoved := false
	if ss := rd.SoftState; ss != nil {
		leaderMoved = ss.Lead != g.lead
		g.lead = ss.Lead
		g.state = stateOf(ss.RaftState)
	}
	if hs := rd.HardState; hs != nil && !etcdraft.IsEmptyHardState(hs) {
		g.term, g.commit = hs.GetTerm(), hs.GetCommit()
	}
	g.last, _ = g.log.LastIndex()
	observers := slices.Clone(g.observers)
	lead := g.lead
	g.mu.Unlock()

	if leaderMoved {
		leader, _ := g.e.book.peer(lead)
		for _, o := range observers {
			o.leaderChanged(leader)
		}
	}
}

func stateOf(s etcdraft.StateType) State {
	switch s {
	case etcdraft.StateLeader:
		return Leader
	case etcdraft.StateCandidate, etcdraft.StatePreCandidate:
		return Candidate
	}
	return Follower
}

func (g *group) resolve(id uint64, resp FSMResponse) {
	if id == 0 {
		return
	}
	g.mu.Lock()
	ch, ok := g.pending[id]
	delete(g.pending, id)
	g.mu.Unlock()
	if ok {
		ch <- resp
	}
}

func (g *group) refreshMembers() {
	members := make([]Member, 0, len(g.conf.GetVoters())+len(g.conf.GetLearners()))
	for _, id := range g.conf.GetVoters() {
		if p, ok := g.e.book.peer(id); ok {
			members = append(members, Member{ID: p, Voter: true})
		}
	}
	for _, id := range g.conf.GetLearners() {
		if p, ok := g.e.book.peer(id); ok {
			members = append(members, Member{ID: p})
		}
	}
	g.mu.Lock()
	g.members = members
	g.mu.Unlock()
}

// confChangeOf is the membership change ent carries, and the id of the change
// asked for, which only a change of this group's own making has.
func confChangeOf(ent *pb.Entry) (pb.ConfChangeI, uint64, error) {
	if ent.GetType() == pb.EntryType_EntryConfChangeV2 {
		cc := &pb.ConfChangeV2{}
		return cc, 0, proto.Unmarshal(ent.GetData(), cc)
	}
	cc := &pb.ConfChange{}
	if err := proto.Unmarshal(ent.GetData(), cc); err != nil {
		return nil, 0, err
	}
	return cc, cc.GetId(), nil
}

// membersOf is every member conf names, by the peer id raft's numbers stand for.
func (g *group) membersOf(conf *pb.ConfState) [][]byte {
	var out [][]byte
	for _, ids := range [][]uint64{conf.GetVoters(), conf.GetLearners(), conf.GetVotersOutgoing(), conf.GetLearnersNext()} {
		for _, id := range ids {
			if p, ok := g.e.book.peer(id); ok {
				out = append(out, []byte(p))
			}
		}
	}
	return out
}

// openSnapshot is the machine a snapshot holds, its members learned first: raft's
// configuration names them only by number, and a member nobody can name is one no
// vote or entry reaches.
func (g *group) openSnapshot(snap *pb.Snapshot) ([]byte, error) {
	data := &raftv2.SnapshotData{}
	if err := proto.Unmarshal(snap.GetData(), data); err != nil {
		return nil, err
	}
	for _, m := range data.GetMembers() {
		if p, err := peer.IDFromBytes(m); err == nil {
			g.e.book.learn(p)
		}
	}
	return data.GetMachine(), nil
}

// snapshotAt keeps the machine as the applier has applied it, naming conf, and
// compacts the log behind it. It runs on the applier, between applies.
func (g *group) snapshotAt(conf *pb.ConfState) error {
	a := g.applier
	if a.applied <= a.snapIndex {
		return nil
	}
	machine, err := g.fsm.Snapshot()
	if err != nil {
		return fmt.Errorf("reading its machine for a snapshot: %w", err)
	}
	data, err := proto.Marshal(&raftv2.SnapshotData{Members: g.membersOf(conf), Machine: machine})
	if err != nil {
		return err
	}
	if err := g.log.compact(g.e.ctx, a.applied, conf, data, g.trailing); err != nil {
		return fmt.Errorf("compacting its log: %w", err)
	}
	a.snapIndex = a.applied
	a.confChanged = false
	return nil
}

// Snapshot keeps the machine as it has applied now, whatever the threshold says.
func (g *group) Snapshot() error {
	a := snapshotAsk{g: g, reply: make(chan error, 1)}
	if err := g.e.do(a); err != nil {
		return err
	}
	return <-a.reply
}

type snapshotAsk struct {
	g     *group
	reply chan error
}

func (a snapshotAsk) run(*engine) {
	a.g.applier.enqueue(applyItem{snapshot: a.reply, conf: proto.Clone(a.g.conf).(*pb.ConfState)})
}

// maxRetry is the longest a stalled group waits between tries.
const maxRetry = time.Second

// written records how writing rd went: a refused pass stalls the group, and a
// write that lands clears the stall and answers who waited for it.
func (g *group) written(rd etcdraft.Ready, err error) {
	if err == nil {
		if g.stalled != nil {
			engineLogger.Infof("[%s] %s: its state is written again", g.e.id.ShortString(), g.namespace)
		}
		g.stalled, g.backoff = nil, 0
		g.answerDurable(nil)
		return
	}
	if g.stalled == nil {
		engineLogger.Errorf("[%s] %s: writing its state: %v; it stalls until a write lands", g.e.id.ShortString(), g.namespace, err)
		g.answerDurable(fmt.Errorf("raft: %s: writing its state: %w", g.namespace, err))
	}
	g.stalled = &rd
	g.backoff = min(max(2*g.backoff, engineResolution), maxRetry)
	g.retryAt = g.e.clock.Now().Add(g.backoff)
}

// answerDurable tells who waited for this pass to be written how it went.
func (g *group) answerDurable(err error) {
	for _, ch := range g.durable {
		ch <- err
	}
	g.durable = nil
}

// shut fails everything waiting on the group.
func (g *group) shut(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.err != nil {
		return
	}
	g.err = err
	for id, ch := range g.pending {
		close(ch)
		delete(g.pending, id)
	}
	for id, ch := range g.reads {
		close(ch)
		delete(g.reads, id)
	}
	for _, w := range g.waits {
		close(w.done)
	}
	g.waits = nil
	g.state = Shutdown
}

func (g *group) failed() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.err
}

// randomID names a proposal, a change or a read: never zero, and drawn so two
// nodes, or one node either side of a restart, never name two things alike.
func randomID() uint64 {
	var b [8]byte
	rand.Read(b[:])
	return binary.BigEndian.Uint64(b[:]) | 1
}

// Propose replicates cmd and answers what the machine said when it applied it.
func (g *group) Propose(cmd []byte, timeout time.Duration) (FSMResponse, error) {
	if g.frozen() {
		return FSMResponse{}, ErrFrozen
	}
	id := randomID()
	data, err := proto.Marshal(&raftv2.Proposal{Id: id, Command: cmd})
	if err != nil {
		return FSMResponse{}, err
	}
	ch := make(chan FSMResponse, 1)
	if err := g.await(id, ch); err != nil {
		return FSMResponse{}, err
	}
	a := proposeAsk{g: g, data: data, reply: make(chan error, 1)}
	return g.settle(id, ch, a, timeout)
}

// await registers ch for the result of proposal id.
func (g *group) await(id uint64, ch chan FSMResponse) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.err != nil {
		return g.err
	}
	g.pending[id] = ch
	return nil
}

func (g *group) forget(id uint64) {
	g.mu.Lock()
	delete(g.pending, id)
	g.mu.Unlock()
}

// settle hands a to the loop and waits for proposal id's result.
func (g *group) settle(id uint64, ch chan FSMResponse, a replied, timeout time.Duration) (FSMResponse, error) {
	if err := g.e.do(a); err != nil {
		g.forget(id)
		return FSMResponse{}, err
	}
	if err := <-a.answer(); err != nil {
		g.forget(id)
		return FSMResponse{}, err
	}
	term := g.Term()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case resp, ok := <-ch:
		if !ok {
			return FSMResponse{}, g.failedOr(ErrShutdown)
		}
		return resp, nil
	case <-timer.C:
		g.forget(id)
		if g.Term() != term || g.State() != Leader {
			return FSMResponse{}, ErrLeadershipLost
		}
		return FSMResponse{}, ErrApplyTimeout
	}
}

func (g *group) failedOr(err error) error {
	if f := g.failed(); f != nil {
		return f
	}
	return err
}

// replied is an ask whose caller waits for whether raft took it.
type replied interface {
	ask
	answer() <-chan error
}

type proposeAsk struct {
	g     *group
	data  []byte
	reply chan error
}

func (a proposeAsk) answer() <-chan error { return a.reply }

func (a proposeAsk) run(*engine) {
	if a.g.State() != Leader {
		a.reply <- ErrNotLeader
		return
	}
	if err := a.g.rn.Propose(a.data); err != nil {
		if errors.Is(err, etcdraft.ErrProposalDropped) {
			err = ErrNotLeader
		}
		a.reply <- err
		return
	}
	a.reply <- nil
}

// AddVoter makes p a voting member, once the change has applied here.
func (g *group) AddVoter(p peer.ID, timeout time.Duration) error {
	return g.change(pb.ConfChangeType_ConfChangeAddNode, p, timeout)
}

// RemoveServer takes p out of the group.
func (g *group) RemoveServer(p peer.ID, timeout time.Duration) error {
	return g.change(pb.ConfChangeType_ConfChangeRemoveNode, p, timeout)
}

func (g *group) change(t pb.ConfChangeType, p peer.ID, timeout time.Duration) error {
	id := randomID()
	g.e.book.learn(p)
	ch := make(chan FSMResponse, 1)
	if err := g.await(id, ch); err != nil {
		return err
	}
	cc := &pb.ConfChange{Type: t.Enum(), NodeId: proto.Uint64(raftID(p)), Context: []byte(p), Id: proto.Uint64(id)}
	_, err := g.settle(id, ch, confAsk{g: g, cc: cc, reply: make(chan error, 1)}, timeout)
	return err
}

type confAsk struct {
	g     *group
	cc    *pb.ConfChange
	reply chan error
}

func (a confAsk) answer() <-chan error { return a.reply }

func (a confAsk) run(*engine) {
	if a.g.State() != Leader {
		a.reply <- ErrNotLeader
		return
	}
	if err := a.g.rn.ProposeConfChange(a.cc); err != nil {
		if errors.Is(err, etcdraft.ErrProposalDropped) {
			err = ErrNotLeader
		}
		a.reply <- err
		return
	}
	a.reply <- nil
}

// Barrier returns once this node has applied everything committed before it was
// asked: raft's read index, confirmed by a quorum, then applied here. No entry is
// written for it.
func (g *group) Barrier(timeout time.Duration) error {
	id := randomID()
	ch := make(chan uint64, 1)
	g.mu.Lock()
	if g.err != nil {
		g.mu.Unlock()
		return g.err
	}
	g.reads[id] = ch
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.reads, id)
		g.mu.Unlock()
	}()

	ctx := make([]byte, 8)
	binary.BigEndian.PutUint64(ctx, id)
	led := g.State() == Leader
	ask := readAsk{g: g, ctx: ctx}
	if err := g.e.do(ask); err != nil {
		return err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	// THE READ IS ASKED AGAIN until it is answered: raft sends a follower's read to
	// its leader, and the answer back, once, and a frame the wire could not
	// deliver takes either with it.
	again := time.NewTicker(g.tickEvery * electionTicks)
	defer again.Stop()
	var index uint64
	for answered := false; !answered; {
		select {
		case i, ok := <-ch:
			if !ok {
				return g.failedOr(ErrShutdown)
			}
			index, answered = i, true
		case <-again.C:
			if err := g.e.do(ask); err != nil {
				return err
			}
		case <-timer.C:
			switch _, known := g.Leader(); {
			case led && g.State() != Leader:
				// Raft drops the read of a leader that steps down.
				return ErrLeadershipLost
			case !known:
				return ErrNoLeader
			}
			return ErrApplyTimeout
		}
	}
	return g.appliedTo(index, timer.C)
}

// appliedTo waits until the machine has applied index.
func (g *group) appliedTo(index uint64, deadline <-chan time.Time) error {
	g.mu.Lock()
	if g.err != nil {
		g.mu.Unlock()
		return g.err
	}
	if g.appliedLocked() >= index {
		g.mu.Unlock()
		return nil
	}
	w := indexWait{index: index, done: make(chan struct{})}
	g.waits = append(g.waits, w)
	g.mu.Unlock()
	select {
	case <-w.done:
		return g.failed()
	case <-deadline:
		return ErrApplyTimeout
	}
}

// appliedLocked is the applied index as the loop last published it; mu is held.
func (g *group) appliedLocked() uint64 { return g.appliedCache }

type readAsk struct {
	g   *group
	ctx []byte
}

func (a readAsk) run(*engine) { a.g.rn.ReadIndex(a.ctx) }

// Bootstrap starts the group's log with peers as its voters, on a node that has
// none yet.
func (g *group) Bootstrap(peers []peer.ID) error {
	a := bootstrapAsk{g: g, peers: peers, reply: make(chan error, 1)}
	if err := g.e.do(a); err != nil {
		return err
	}
	return <-a.reply
}

type bootstrapAsk struct {
	g     *group
	peers []peer.ID
	reply chan error
}

func (a bootstrapAsk) run(e *engine) {
	if last, _ := a.g.log.LastIndex(); last > 0 {
		a.reply <- ErrCantBootstrap
		return
	}
	if hs, _, _ := a.g.log.InitialState(); !etcdraft.IsEmptyHardState(hs) {
		a.reply <- ErrCantBootstrap
		return
	}
	// A GROUP BOUND TO A FORMATION ALREADY belongs to one somebody else formed.
	if a.g.log.formation != nil {
		a.reply <- fmt.Errorf("%w: it is bound to a formation", ErrCantBootstrap)
		return
	}
	// THE FORMATION IS WRITTEN BEFORE ITS FIRST ENTRIES: a crash between the two
	// leaves a group bound to a formation it holds nothing of, which it may leave.
	if err := a.g.setFormation(newFormation()); err != nil {
		a.reply <- err
		return
	}
	peers := make([]etcdraft.Peer, 0, len(a.peers))
	for _, p := range a.peers {
		peers = append(peers, etcdraft.Peer{ID: e.book.learn(p), Context: []byte(p)})
	}
	if err := a.g.rn.Bootstrap(peers); err != nil {
		a.reply <- fmt.Errorf("%w: %w", ErrCantBootstrap, err)
		return
	}
	// A GROUP OF ONE ELECTS ITSELF at once rather than wait out a timeout nobody else
	// will interrupt.
	if len(a.peers) == 1 && a.peers[0] == e.id {
		if err := a.g.rn.Campaign(); err != nil {
			a.reply <- err
			return
		}
	}
	// ANSWERED ONCE WRITTEN: a bootstrap the store refuses is refused to its caller.
	a.g.durable = append(a.g.durable, a.reply)
}

// formationSize is how many random bytes name a formation.
const formationSize = 16

func newFormation() []byte {
	f := make([]byte, formationSize)
	rand.Read(f)
	return f
}

// setFormation binds the group to formation on disk, then in memory. The loop
// alone calls it.
func (g *group) setFormation(formation []byte) error {
	if err := g.log.bind(g.e.ctx, formation); err != nil {
		return err
	}
	g.mu.Lock()
	g.formation = formation
	g.mu.Unlock()
	return nil
}

// Freeze has the group take no write for d: it is handing its state to another
// group, and a write it took after the state was cut would be lost with it.
func (g *group) Freeze(d time.Duration) {
	g.mu.Lock()
	g.frozenUntil = g.e.clock.Now().Add(d)
	g.mu.Unlock()
}

func (g *group) frozen() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.e.clock.Now().Before(g.frozenUntil)
}

// Formation is the formation the group is bound to, nil while it is bound to none.
func (g *group) Formation() []byte {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.formation
}

// Bind binds a group that is bound to no formation to formation, durably, before
// it takes any message of it. A group bound to formation already is left alone;
// one bound to another refuses.
func (g *group) Bind(formation []byte) error {
	if len(formation) != formationSize {
		return fmt.Errorf("raft: %s: a formation is %d bytes, not %d", g.namespace, formationSize, len(formation))
	}
	a := bindAsk{g: g, formation: formation, reply: make(chan error, 1)}
	if err := g.e.do(a); err != nil {
		return err
	}
	return <-a.reply
}

type bindAsk struct {
	g         *group
	formation []byte
	reply     chan error
}

func (a bindAsk) run(*engine) {
	switch {
	case a.g.log.formation == nil:
		a.reply <- a.g.setFormation(slices.Clone(a.formation))
	case bytes.Equal(a.g.log.formation, a.formation):
		a.reply <- nil
	default:
		a.reply <- ErrBoundElsewhere
	}
}

// Pristine says the group holds nothing of any formation, so leaving the one it is
// bound to loses nothing: no entry, no snapshot, and nothing applied.
func (g *group) Pristine() bool {
	a := pristineAsk{g: g, reply: make(chan bool, 1)}
	if err := g.e.do(a); err != nil {
		return false
	}
	return <-a.reply
}

type pristineAsk struct {
	g     *group
	reply chan bool
}

func (a pristineAsk) run(*engine) { a.reply <- a.g.log.pristine() }

// TransferLeadership hands leadership to to, or to whichever voter is most caught
// up when to is empty.
func (g *group) TransferLeadership(to peer.ID) error {
	if g.State() != Leader {
		return ErrNotLeader
	}
	target := uint64(0)
	if to != "" {
		target = raftID(to)
	} else {
		g.mu.Lock()
		for _, m := range g.members {
			if m.Voter && m.ID != g.e.id {
				target = raftID(m.ID)
				break
			}
		}
		g.mu.Unlock()
	}
	if target == 0 {
		return ErrNoLeader
	}
	// RAFT GIVES UP a transfer its target does not catch up to within an election
	// timeout, so it is asked again each one until leadership has moved.
	election := g.tickEvery * electionTicks
	deadline := time.Now().Add(transferAttempts * election)
	for time.Now().Before(deadline) {
		if err := g.e.do(transferAsk{g: g, to: target}); err != nil {
			return err
		}
		asked := time.Now()
		for time.Since(asked) < election {
			if g.State() != Leader {
				return nil
			}
			time.Sleep(engineResolution)
		}
	}
	return ErrTransferFailed
}

// transferAttempts is how many election timeouts a transfer is asked for.
const transferAttempts = 20

type transferAsk struct {
	g  *group
	to uint64
}

func (a transferAsk) run(*engine) { a.g.rn.TransferLeader(a.to) }

// Observe adds o to what hears the group's leader and foreign votes.
func (g *group) Observe(o groupObserver) {
	g.mu.Lock()
	g.observers = append(g.observers, o)
	g.mu.Unlock()
}

// Close stops the group on the engine, once what it is applying has applied; what
// it kept stays on disk.
func (g *group) Close() error {
	a := closeAsk{g: g, done: make(chan struct{})}
	if err := g.e.do(a); err == nil {
		<-a.done
	}
	<-g.applier.done
	g.shut(ErrShutdown)
	return nil
}

type closeAsk struct {
	g    *group
	done chan struct{}
}

func (a closeAsk) run(e *engine) {
	if e.groups[a.g.namespace] == a.g {
		delete(e.groups, a.g.namespace)
	}
	a.g.answerDurable(ErrShutdown)
	a.g.applier.stop()
	close(a.done)
}

// Wipe deletes what the group kept, once it is closed.
func (g *group) Wipe(ctx context.Context) error { return g.log.wipe(ctx) }

func (g *group) State() State {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state
}

// Leader is the member raft last heard lead, and whether there is one.
func (g *group) Leader() (peer.ID, bool) {
	g.mu.Lock()
	lead, shut := g.lead, g.err != nil
	g.mu.Unlock()
	if lead == 0 || shut {
		return "", false
	}
	return g.e.book.peer(lead)
}

func (g *group) Term() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.term
}

func (g *group) CommitIndex() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.commit
}

// AppliedIndex is how far the machine has applied.
func (g *group) AppliedIndex() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.appliedCache
}

func (g *group) LastIndex() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.last
}

func (g *group) Members() []Member {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.members)
}

// etcdLogger is etcd raft's log, on tau's.
type etcdLogger struct{}

func (etcdLogger) Debug(v ...interface{})                 { engineLogger.Debug(v...) }
func (etcdLogger) Debugf(format string, v ...interface{}) { engineLogger.Debugf(format, v...) }
func (etcdLogger) Error(v ...interface{})                 { engineLogger.Error(v...) }
func (etcdLogger) Errorf(format string, v ...interface{}) { engineLogger.Errorf(format, v...) }
func (etcdLogger) Info(v ...interface{})                  { engineLogger.Debug(v...) }
func (etcdLogger) Infof(format string, v ...interface{})  { engineLogger.Debugf(format, v...) }
func (etcdLogger) Warning(v ...interface{})               { engineLogger.Warn(v...) }
func (etcdLogger) Warningf(format string, v ...interface{}) {
	engineLogger.Warnf(format, v...)
}
func (etcdLogger) Fatal(v ...interface{}) { panic(fmt.Sprint(v...)) }
func (etcdLogger) Fatalf(format string, v ...interface{}) {
	panic(fmt.Sprintf(format, v...))
}
func (etcdLogger) Panic(v ...interface{}) { panic(fmt.Sprint(v...)) }
func (etcdLogger) Panicf(format string, v ...interface{}) {
	panic(fmt.Sprintf(format, v...))
}
