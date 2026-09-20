package cspbft

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"
	"time"
)

// harness_test.go is the whole test network: a map of inboxes, a seeded
// scheduler and a fake signature scheme. There is no socket, no goroutine and
// no sleep in it, which is why "an equivocating primary against a partitioned
// backup at seed 41" is a table entry here rather than a flaky integration
// test.

// --- signatures -----------------------------------------------------------
//
// A signature is sha256(domain | id | preimage). It is not cryptography and is
// not meant to be: what the tests need is a function only the holder of an id
// computes correctly, so that forging is expressible.

func sigFor(domain, id string, pre []byte) []byte {
	h := sha256.New()
	h.Write([]byte(domain))
	h.Write([]byte{0})
	h.Write([]byte(id))
	h.Write([]byte{0})
	h.Write(pre)
	return h.Sum(nil)
}

func signAs(id ReplicaID) Signer {
	return func(pre []byte) ([]byte, error) { return sigFor("replica", string(id), pre), nil }
}

func replicaVerifier() Verifier {
	return func(from ReplicaID, pre, sig []byte) error {
		if string(sig) != string(sigFor("replica", string(from), pre)) {
			return fmt.Errorf("signature does not match %q", from)
		}
		return nil
	}
}

func clientVerifier() ClientVerifier {
	return func(from ClientID, pre, sig []byte) error {
		if string(sig) != string(sigFor("client", string(from), pre)) {
			return fmt.Errorf("signature does not match client %q", from)
		}
		return nil
	}
}

// resign recomputes a signature after a test has altered a message. A
// byzantine replica is exactly a replica that signs something it should not.
func resign(id ReplicaID, m Message) Message {
	m.Signature = sigFor("replica", string(id), Preimage(m))
	return m
}

// --- the network ----------------------------------------------------------

type envelope struct {
	from ReplicaID
	to   ReplicaID
	msg  Message
}

// mutator is how byzantine behaviour is expressed: it sees every message a
// replica is about to send and may change it, per recipient, or drop it.
type mutator func(s *sim, from, to ReplicaID, m Message) (Message, bool)

type sim struct {
	t   *testing.T
	ids []ReplicaID
	rep map[ReplicaID]*Replica
	rnd *rand.Rand
	k   uint64

	queue   []envelope
	mutate  map[ReplicaID]mutator
	down    map[ReplicaID]bool
	part    map[ReplicaID]bool
	timers  map[ReplicaID]map[Timer]time.Duration
	journal map[ReplicaID][]Record

	state    map[ReplicaID]map[Sequence]string
	lastExec map[ReplicaID]Sequence

	// held is the caller-side half of ErrFutureView: a message for a view this
	// replica has not entered is kept and re-stepped once it has. The package
	// says so in the error's own comment, and a caller that drops instead
	// deadlocks whenever a NEW-VIEW loses a race with the first PRE-PREPARE
	// the new primary sends after it.
	held map[ReplicaID][]envelope

	digests  []Digest
	slices   []slice
	notices  []error
	evidence []Equivocation
	steps    int
}

// slice is one action slice and the replica that produced it, so a durability
// failure names the replica that broke the contract rather than "someone".
type slice struct {
	who  ReplicaID
	acts []Action
}

type simOpt func(*simConfig)

type simConfig struct {
	k      uint64
	scorer Scorer
}

func withSimInterval(k uint64) simOpt { return func(c *simConfig) { c.k = k } }
func withSimScorer(s Scorer) simOpt   { return func(c *simConfig) { c.scorer = s } }

func newSim(t *testing.T, n int, seed int64, opts ...simOpt) *sim {
	t.Helper()
	cfg := &simConfig{k: 8, scorer: RoundRobin{}}
	for _, o := range opts {
		o(cfg)
	}
	ids := make([]ReplicaID, 0, n)
	for i := 0; i < n; i++ {
		ids = append(ids, ReplicaID(fmt.Sprintf("r%02d", i)))
	}
	s := &sim{
		t:        t,
		ids:      ids,
		rep:      map[ReplicaID]*Replica{},
		rnd:      rand.New(rand.NewSource(seed)),
		k:        cfg.k,
		mutate:   map[ReplicaID]mutator{},
		down:     map[ReplicaID]bool{},
		part:     map[ReplicaID]bool{},
		timers:   map[ReplicaID]map[Timer]time.Duration{},
		journal:  map[ReplicaID][]Record{},
		state:    map[ReplicaID]map[Sequence]string{},
		lastExec: map[ReplicaID]Sequence{},
		held:     map[ReplicaID][]envelope{},
	}
	for _, id := range ids {
		r, err := New(ids, id,
			WithSigner(signAs(id)),
			WithVerifier(replicaVerifier()),
			WithClientVerifier(clientVerifier()),
			WithScorer(cfg.scorer),
			WithCheckpointInterval(cfg.k),
			// The base timeout is a number this package does arithmetic on and
			// never compares to a clock, so one value serves every scenario:
			// the harness decides when a timer elapsed, not the duration.
			WithBaseTimeout(4*time.Second),
		)
		if err != nil {
			t.Fatalf("new replica %q: %v", id, err)
		}
		s.rep[id] = r
		s.timers[id] = map[Timer]time.Duration{}
		s.state[id] = map[Sequence]string{}
	}
	return s
}

// request builds a client request and hands it to every replica, which is what
// a client that cannot tell which replica is primary does.
func (s *sim) request(client ClientID, serial uint64, payload string) Message {
	m := Message{
		Kind:     KindRequest,
		Client:   client,
		Serial:   serial,
		Proposal: Proposal(payload),
	}
	m.Digest = RequestDigest(SHA256, client, serial, m.Proposal)
	m.Signature = sigFor("client", string(client), Preimage(m))
	s.digests = append(s.digests, m.Digest)
	for _, id := range s.ids {
		s.queue = append(s.queue, envelope{from: "client", to: id, msg: m})
	}
	return m
}

// restart rebuilds a replica from nothing but its journal, which is what a
// crash and a restart actually are. The application state it keeps is the one
// at its last checkpoint; everything after that it works out again.
func (s *sim) restart(t *testing.T, id ReplicaID) {
	t.Helper()
	r, err := New(s.ids, id,
		WithSigner(signAs(id)),
		WithVerifier(replicaVerifier()),
		WithClientVerifier(clientVerifier()),
		WithCheckpointInterval(s.k),
	)
	if err != nil {
		t.Fatalf("restart %s: %v", id, err)
	}
	if _, err := r.Restore(s.journal[id]); err != nil {
		t.Fatalf("restore %s: %v", id, err)
	}
	s.rep[id] = r
	s.timers[id] = map[Timer]time.Duration{}
	delete(s.held, id)
}

// requestTo hands a request to one replica only.
func (s *sim) requestTo(to ReplicaID, client ClientID, serial uint64, payload string) Message {
	m := Message{Kind: KindRequest, Client: client, Serial: serial, Proposal: Proposal(payload)}
	m.Digest = RequestDigest(SHA256, client, serial, m.Proposal)
	m.Signature = sigFor("client", string(client), Preimage(m))
	s.queue = append(s.queue, envelope{from: "client", to: to, msg: m})
	return m
}

func (s *sim) reachable(a, b ReplicaID) bool {
	if s.down[a] || s.down[b] {
		return false
	}
	// A partition here is total isolation, which is the shape that actually
	// stresses the protocol: a replica that hears nothing and is heard by
	// nobody.
	return s.part[a] == s.part[b]
}

// deliver runs one envelope. Everything else in the harness is bookkeeping.
func (s *sim) deliver(e envelope) {
	if s.down[e.to] {
		return
	}
	if e.from != "client" && !s.reachable(e.from, e.to) {
		return
	}
	before := s.rep[e.to].CurrentView()
	acts, err := s.rep[e.to].Step(e.msg)
	if errors.Is(err, ErrFutureView) && len(s.held[e.to]) < 4096 {
		s.held[e.to] = append(s.held[e.to], e)
	}
	s.handle(e.to, acts)
	if s.rep[e.to].CurrentView() != before {
		s.queue = append(s.queue, s.held[e.to]...)
		delete(s.held, e.to)
	}
}

func (s *sim) handle(from ReplicaID, acts []Action) {
	if len(acts) == 0 {
		return
	}
	s.slices = append(s.slices, slice{who: from, acts: acts})
	for _, a := range acts {
		switch v := a.(type) {
		case Send:
			s.emit(from, v.To, v.Message)
		case Broadcast:
			for _, id := range s.ids {
				if id != from {
					s.emit(from, id, v.Message)
				}
			}
		case Persist:
			s.journal[from] = append(s.journal[from], v.Records...)
		case Execute:
			s.apply(from, v)
		case TakeCheckpoint:
			d := s.digestOf(from, v.Seq)
			more, err := s.rep[from].Checkpointed(v.Seq, d)
			if err != nil {
				s.t.Fatalf("%s: checkpointed %d: %v", from, v.Seq, err)
			}
			s.handle(from, more)
		case FetchState:
			s.transfer(from, v)
		case SetTimer:
			s.timers[from][v.Which] = v.After
		case CancelTimer:
			delete(s.timers[from], v.Which)
		case Evidence:
			s.evidence = append(s.evidence, v.Proof)
		case Notice:
			s.notices = append(s.notices, v.Err)
		}
	}
}

func (s *sim) emit(from, to ReplicaID, m Message) {
	if mut, ok := s.mutate[from]; ok {
		out, keep := mut(s, from, to, m)
		if !keep {
			return
		}
		m = out
	}
	s.queue = append(s.queue, envelope{from: from, to: to, msg: m})
}

// apply is the test application: it records what was ordered where, which is
// the only thing the safety assertion needs.
func (s *sim) apply(id ReplicaID, e Execute) {
	if prev, ok := s.state[id][e.Seq]; ok && prev != string(e.Proposal) {
		s.t.Fatalf("%s executed two different proposals at sequence %d", id, e.Seq)
	}
	s.state[id][e.Seq] = string(e.Proposal)
	if e.Seq > s.lastExec[id] {
		s.lastExec[id] = e.Seq
	}
	s.checkSafety()
}

// checkSafety is S1, asserted after every execution across every replica: no
// two replicas ever execute different proposals at one sequence number.
func (s *sim) checkSafety() {
	for seq := Sequence(1); seq <= s.maxExecuted(); seq++ {
		var want string
		var have bool
		for _, id := range s.ids {
			p, ok := s.state[id][seq]
			if !ok {
				continue
			}
			if !have {
				want, have = p, true
				continue
			}
			if p != want {
				s.t.Fatalf("two replicas executed different proposals at sequence %d: %q and %q", seq, want, p)
			}
		}
	}
}

func (s *sim) maxExecuted() Sequence {
	var max Sequence
	for _, id := range s.ids {
		if s.lastExec[id] > max {
			max = s.lastExec[id]
		}
	}
	return max
}

// digestOf is the application's state digest: every proposal executed at or
// below seq, in sequence order. Two replicas that executed the same order
// produce the same bytes, which is exactly the property a checkpoint asserts.
func (s *sim) digestOf(id ReplicaID, seq Sequence) Digest {
	var b []byte
	seqs := make([]Sequence, 0, len(s.state[id]))
	for n := range s.state[id] {
		if n <= seq {
			seqs = append(seqs, n)
		}
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for _, n := range seqs {
		b = binary.BigEndian.AppendUint64(b, uint64(n))
		b = append(b, s.state[id][n]...)
		b = append(b, 0)
	}
	return sha256.Sum256(b)
}

// transfer is the caller-side half of FetchState: copy the state from somebody
// who certified it, then say so with Installed.
func (s *sim) transfer(to ReplicaID, f FetchState) {
	for _, donor := range f.From {
		if donor == to || s.down[donor] || !s.reachable(donor, to) {
			continue
		}
		if s.lastExec[donor] < f.Seq || s.digestOf(donor, f.Seq) != f.Digest {
			continue
		}
		for n, p := range s.state[donor] {
			if n <= f.Seq {
				s.state[to][n] = p
			}
		}
		if f.Seq > s.lastExec[to] {
			s.lastExec[to] = f.Seq
		}
		acts, err := s.rep[to].Installed(f.Seq, f.Digest)
		if err != nil {
			s.t.Fatalf("%s: installed %d: %v", to, f.Seq, err)
		}
		s.handle(to, acts)
		return
	}
}

// drain delivers until the network is quiet, in a seeded random order.
func (s *sim) drain() {
	for len(s.queue) > 0 {
		s.steps++
		if s.steps > 200000 {
			s.t.Fatal("the simulation did not settle")
		}
		i := s.rnd.Intn(len(s.queue))
		e := s.queue[i]
		s.queue = append(s.queue[:i], s.queue[i+1:]...)
		s.deliver(e)
	}
}

// tick fires every pending timer, which is how the harness expresses "enough
// time passed". There is no clock: the core asked for a duration and the
// harness decides it elapsed.
func (s *sim) tick() {
	type pending struct {
		id ReplicaID
		w  Timer
	}
	var fire []pending
	for _, id := range s.ids {
		if s.down[id] {
			continue
		}
		for w := range s.timers[id] {
			fire = append(fire, pending{id, w})
		}
	}
	sort.Slice(fire, func(i, j int) bool {
		if fire[i].id != fire[j].id {
			return fire[i].id < fire[j].id
		}
		return fire[i].w < fire[j].w
	})
	for _, p := range fire {
		if _, still := s.timers[p.id][p.w]; !still {
			continue
		}
		delete(s.timers[p.id], p.w)
		s.handle(p.id, s.rep[p.id].Timeout(p.w))
	}
}

// settle alternates draining and ticking until nothing moves or the budget is
// spent. It is the harness's stand-in for "the network eventually behaves".
func (s *sim) settle(rounds int) {
	for i := 0; i < rounds; i++ {
		s.drain()
		before := s.maxExecuted()
		s.tick()
		s.drain()
		if len(s.queue) == 0 && s.maxExecuted() == before && !s.anyTimer() {
			return
		}
	}
}

func (s *sim) anyTimer() bool {
	for _, id := range s.ids {
		if len(s.timers[id]) > 0 {
			return true
		}
	}
	return false
}

func (s *sim) primary() ReplicaID { return s.rep[s.ids[0]].Primary(s.rep[s.ids[0]].CurrentView()) }

// live is every replica that is neither down nor partitioned away.
func (s *sim) live() []ReplicaID {
	var out []ReplicaID
	for _, id := range s.ids {
		if !s.down[id] && !s.part[id] {
			out = append(out, id)
		}
	}
	return out
}

func (s *sim) requireExecuted(seq Sequence, payload string, who ...ReplicaID) {
	s.t.Helper()
	if len(who) == 0 {
		who = s.live()
	}
	for _, id := range who {
		got, ok := s.state[id][seq]
		if !ok {
			s.t.Fatalf("%s never executed sequence %d (it is at %d)", id, seq, s.lastExec[id])
		}
		if got != payload {
			s.t.Fatalf("%s executed %q at sequence %d, wanted %q", id, got, seq, payload)
		}
	}
}
