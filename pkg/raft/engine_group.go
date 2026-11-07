package raft

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"path"
	"slices"
	"sync"
	"time"

	hraft "github.com/hashicorp/raft"
	"github.com/libp2p/go-libp2p/core/peer"
	etcdraft "go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
)

// machine is what a group applies its log to.
type machine interface {
	Apply(*hraft.Log) interface{}
	Restore(io.ReadCloser) error
	state() ([]byte, error)
}

// groupObserver hears what the healer watches for: the leader changing, and a vote
// asked of this node by one its group does not hold.
type groupObserver interface {
	leaderChanged(leader peer.ID)
	foreignVote(from peer.ID)
}

// groupConfig is how a group runs.
type groupConfig struct {
	timeouts TimeoutConfig
	machine  machine
}

// group is one raft group on a node's engine. Its raft belongs to the loop; what
// other goroutines read of it is cached under mu after each pass.
type group struct {
	e         *engine
	namespace string
	rn        *etcdraft.RawNode
	log       *engineLog
	fsm       machine

	tickEvery     time.Duration
	nextTick      time.Time
	snapshotEvery uint64
	trailing      uint64
	snapIndex     uint64
	applied       uint64
	conf          *pb.ConfState

	// confChanged is a membership change applied since the last snapshot.
	confChanged bool

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
	log, err := openEngineLog(e.ctx, e.node.Store(), path.Join(RaftStoragePrefix, namespace, "engine"))
	if err != nil {
		return nil, err
	}
	g := &group{
		e:             e,
		namespace:     namespace,
		log:           log,
		fsm:           cfg.machine,
		tickEvery:     max(time.Millisecond, cfg.timeouts.HeartbeatTimeout/electionTicks),
		snapshotEvery: cfg.timeouts.SnapshotThreshold,
		trailing:      cfg.timeouts.TrailingLogs,
		pending:       map[uint64]chan FSMResponse{},
		reads:         map[uint64]chan uint64{},
		state:         Follower,
	}
	g.nextTick = time.Now().Add(g.tickEvery)

	// WHAT THE SNAPSHOT HOLDS IS WHERE APPLYING RESUMES: the machine is set back to
	// it, and raft hands over every committed entry after it.
	snap, err := log.Snapshot()
	if err != nil {
		return nil, err
	}
	g.conf = &pb.ConfState{}
	if !etcdraft.IsEmptySnap(snap) {
		if err := g.fsm.Restore(io.NopCloser(bytes.NewReader(snap.GetData()))); err != nil {
			return nil, fmt.Errorf("raft: %s: restoring its machine: %w", namespace, err)
		}
		g.snapIndex = snap.GetMetadata().GetIndex()
		g.applied = g.snapIndex
		g.conf = proto.Clone(snap.GetMetadata().GetConfState()).(*pb.ConfState)
	} else if _, cs, err := log.InitialState(); err == nil && cs != nil {
		g.conf = proto.Clone(cs).(*pb.ConfState)
	}

	rn, err := etcdraft.NewRawNode(&etcdraft.Config{
		ID:                        e.self,
		ElectionTick:              electionTicks,
		HeartbeatTick:             heartbeatTicks,
		Storage:                   log,
		Applied:                   g.applied,
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
	g.appliedCache = g.applied
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

// apply hands the machine what committed, answers who waits, and caches what other
// goroutines read.
func (g *group) apply(rd etcdraft.Ready) {
	if snap := rd.Snapshot; snap != nil && !etcdraft.IsEmptySnap(snap) {
		if err := g.fsm.Restore(io.NopCloser(bytes.NewReader(snap.GetData()))); err != nil {
			engineLogger.Errorf("[%s] %s: restoring a snapshot: %v", g.e.node.ID().ShortString(), g.namespace, err)
		}
		g.snapIndex = snap.GetMetadata().GetIndex()
		g.applied = g.snapIndex
		g.conf = proto.Clone(snap.GetMetadata().GetConfState()).(*pb.ConfState)
		g.refreshMembers()
	}

	for _, ent := range rd.CommittedEntries {
		switch ent.GetType() {
		case pb.EntryType_EntryNormal:
			if len(ent.GetData()) == 0 {
				break
			}
			p := &raftv2.Proposal{}
			if err := proto.Unmarshal(ent.GetData(), p); err != nil {
				break
			}
			resp, _ := g.fsm.Apply(&hraft.Log{Index: ent.GetIndex(), Term: ent.GetTerm(), Type: hraft.LogCommand, Data: p.GetCommand()}).(FSMResponse)
			g.resolve(p.GetId(), resp)
		case pb.EntryType_EntryConfChange:
			cc := &pb.ConfChange{}
			if err := proto.Unmarshal(ent.GetData(), cc); err != nil {
				break
			}
			if len(cc.GetContext()) > 0 {
				if p, err := peer.IDFromBytes(cc.GetContext()); err == nil {
					g.e.book.learn(p)
				}
			}
			g.conf = g.rn.ApplyConfChange(cc)
			g.confChanged = true
			g.refreshMembers()
			g.resolve(cc.GetId(), FSMResponse{})
		case pb.EntryType_EntryConfChangeV2:
			cc := &pb.ConfChangeV2{}
			if err := proto.Unmarshal(ent.GetData(), cc); err != nil {
				break
			}
			g.conf = g.rn.ApplyConfChange(cc)
			g.confChanged = true
			g.refreshMembers()
		}
		g.applied = ent.GetIndex()
	}

	g.mu.Lock()
	for _, rs := range rd.ReadStates {
		if len(rs.RequestCtx) == 8 {
			if ch, ok := g.reads[binary.BigEndian.Uint64(rs.RequestCtx)]; ok {
				ch <- rs.Index
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
	g.appliedCache = g.applied
	kept := g.waits[:0]
	for _, w := range g.waits {
		if w.index <= g.applied {
			close(w.done)
		} else {
			kept = append(kept, w)
		}
	}
	g.waits = kept
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

// compact snapshots the machine once enough has applied since the last snapshot,
// and after a membership change once the log has been compacted: a member joining
// has to be sent a snapshot that names it, or it refuses it.
func (g *group) compact() {
	due := g.snapshotEvery > 0 && g.applied >= g.snapIndex+g.snapshotEvery
	if g.confChanged && g.snapIndex > 0 && g.applied > g.snapIndex {
		due = true
	}
	if !due {
		return
	}
	data, err := g.fsm.state()
	if err != nil {
		engineLogger.Errorf("[%s] %s: reading its machine for a snapshot: %v", g.e.node.ID().ShortString(), g.namespace, err)
		return
	}
	if err := g.log.compact(g.e.ctx, g.applied, g.conf, data, g.trailing); err != nil {
		engineLogger.Errorf("[%s] %s: compacting its log: %v", g.e.node.ID().ShortString(), g.namespace, err)
		return
	}
	g.snapIndex = g.applied
	g.confChanged = false
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

// Propose replicates cmd and answers what the machine said when it applied it.
func (g *group) Propose(cmd []byte, timeout time.Duration) (FSMResponse, error) {
	id := rand.Uint64() | 1
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
	id := rand.Uint64() | 1
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
	id := rand.Uint64() | 1
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
	if err := g.e.do(readAsk{g: g, ctx: ctx}); err != nil {
		return err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var index uint64
	select {
	case i, ok := <-ch:
		if !ok {
			return g.failedOr(ErrShutdown)
		}
		index = i
	case <-timer.C:
		if _, ok := g.Leader(); !ok {
			return ErrNoLeader
		}
		return ErrApplyTimeout
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
	if len(a.peers) == 1 && a.peers[0] == e.node.ID() {
		if err := a.g.rn.Campaign(); err != nil {
			a.reply <- err
			return
		}
	}
	a.reply <- nil
}

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
			if m.Voter && m.ID != g.e.node.ID() {
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

// Close stops the group on the engine; what it kept stays on disk.
func (g *group) Close() error {
	a := closeAsk{g: g, done: make(chan struct{})}
	if err := g.e.do(a); err != nil {
		return nil
	}
	<-a.done
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
	a.g.shut(ErrShutdown)
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
	lead := g.lead
	g.mu.Unlock()
	if lead == 0 {
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
