package raft

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	mrand "math/rand/v2"
	"path"
	"slices"
	"testing"
	"testing/cryptotest"
	"time"

	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
)

// The simulation runs a cloud of engines on one goroutine: the clock, the network
// and every apply are stepped by the test, and every random choice — the network's,
// the faults', raft's own election jitter and the proposal ids — comes from one
// seed. A failure names its seed, and running that seed again runs the same cloud.

// simClock is the time the simulation says it is.
type simClock struct{ now time.Time }

func (c *simClock) Now() time.Time { return c.now }

// steppedApply leaves a group's committed work queued for the simulation to apply.
type steppedApply struct{}

func (steppedApply) start(*group) {}

// applyQueued applies what a group has queued, in order, on the caller.
func applyQueued(g *group) {
	a := g.applier
	for {
		a.mu.Lock()
		if len(a.queue) == 0 {
			a.mu.Unlock()
			return
		}
		item := a.queue[0]
		a.queue = a.queue[1:]
		a.mu.Unlock()
		g.applyItem(item)
	}
}

// simFrame is a frame in flight, and the round its sender sent it in.
type simFrame struct {
	from, to peer.ID
	round    int
	frame    *raftv2.Frame
}

type simNet struct {
	rng      *mrand.Rand
	inflight []simFrame
	round    int
	drop     float64
	// side is which side of a partition each node is on; equal sides talk.
	side map[peer.ID]int
}

type simWire struct {
	net  *simNet
	from peer.ID
}

func (w simWire) send(to peer.ID, f *raftv2.Frame) {
	w.net.inflight = append(w.net.inflight, simFrame{from: w.from, to: to, round: w.net.round, frame: proto.Clone(f).(*raftv2.Frame)})
}

func (simWire) close() {}

// settle orders what a node sent in one pass. The engine builds a pass's frames
// from maps, whose order Go randomises on purpose; ordering them here keeps the
// seed in charge without changing what any one group sent, or in what order.
func (n *simNet) settle(from int) {
	tail := n.inflight[from:]
	for _, f := range tail {
		slices.SortStableFunc(f.frame.Envelopes, func(a, b *raftv2.Envelope) int {
			switch {
			case a.GetGroup() < b.GetGroup():
				return -1
			case a.GetGroup() > b.GetGroup():
				return 1
			}
			return 0
		})
	}
	slices.SortStableFunc(tail, func(a, b simFrame) int {
		switch {
		case a.to < b.to:
			return -1
		case a.to > b.to:
			return 1
		}
		return 0
	})
}

// simGroup is one group a node runs, and what its machine applied at each index
// across every incarnation of the node.
type simGroup struct {
	name    string
	g       *group
	applied map[uint64]string
}

type recorder struct {
	FSM
	t     testing.TB
	where string
	sg    *simGroup
}

func (r recorder) Apply(index uint64, cmd []byte) FSMResponse {
	if prev, ok := r.sg.applied[index]; ok && prev != string(cmd) {
		r.t.Fatalf("%s applied two commands at index %d", r.where, index)
	}
	r.sg.applied[index] = string(cmd)
	return r.FSM.Apply(index, cmd)
}

type simNode struct {
	id     peer.ID
	store  ds.Batching
	e      *engine
	groups map[string]*simGroup
	up     bool
	// still is a node whose loop takes no ticks, as a stalled loop takes none.
	still bool
}

// simWrite is a write asked of a leader and not yet answered.
type simWrite struct {
	group, key string
	ch         chan FSMResponse
	node       *simNode
}

type sim struct {
	t      testing.TB
	rng    *mrand.Rand
	clock  *simClock
	net    *simNet
	nodes  []*simNode
	layout map[string][]int
	timing TimeoutConfig

	writes  []simWrite
	acked   map[string]map[string]bool
	leaders map[string]map[uint64]peer.ID
	trace   [32]byte
	n       int
}

func newSim(t *testing.T, seed uint64, nodes int, layout map[string][]int) *sim {
	return newSimTimed(t, seed, nodes, layout, 50*time.Millisecond)
}

// newSimTimed is a simulation whose groups take heartbeat as their heartbeat
// timeout, of which a tick is a tenth: a round is engineResolution whatever it is.
func newSimTimed(t *testing.T, seed uint64, nodes int, layout map[string][]int, heartbeat time.Duration) *sim {
	cryptotest.SetGlobalRandom(t, seed)
	s := &sim{
		t:       t,
		rng:     mrand.New(mrand.NewPCG(seed, seed^0x9e3779b97f4a7c15)),
		clock:   &simClock{now: time.Unix(1_700_000_000, 0)},
		layout:  layout,
		timing:  TimeoutConfig{HeartbeatTimeout: heartbeat, SnapshotThreshold: 64, TrailingLogs: 16},
		acked:   map[string]map[string]bool{},
		leaders: map[string]map[uint64]peer.ID{},
	}
	s.net = &simNet{rng: s.rng, side: map[peer.ID]int{}}
	for range nodes {
		key, _, err := crypto.GenerateEd25519Key(rand.Reader)
		require.NoError(t, err)
		id, err := peer.IDFromPrivateKey(key)
		require.NoError(t, err)
		s.nodes = append(s.nodes, &simNode{id: id, store: dssync.MutexWrap(ds.NewMapDatastore()), groups: map[string]*simGroup{}})
	}
	for _, n := range s.nodes {
		s.start(n)
	}
	for _, name := range s.groupNames() {
		founder := s.nodes[layout[name][0]]
		var members []peer.ID
		for _, i := range layout[name] {
			members = append(members, s.nodes[i].id)
		}
		a := bootstrapAsk{g: founder.groups[name].g, peers: members, reply: make(chan error, 1)}
		a.run(founder.e)
		for _, i := range layout[name][1:] {
			s.bind(s.nodes[i], name, founder.groups[name].g.Formation())
		}
		s.acked[name] = map[string]bool{}
		s.leaders[name] = map[uint64]peer.ID{}
	}
	return s
}

// bind binds n's group to formation, as a member that asked for an offer does.
func (s *sim) bind(n *simNode, name string, formation []byte) {
	a := bindAsk{g: n.groups[name].g, formation: formation, reply: make(chan error, 1)}
	a.run(n.e)
	require.NoError(s.t, <-a.reply)
}

func (s *sim) groupNames() []string {
	names := make([]string, 0, len(s.layout))
	for name := range s.layout {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// start runs n on a fresh engine over what its store kept, as a node does after a
// crash: nothing in memory survives.
func (s *sim) start(n *simNode) {
	n.e = newEngineOn(context.Background(), n.id, s.clock, steppedApply{})
	n.e.wire = simWire{net: s.net, from: n.id}
	for _, name := range s.groupNames() {
		if !slices.ContainsFunc(s.layout[name], func(i int) bool { return s.nodes[i] == n }) {
			continue
		}
		sg, ok := n.groups[name]
		if !ok {
			sg = &simGroup{name: name, applied: map[uint64]string{}}
			n.groups[name] = sg
		}
		machine := recorder{
			FSM:   newKVFSM(context.Background(), n.store, path.Join(RaftStoragePrefix, name)),
			t:     s.t,
			where: fmt.Sprintf("%s on %s", name, n.id.ShortString()),
			sg:    sg,
		}
		a := openAsk{namespace: name, cfg: groupConfig{timeouts: s.timing, machine: machine, store: n.store}, reply: make(chan openResult, 1)}
		a.run(n.e)
		r := <-a.reply
		require.NoError(s.t, r.err)
		sg.g = r.g
	}
	n.up = true
}

// crash stops n where it stands. afterPersist crashes it after its last pass was
// written and before what it sent left: its frames from this round are lost.
func (s *sim) crash(n *simNode, afterPersist bool) {
	n.up = false
	n.e.cancel()
	kept := s.net.inflight[:0]
	for _, f := range s.net.inflight {
		if f.to == n.id || (afterPersist && f.from == n.id && f.round == s.net.round) {
			continue
		}
		kept = append(kept, f)
	}
	s.net.inflight = kept
	writes := s.writes[:0]
	for _, w := range s.writes {
		if w.node != n {
			writes = append(writes, w)
		}
	}
	s.writes = writes
}

// deliver hands over what the network lets through this round: each frame in
// flight is delivered, dropped, or held for a later round.
func (s *sim) deliver() {
	held := s.net.inflight[:0]
	var now, lost []simFrame
	for _, f := range s.net.inflight {
		switch {
		case s.net.side[f.from] != s.net.side[f.to], s.rng.Float64() < s.net.drop:
			lost = append(lost, f)
		case s.rng.IntN(4) == 0:
			held = append(held, f)
		default:
			now = append(now, f)
		}
	}
	s.net.inflight = held
	for _, f := range lost {
		s.reported(f, false)
	}
	s.rng.Shuffle(len(now), func(i, j int) { now[i], now[j] = now[j], now[i] })
	for _, f := range now {
		if to := s.node(f.to); to != nil && to.up {
			to.e.received(f.from, f.frame)
			s.reported(f, true)
		} else {
			s.reported(f, false)
		}
	}
}

// reported tells the sender what became of a frame carrying a snapshot, as the
// wire does: raft holds a follower it sent one to until it hears.
func (s *sim) reported(f simFrame, delivered bool) {
	if !slices.ContainsFunc(f.frame.GetEnvelopes(), (*raftv2.Envelope).GetSnapshot) {
		return
	}
	if from := s.node(f.from); from != nil && from.up {
		reportOf(f.to, f.frame, delivered).run(from.e)
	}
}

func (s *sim) node(id peer.ID) *simNode {
	for _, n := range s.nodes {
		if n.id == id {
			return n
		}
	}
	return nil
}

// step is one round: the clock moves, the network delivers, and each node that is
// up takes one pass of its loop.
func (s *sim) step() {
	s.net.round++
	s.clock.now = s.clock.now.Add(engineResolution)
	s.deliver()
	for _, n := range s.nodes {
		if !n.up {
			continue
		}
		// IN ORDER: every group's election jitter is drawn from one stream, so the
		// order groups tick in is part of what the seed decides.
		for _, name := range s.groupNames() {
			if sg, ok := n.groups[name]; ok && !n.still {
				sg.g.tick(s.clock.now)
			}
		}
		n.e.drain()
		from := len(s.net.inflight)
		n.e.cycle()
		s.net.settle(from)
		for _, name := range s.groupNames() {
			if sg, ok := n.groups[name]; ok {
				applyQueued(sg.g)
			}
		}
	}
	s.observe()
}

// observe checks election safety and folds each group's state into the trace.
func (s *sim) observe() {
	h := sha256.New()
	h.Write(s.trace[:])
	for _, n := range s.nodes {
		for _, name := range s.groupNames() {
			sg, ok := n.groups[name]
			if !ok || !n.up {
				continue
			}
			state, term := sg.g.State(), sg.g.Term()
			fmt.Fprintf(h, "%d %s %s %d %d %d|", s.net.round, n.id, name, state, term, sg.g.CommitIndex())
			if state != Leader {
				continue
			}
			if was, ok := s.leaders[name][term]; ok && was != n.id {
				s.t.Fatalf("%s has two leaders in term %d: %s and %s", name, term, was.ShortString(), n.id.ShortString())
			}
			s.leaders[name][term] = n.id
		}
	}
	copy(s.trace[:], h.Sum(nil))
}

// note folds an event into the trace.
func (s *sim) note(format string, args ...any) {
	h := sha256.New()
	h.Write(s.trace[:])
	fmt.Fprintf(h, format, args...)
	copy(s.trace[:], h.Sum(nil))
}

// propose asks name's leader, if it has one up, for a write, without waiting.
func (s *sim) propose(name string) {
	for _, i := range s.layout[name] {
		n := s.nodes[i]
		if !n.up || n.groups[name].g.State() != Leader {
			continue
		}
		s.proposeOn(n, name)
		return
	}
}

// proposeOn asks n for a write to name, without waiting, and says whether n took it.
func (s *sim) proposeOn(n *simNode, name string) bool {
	g := n.groups[name].g
	s.n++
	key := fmt.Sprintf("k%d", s.n)
	cmd, err := encodeSetCommand(key, []byte(key))
	require.NoError(s.t, err)
	id := randomID()
	data, err := proto.Marshal(&raftv2.Proposal{Id: id, Command: cmd})
	require.NoError(s.t, err)
	ch := make(chan FSMResponse, 1)
	require.NoError(s.t, g.await(id, ch))
	a := proposeAsk{g: g, data: data, reply: make(chan error, 1)}
	a.run(n.e)
	if err := <-a.reply; err != nil {
		g.forget(id)
		return false
	}
	s.writes = append(s.writes, simWrite{group: name, key: key, ch: ch, node: n})
	return true
}

// answered collects the writes that were acknowledged.
func (s *sim) answered() {
	waiting := s.writes[:0]
	for _, w := range s.writes {
		select {
		case resp, ok := <-w.ch:
			if ok && resp.Error == nil {
				s.acked[w.group][w.key] = true
			}
		default:
			waiting = append(waiting, w)
		}
	}
	s.writes = waiting
}

// chaos runs rounds of drops, delays, partitions and crashes, writing as it goes.
func (s *sim) chaos(rounds int) {
	s.net.drop = 0.05
	for range rounds {
		switch r := s.rng.Float64(); {
		case r < 0.004:
			for _, n := range s.nodes {
				s.net.side[n.id] = s.rng.IntN(2)
			}
		case r < 0.012:
			clear(s.net.side)
		case r < 0.016:
			if n := s.nodes[s.rng.IntN(len(s.nodes))]; n.up {
				after := s.rng.IntN(2) == 0
				s.note("crash %s %v", n.id, after)
				s.crash(n, after)
			}
		case r < 0.030:
			if n := s.nodes[s.rng.IntN(len(s.nodes))]; !n.up {
				s.note("start %s", n.id)
				s.start(n)
			}
		}
		if s.rng.IntN(3) == 0 {
			names := s.groupNames()
			s.propose(names[s.rng.IntN(len(names))])
		}
		s.step()
		s.answered()
	}
}

// heal brings every node back on a network that loses nothing, and runs until
// every acknowledged write is on every member of its group.
func (s *sim) heal(rounds int) {
	s.net.drop = 0
	clear(s.net.side)
	for _, n := range s.nodes {
		if !n.up {
			s.start(n)
		}
	}
	for range rounds {
		s.step()
		s.answered()
		if s.converged() {
			return
		}
	}
	for _, name := range s.groupNames() {
		for key := range s.acked[name] {
			for _, i := range s.layout[name] {
				if _, found := s.nodes[i].groups[name].g.fsm.Get(key); !found {
					s.t.Fatalf("%s acknowledged %s, and %s never applied it", name, key, s.nodes[i].id.ShortString())
				}
			}
		}
	}
}

func (s *sim) converged() bool {
	for _, name := range s.groupNames() {
		for key := range s.acked[name] {
			for _, i := range s.layout[name] {
				if _, found := s.nodes[i].groups[name].g.fsm.Get(key); !found {
					return false
				}
			}
		}
	}
	return true
}

// agreed checks that every member of a group applied the same command wherever two
// applied the same index.
func (s *sim) agreed() {
	for _, name := range s.groupNames() {
		at := map[uint64]string{}
		for _, i := range s.layout[name] {
			for index, cmd := range s.nodes[i].groups[name].applied {
				if was, ok := at[index]; ok && was != cmd {
					s.t.Fatalf("%s applied two commands at index %d on different members", name, index)
				}
				at[index] = cmd
			}
		}
	}
}

// simLayout is four groups over five nodes: three of three members, overlapping,
// and one of all five.
var simLayout = map[string][]int{
	"a": {0, 1, 2},
	"b": {1, 2, 3},
	"c": {2, 3, 4},
	"d": {0, 1, 2, 3, 4},
}

// THE SAME SEED RUNS THE SAME CLOUD: what every node of every group was, round by
// round, comes out the same twice.
func TestASimulationSeedReplays(t *testing.T) {
	run := func(t *testing.T) [32]byte {
		s := newSim(t, 7, 5, simLayout)
		s.chaos(1500)
		return s.trace
	}
	var first, second [32]byte
	t.Run("first", func(t *testing.T) { first = run(t) })
	t.Run("second", func(t *testing.T) { second = run(t) })
	require.Equal(t, first, second, "one seed ran two different clouds")
}

// simStall is a group of the given size whose last member joins with a store
// that refuses its raft state, once the rest have formed under a leader: the
// write naming that leader is the first the store refuses.
type simStall struct {
	*sim
	late  *simNode
	store *faultStore
	first *simNode
}

const simStallVote = "/raft/a/engine/hardstate"

func newSimStall(t *testing.T, size int) simStall {
	members := make([]int, size)
	for i := range members {
		members[i] = i
	}
	s := simStall{sim: newSim(t, 1, size, map[string][]int{"a": members})}
	s.late = s.nodes[size-1]
	s.crash(s.late, false)
	for range 2000 {
		if s.first = s.leading(); s.first != nil && s.formed() {
			break
		}
		s.step()
	}
	require.True(t, s.first != nil && s.formed(), "the group never formed")

	s.store = newFaultStore()
	s.store.failPut(simStallVote, errNoSpace)
	s.late.store = s.store
	s.start(s.late)
	s.bind(s.late, "a", s.first.groups["a"].g.Formation())
	g := s.lateGroup()
	for range 2000 {
		if g.stalled != nil && g.stalled.SoftState != nil && g.stalled.SoftState.Lead != 0 {
			break
		}
		s.step()
	}
	require.NotNil(t, g.stalled, "the late member's store never refused it")
	heard, _ := s.late.e.book.peer(g.stalled.SoftState.Lead)
	require.Equal(t, s.first.id, heard)
	return s
}

func (s simStall) lateGroup() *group { return s.late.groups["a"].g }

// leading is the member other than the late one that leads, if one does.
func (s simStall) leading() *simNode {
	for _, n := range s.nodes {
		if n != s.late && n.up && n.groups["a"].g.State() == Leader {
			return n
		}
	}
	return nil
}

// formed says every member that is up holds the group's configuration.
func (s simStall) formed() bool {
	for _, n := range s.nodes {
		if n != s.late && n.up && len(n.groups["a"].g.conf.GetVoters()) != len(s.nodes) {
			return false
		}
	}
	return true
}

// recovered steps until the late member's store has taken its state.
func (s simStall) recovered() {
	s.store.failPut(simStallVote, nil)
	for range 2000 {
		if s.lateGroup().stalled == nil {
			return
		}
		s.step()
	}
	s.t.Fatalf("the late member never wrote its state")
}

// A MEMBER WHOSE STORE COMES BACK NAMES THE LEADER IT KNOWS NOW, not the one it
// heard when its write was refused: a join asked of it would be forwarded to a
// leader long gone.
func TestAStalledMemberNamesNoLeaderItHeardBeforeTheStall(t *testing.T) {
	s := newSimStall(t, 5)
	g := s.lateGroup()

	s.crash(s.first, false)
	var second *simNode
	for range 2000 {
		if second = s.leading(); second != nil {
			if now, _ := s.late.e.book.peer(g.rn.BasicStatus().Lead); now == second.id {
				break
			}
		}
		s.step()
	}
	require.NotNil(t, second, "the rest never elected another leader")

	s.recovered()
	named, _ := g.Leader()
	require.NotEqual(t, s.first.id, named, "the late member names the leader it heard before its write was refused")
}

// A GROUP OF THREE WHOSE LEADER DIES WHILE A MEMBER'S STORE REFUSES ITS STATE
// LEADS AGAIN ONCE THE STORE COMES BACK, with nobody to restart anything: one
// member of three cannot elect, and the one that could not write its vote takes
// part again the moment a write lands.
func TestAGroupLeadsAgainOnceAStalledMembersStoreComesBack(t *testing.T) {
	s := newSimStall(t, 3)
	g := s.lateGroup()

	s.crash(s.first, false)
	for range 400 {
		s.step()
	}
	require.Nil(t, s.leading(), "one member of three elected itself")

	s.recovered()
	var second *simNode
	for range 4000 {
		if second = s.leading(); second != nil {
			if now, _ := g.Leader(); now == second.id {
				break
			}
		}
		s.step()
	}
	require.NotNil(t, second, "the group never led again once the store came back")
	named, _ := g.Leader()
	require.Equal(t, second.id, named, "the member whose store came back never followed the new leader")
	s.propose("a")
	for range 2000 {
		s.step()
		s.answered()
		if len(s.acked["a"]) > 0 {
			break
		}
	}
	require.NotEmpty(t, s.acked["a"], "the group led again and never committed a write")
}

// UNDER DROPS, DELAYS, REORDERING, PARTITIONS AND CRASHES — between a pass's write
// and its send among them — no group ever has two leaders in a term, no index ever
// applies two commands, and every write a leader acknowledged is on every member
// once the network heals.
func TestAGroupKeepsItsPromisesUnderFaults(t *testing.T) {
	seeds := 12
	if testing.Short() {
		seeds = 3
	}
	for seed := range uint64(seeds) {
		t.Run(fmt.Sprintf("seed %d", seed+1), func(t *testing.T) {
			s := newSim(t, seed+1, 5, simLayout)
			s.chaos(4000)
			s.heal(4000)
			s.agreed()
			total := 0
			for _, keys := range s.acked {
				total += len(keys)
			}
			require.NotZero(t, total, "nothing was acknowledged, so nothing was proven")
		})
	}
}

// lose crashes n and starts it again on an empty store under its own id, bound to
// each formation it was a member of, as a node whose disk was replaced binds to the
// offer it asks for: its groups still list it, and their leaders hold the progress
// of the log it lost.
func (s *sim) lose(n *simNode) {
	formations := map[string][]byte{}
	for name, sg := range n.groups {
		formations[name] = sg.g.Formation()
	}
	s.note("lose %s", n.id)
	s.crash(n, false)
	n.store = dssync.MutexWrap(ds.NewMapDatastore())
	s.start(n)
	for _, name := range s.groupNames() {
		if f, ok := formations[name]; ok {
			s.bind(n, name, f)
		}
	}
}

// leading is the member of name that is up and leads, if one does.
func (s *sim) leading(name string) *simNode {
	for _, i := range s.layout[name] {
		if n := s.nodes[i]; n.up && n.groups[name].g.State() == Leader {
			return n
		}
	}
	return nil
}

// written steps until name has acknowledged writes writes, every member has
// applied them, and a member leads, and returns that leader.
func (s *sim) written(name string, writes int) *simNode {
	for range 4000 {
		if len(s.acked[name]) < writes {
			s.propose(name)
		}
		s.step()
		s.answered()
		if len(s.acked[name]) >= writes && s.converged() {
			if l := s.leading(name); l != nil {
				return l
			}
		}
	}
	s.t.Fatalf("%s never applied %d writes on every member", name, writes)
	return nil
}

// otherThan is a member of name other than n.
func (s *sim) otherThan(name string, n *simNode) *simNode {
	for _, i := range s.layout[name] {
		if s.nodes[i] != n {
			return s.nodes[i]
		}
	}
	return nil
}

// A MEMBER OF TWO BACK WITH ITS LOG LOST, UNDER ITS OWN ID, IS CAUGHT UP, written to
// or left alone. Its leader holds the progress of the log it lost, and raft would
// send it a commit past the end of its log; it answers that leader nothing, and the
// leader's quorum, which needs it, ends the term.
func TestAMemberOfTwoBackWithItsLogLostIsCaughtUp(t *testing.T) {
	for _, writing := range []bool{false, true} {
		t.Run(fmt.Sprintf("writing %v", writing), func(t *testing.T) {
			s := newSim(t, 3, 2, map[string][]int{"a": {0, 1}})
			leader := s.written("a", 4)
			lost := s.otherThan("a", leader)
			before := len(s.acked["a"])
			s.lose(lost)

			caughtUp := false
			for range 4000 {
				if writing && s.rng.IntN(4) == 0 {
					s.propose("a")
				}
				s.step()
				s.answered()
				if caughtUp = s.converged() && (!writing || len(s.acked["a"]) > before); caughtUp {
					break
				}
			}
			require.True(t, caughtUp, "the member back with its log lost was never caught up")
			s.agreed()
		})
	}
}

// A MEMBER BACK WITH ITS LOG LOST, IN A GROUP WHOSE QUORUM DOES WITHOUT IT, IS CAUGHT
// UP ONCE THE LEAD MOVES: the leader leads on with the rest, holding the progress of
// the log the member lost, and the member answers it nothing until the leader it
// names in its place leads.
func TestAMemberBackWithItsLogLostIsCaughtUpOnceTheLeadMoves(t *testing.T) {
	s := newSim(t, 5, 3, map[string][]int{"a": {0, 1, 2}})
	leader := s.written("a", 4)
	lost := s.otherThan("a", leader)
	s.lose(lost)
	for range 400 {
		s.step()
	}
	require.Same(t, leader, s.leading("a"), "the leader stopped leading with the rest")
	require.False(t, s.converged(), "the leader caught up a member it holds stale progress for")

	ask := func(of *simNode, last uint64) standIn {
		a := standInAsk{g: of.groups["a"].g, joiner: raftID(lost.id), last: last, reply: make(chan standIn, 1)}
		a.run(of.e)
		return <-a.reply
	}
	var third *simNode
	for _, i := range s.layout["a"] {
		if n := s.nodes[i]; n != leader && n != lost {
			third = n
		}
	}
	require.False(t, ask(third, 0).stale, "a follower names a stand-in")
	require.False(t, ask(leader, ^uint64(0)).stale, "a joiner holding everything it acknowledged is stale")
	in := ask(leader, lost.groups["a"].g.LastIndex())
	require.True(t, in.stale)
	require.Equal(t, third.id, in.to, "the lead is not handed to the member that is caught up")

	transferAsk{g: leader.groups["a"].g, to: raftID(in.to)}.run(leader.e)
	for range 4000 {
		s.step()
		s.answered()
		if s.converged() {
			break
		}
	}
	require.True(t, s.converged(), "the member back with its log lost was never caught up once the lead moved")
	require.Equal(t, in.to, s.leading("a").id)
	s.agreed()
}
