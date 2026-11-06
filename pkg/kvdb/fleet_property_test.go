package kvdb

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"

	ds "github.com/ipfs/go-datastore"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
	"google.golang.org/protobuf/proto"
)

// TestTheFleetUnderTime is the property test of the time-bounded design
// (T7.1, T8.14): per seed, two to six replicas on a virtual clock, one of them
// possibly with a clock wildly wrong, put and delete through one-way and
// group cuts long and short, kills, clean and crash restarts, restores from
// backups young and old, hot clones and fresh replicas, compactions, and
// advances of time by fractions of T, by T+m and by 3T. Every replica in
// contact hears every other at least once per D.
//
// The oracle is the application's operations only: a put is a value at a
// key, a removal names the values its writer saw there. After a quiesce (all
// cuts healed, everyone rejoined, time advanced) it asserts:
//
//  1. every correct replica holds the same state, element by element;
//  2. what is live is what the operations say, where an operation may go
//     either way only when (a) it had, at some moment of its life, no correct
//     replica alive that held it; (b) its writer's clock was the wrong one, a
//     wrong clock hurting only its owner; (c) it is the T5.4 residue: a
//     removal whose expiry passed while a replica that never heard it, and
//     was not rejoining, still held the value; or (d) a value its writer kept
//     unacknowledged and wrote again after a rebase while a removal of it had
//     already reached someone (T8.7, see the note);
//  3. bounded storage: after 3T with no writes, a correct replica knows at
//     most one incarnation per live replica and holds no pending removal, no
//     missing parent, no retry mark and no own operation, and at most one
//     block per live replica, plus one;
//  4. a replica is rebased only after an absence of most of T, or a restore
//     from an old backup.
func TestTheFleetUnderTime(t *testing.T) {
	t.Parallel()
	seeds := 120
	if testing.Short() {
		seeds = 20
	}
	for seed := range seeds {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			t.Parallel()
			runFleet(t, int64(seed), 1, false)
		})
	}
}

func TestTheFleetUnderTimeIsBoundedOverLongRuns(t *testing.T) {
	t.Parallel()
	seeds := 8
	if testing.Short() {
		seeds = 2
	}
	for seed := range seeds {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			t.Parallel()
			runFleet(t, int64(1000+seed), 4, false)
		})
	}
}

func TestTheFleetUnderChangingTime(t *testing.T) {
	t.Parallel()
	seeds := 60
	if testing.Short() {
		seeds = 10
	}
	for seed := range seeds {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			t.Parallel()
			runFleet(t, int64(2000+seed), 1, true)
		})
	}
}

const propTick = 2 * time.Second // D: a pair in contact hears each other at least this often

type propPut struct {
	key, value string
	author     int
	dot        dot
	at         time.Time
	fragile    bool
	byWrong    bool
	reached    map[int]bool
	// holders are the correct replicas it was live on after the last step.
	holders map[int]bool
}

type propRem struct {
	key       string
	removes   []*propPut
	author    int
	dot       dot
	at        time.Time
	ttl       time.Duration
	fragile   bool
	byWrong   bool
	reached   map[int]bool
	mayReturn map[*propPut]bool
}

type propBackup struct {
	of        int
	store     ds.Datastore
	dag       *fetchThroughDAGSvc
	lastHeard time.Time
}

type propFleet struct {
	t       *testing.T
	seed    int64
	st      *simTime
	rng     *rand.Rand
	reps    []*simReplica
	wrong   int
	cut     map[[2]int]bool
	backups []propBackup
	puts    map[string]*propPut
	rems    []*propRem
	trace   []string
	step    int
	gens    map[int]int
	// lastHeard is, per replica, the true time it last heard a correct
	// peer in contact; rebases are checked against it.
	lastHeard map[int]time.Time
	tvaries   bool
	ids       int
	// incLast is, per incarnation any replica held, the last time it was
	// current or a removal of one of its values was written.
	incLast   map[string]time.Time
	rejoins   map[int]bool
	wrongName string
	// replaced are the replicas whose state this step replaced: killed,
	// restored or rebased.
	replaced  map[int]bool
	logs      map[int]map[dot]*pb.Delta
	tickSince map[int]uint64
}

func (f *propFleet) now() time.Time { return f.st.at(0)() }

func (f *propFleet) log(format string, args ...any) {
	f.trace = append(f.trace, fmt.Sprintf("%d %s: ", f.step, f.now().Format("15:04:05"))+fmt.Sprintf(format, args...))
}

func (f *propFleet) fail(format string, args ...any) {
	f.t.Helper()
	tail := f.trace
	if len(tail) > 400 {
		tail = tail[len(tail)-400:]
	}
	f.t.Fatalf("%s\ntrace:\n%s", fmt.Sprintf(format, args...), strings.Join(tail, "\n"))
}

func (f *propFleet) live() []int {
	var out []int
	for i, sr := range f.reps {
		if sr != nil {
			out = append(out, i)
		}
	}
	return out
}

func (f *propFleet) correct() []int {
	var out []int
	for _, i := range f.live() {
		if i != f.wrong {
			out = append(out, i)
		}
	}
	return out
}

func (f *propFleet) pick() int {
	l := f.live()
	return l[f.rng.Intn(len(l))]
}

func (f *propFleet) add(offset time.Duration) int {
	f.ids++
	sr := f.st.replica(f.t, f.name(), offset)
	f.reps = append(f.reps, sr)
	i := len(f.reps) - 1
	f.gens[i] = 0
	f.lastHeard[i] = f.now()
	if f.tvaries && f.rng.Intn(2) == 0 {
		f.setT(i)
	}
	return i
}

// name is the id of replica number f.ids. Seeds run in parallel in one
// process, and a store reopened takes back the incarnation its id last closed
// under, so no two seeds share an id.
func (f *propFleet) name() string { return fmt.Sprintf("s%d.r%d", f.seed, f.ids) }

func (f *propFleet) setT(i int) {
	t := time.Duration(1+f.rng.Intn(4)) * simRetention
	if err := f.reps[i].r.SetRetention(t); err != nil {
		f.t.Fatal(err)
	}
	f.log("r%d T=%s", i, t)
}

func (f *propFleet) values(i int, key string) []*propPut {
	sr := f.reps[i]
	els, err := sr.r.set.liveElems(context.Background(), newTxn(sr.r.store), key)
	if err != nil {
		f.t.Fatal(err)
	}
	var out []*propPut
	seen := map[string]bool{}
	for _, e := range els {
		if p := f.puts[string(e.value)]; p != nil && !seen[p.value] {
			seen[p.value] = true
			out = append(out, p)
		}
	}
	return out
}

func (f *propFleet) holds(i int, p *propPut) bool {
	for _, q := range f.values(i, p.key) {
		if q == p {
			return true
		}
	}
	return false
}

func (f *propFleet) lastDot(i int) dot {
	r := f.reps[i].r
	r.mu.Lock()
	defer r.mu.Unlock()
	return dot{replica: r.replica, counter: r.counter}
}

func (f *propFleet) ttlOf(i int) time.Duration {
	return time.Duration(f.reps[i].r.retention()) * time.Millisecond
}

func (f *propFleet) put(i int, key string) {
	value := fmt.Sprintf("v%d.%d", f.step, i)
	seen := f.values(i, key)
	if err := f.reps[i].r.Put(context.Background(), ds.NewKey(key), []byte(value)); err != nil {
		f.t.Fatal(err)
	}
	d := f.lastDot(i)
	p := &propPut{key: key, value: value, author: i, dot: d, at: f.now(), byWrong: i == f.wrong, reached: map[int]bool{}, holders: map[int]bool{}}
	if i != f.wrong {
		p.holders[i] = true
	}
	f.puts[value] = p
	f.removing(seen)
	if len(seen) > 0 {
		f.rems = append(f.rems, &propRem{key: key, removes: seen, author: i, dot: d, at: f.now(), ttl: f.ttlOf(i), byWrong: i == f.wrong, reached: map[int]bool{}, mayReturn: map[*propPut]bool{}})
	}
	f.log("r%d put %s=%s (over %d) %s", i, key, value, len(seen), d)
}

func (f *propFleet) del(i int, key string) {
	seen := f.values(i, key)
	if len(seen) == 0 {
		return
	}
	if err := f.reps[i].r.Delete(context.Background(), ds.NewKey(key)); err != nil {
		f.t.Fatal(err)
	}
	d := f.lastDot(i)
	f.removing(seen)
	f.rems = append(f.rems, &propRem{key: key, removes: seen, author: i, dot: d, at: f.now(), ttl: f.ttlOf(i), byWrong: i == f.wrong, reached: map[int]bool{}, mayReturn: map[*propPut]bool{}})
	var vs []string
	for _, p := range seen {
		vs = append(vs, p.value)
	}
	f.log("r%d del %s (%s) %s", i, key, strings.Join(vs, ","), d)
}

// removing records a removal of the values seen: applied to one of their
// dots, it moves the entry of that dot's incarnation to its own time (R-C),
// so the entry is known a T and a margin past it, live incarnation or not.
func (f *propFleet) removing(seen []*propPut) {
	for _, p := range seen {
		f.incLast[p.dot.replica] = f.now()
	}
}

// tick is one rebroadcast interval of D: everyone beats, what they say
// reaches whoever the cuts let it reach, answers included, and everyone
// maintains.
func (f *propFleet) tick() {
	f.st.advance(propTick)
	f.keepLogs()
	live := f.live()
	// What each replica reported before anyone answered it this round: a
	// verdict judges that, even if an ok from another judge lands first.
	f.tickSince = map[int]uint64{}
	for _, i := range live {
		f.tickSince[i] = f.reps[i].r.reportedSinceNow()
	}
	for _, i := range live {
		f.reps[i].beat()
	}
	for round := 0; round < 6; round++ {
		quiet := true
		for _, i := range live {
			msgs := f.reps[i].out.take()
			if len(msgs) == 0 {
				continue
			}
			quiet = false
			for _, j := range live {
				if j == i || f.cut[[2]int{i, j}] {
					continue
				}
				for _, data := range msgs {
					f.deliver(i, j, data)
				}
			}
		}
		if quiet {
			break
		}
	}
	for _, i := range live {
		f.reps[i].maintain(f.t)
		// What the repair loop does on its own interval.
		if f.reps[i].r.IsDirty(context.Background()) {
			f.reach(i)
			f.reps[i].r.Repair(context.Background())
		}
	}
}

func (f *propFleet) deliver(i, j int, data []byte) {
	f.reach(j)
	msg, err := f.reps[j].r.decodeBroadcast(data)
	if err != nil {
		f.t.Fatal(err)
	}
	// What a replica holds is as recent as the last correct peer it heard
	// while not waiting to be judged.
	if i != f.wrong && !msg.rejoining && !f.reps[j].rejoining() {
		f.lastHeard[j] = f.now()
	}
	before := f.reps[j].r.reportedSinceNow()
	beforeInc := f.reps[j].incarnation()
	f.reps[j].r.handleBroadcast(context.Background(), msg)
	if g := f.reps[j].rebases(); g != f.gens[j] {
		f.gens[j] = g
		f.replaced[j] = true
		if msg.verdict != nil {
			f.log("r%d judged r%d: verdict since %d, judge since %d, judge clock %d, to %x, was %x, judge inc %x rejoining %t", i, j, msg.verdict.since, msg.since, msg.now, msg.verdict.to, beforeInc, msg.incarnation, msg.rejoining)
		}
		if ts, ok := f.tickSince[j]; ok && ts < before {
			before = ts
		}
		f.rebasedFrom(j, before)
		f.rewritten(j)
	}
}

func (f *propFleet) reach(j int) {
	var from []*simReplica
	for _, i := range f.live() {
		if i != j && !f.cut[[2]int{i, j}] {
			from = append(from, f.reps[i])
		}
	}
	f.reps[j].reach(from...)
}

func (f *propFleet) advance(d time.Duration) {
	for elapsed := time.Duration(0); elapsed < d; elapsed += propTick {
		f.tick()
		f.observe()
	}
}

func (f *propFleet) healAll() {
	f.cut = map[[2]int]bool{}
	f.log("heal all")
}

func (f *propFleet) partition() {
	live := f.live()
	f.cut = map[[2]int]bool{}
	switch f.rng.Intn(3) {
	case 0:
		x := live[f.rng.Intn(len(live))]
		for _, j := range live {
			if j != x {
				f.cut[[2]int{x, j}], f.cut[[2]int{j, x}] = true, true
			}
		}
		f.log("isolate r%d", x)
	default:
		side := map[int]bool{}
		for _, i := range live {
			side[i] = f.rng.Intn(2) == 0
		}
		oneWay := f.rng.Intn(2) == 0
		for _, i := range live {
			for _, j := range live {
				if i != j && side[i] != side[j] && (!oneWay || side[i]) {
					f.cut[[2]int{i, j}] = true
				}
			}
		}
		var a []string
		for _, i := range live {
			if side[i] {
				a = append(a, fmt.Sprintf("r%d", i))
			}
		}
		f.log("partition %v from the rest, one way %t", a, oneWay)
	}
}

func (f *propFleet) kill(i int) {
	if len(f.correct()) <= 2 && i != f.wrong {
		return
	}
	if err := f.reps[i].r.Close(); err != nil {
		f.t.Fatal(err)
	}
	f.reps[i].r = nil
	f.reps[i].release()
	f.reps[i] = nil
	f.replaced[i] = true
	f.log("kill r%d", i)
}

func (f *propFleet) restart(i int, crash bool) {
	sr := f.reps[i]
	if crash {
		// A process that died: the store as it was, the shutdown mark
		// left, and the incarnation never handed to a next open.
		closeStore(f.t, sr.r)
		forgetIncarnation(sr.r.incarnationKey())
		if err := sr.store.Put(context.Background(), ds.NewKey("crdttest").ChildString(badShutdownKey), nil); err != nil {
			f.t.Fatal(err)
		}
		sr.open(f.t)
	} else {
		sr.reopen(f.t)
	}
	f.gens[i] = f.reps[i].rebases()
	f.log("restart r%d crash %t (rejoining %t, since %d)", i, crash, f.reps[i].rejoining(), f.reps[i].r.reportedSinceNow())
}

func forgetIncarnation(k incarnationKey) {
	incarnations.mu.Lock()
	defer incarnations.mu.Unlock()
	delete(incarnations.closed, k)
}

func (f *propFleet) backup(i int) {
	sr := f.reps[i]
	f.backups = append(f.backups, propBackup{of: i, store: copyStore(f.t, sr.store), dag: copyBlocks(f.t, sr.dag), lastHeard: f.lastHeard[i]})
	f.log("backup r%d", i)
}

func (f *propFleet) restore(i int) {
	var mine []propBackup
	for _, b := range f.backups {
		if b.of == i {
			mine = append(mine, b)
		}
	}
	if len(mine) == 0 {
		return
	}
	b := mine[f.rng.Intn(len(mine))]
	sr := f.reps[i]
	closeStore(f.t, sr.r)
	forgetIncarnation(sr.r.incarnationKey())
	sr.store, sr.dag = copyStore(f.t, b.store), copyBlocks(f.t, b.dag)
	sr.open(f.t)
	f.gens[i] = sr.rebases()
	f.lastHeard[i] = b.lastHeard
	f.forgetReached(i)
	f.replaced[i] = true
	f.log("restore r%d from a backup", i)
}

func (f *propFleet) clone(i int) {
	src := f.reps[i]
	f.ids++
	sr := &simReplica{name: f.name(), store: copyStore(f.t, src.store), dag: copyBlocks(f.t, src.dag), out: &outbox{}, offset: src.offset, st: f.st}
	o := *src.opts
	sr.opts = &o
	sr.open(f.t)
	f.reps = append(f.reps, sr)
	j := len(f.reps) - 1
	f.gens[j] = 0
	f.lastHeard[j] = f.lastHeard[i]
	if i == f.wrong {
		// A copy of the wrong clock's store runs on the same wrong clock.
		f.reps[j].release()
		f.reps[j] = nil
		return
	}
	f.log("clone r%d as r%d", i, j)
}

func (f *propFleet) forgetReached(i int) {
	for _, p := range f.puts {
		delete(p.reached, i)
	}
	for _, r := range f.rems {
		delete(r.reached, i)
	}
}

func (f *propFleet) ownLog(i int) map[dot]*pb.Delta {
	r := f.reps[i].r
	out := map[dot]*pb.Delta{}
	prefix := r.ownPrefix().String() + "/"
	err := scanStore(context.Background(), r.store, r.ownPrefix(), func(k string, v []byte) error {
		d, err := parseDotSegment(strings.TrimPrefix(k, prefix))
		if err != nil {
			return err
		}
		rec := &pb.Delta{}
		if err := proto.Unmarshal(v, rec); err != nil {
			return err
		}
		out[d] = rec
		return nil
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *propFleet) coveredElsewhere(i int, d dot) bool {
	for _, j := range f.correct() {
		if j != i && f.reps[j].r.covered(d) {
			return true
		}
	}
	return false
}

func (f *propFleet) liveValues() map[int]map[string]bool {
	out := map[int]map[string]bool{}
	for _, i := range f.live() {
		r := f.reps[i].r
		vals := map[string]bool{}
		scanStore(context.Background(), r.store, r.set.elemsPrefix(), func(k string, v []byte) error {
			if el, err := r.set.parseElem(k, v); err == nil {
				vals[string(el.value)] = true
			}
			return nil
		})
		out[i] = vals
	}
	return out
}

func (f *propFleet) observe() {
	now := f.now()
	correct := f.correct()
	lv := f.liveValues()
	for _, i := range f.live() {
		f.incLast[f.reps[i].incarnation()] = now
		if rj := f.reps[i].rejoining(); rj != f.rejoins[i] {
			f.rejoins[i] = rj
			f.log("r%d rejoining %t (reports %d)", i, rj, f.reps[i].r.reportedSinceNow())
		}
	}
	for _, i := range f.live() {
		if g := f.reps[i].rebases(); g != f.gens[i] {
			f.gens[i] = g
			f.replaced[i] = true
			f.rebased(i)
		}
	}
	named := map[*propPut]bool{}
	for _, r := range f.rems {
		for _, p := range r.removes {
			named[p] = true
		}
	}
	for _, p := range f.puts {
		// Reached: merged elsewhere, or held there, as an element put back
		// after a rebase is: under its own dot, as an ancient one.
		for _, i := range correct {
			if i != p.author && (f.reps[i].r.covered(p.dot) || lv[i][p.value]) {
				p.reached[i] = true
			}
		}
		if p.fragile || p.byWrong {
			continue
		}
		if !named[p] {
			// Never removed: it is lost only when every replica it was live
			// on was killed, restored or rebased.
			holders := map[int]bool{}
			for _, i := range correct {
				if lv[i][p.value] {
					holders[i] = true
				}
			}
			if len(holders) == 0 {
				for i := range p.holders {
					if !f.replaced[i] && f.reps[i] != nil {
						f.fail("%s=%s is live on no correct replica, though r%d held it and was not replaced", p.key, p.value, i)
					}
				}
				p.fragile = true
				f.log("put %s went with the replicas that held it", p.value)
			}
			p.holders = holders
			continue
		}
		// Held is live on a correct replica, or merged there while its dot
		// can still be known.
		held := false
		for _, i := range correct {
			if lv[i][p.value] || (now.Sub(p.at) < simRetention && f.reps[i].r.covered(p.dot)) {
				held = true
				break
			}
		}
		if !held {
			p.fragile = true
			f.log("put %s is held by no correct replica", p.value)
		}
	}
	for _, r := range f.rems {
		for _, i := range correct {
			if f.reps[i].r.covered(r.dot) {
				r.reached[i] = true
			}
		}
		if !r.fragile && now.Sub(r.at) < r.ttl {
			held := false
			for _, i := range correct {
				if f.reps[i].r.covered(r.dot) {
					held = true
					break
				}
			}
			if !held {
				r.fragile = true
				f.log("removal %s of %s is held by no correct replica", r.dot, r.key)
			}
		}
		if r.fragile || r.byWrong || now.Before(r.at.Add(r.ttl+simMargin)) {
			continue
		}
		for _, p := range r.removes {
			if r.mayReturn[p] {
				continue
			}
			// The holders are the correct replicas, and the writer of the
			// value itself whatever its clock: a writer keeps its own values
			// through a rebase (the re-issue window), so a removal it never
			// heard can expire under its copy.
			holders := correct
			if p.author == f.wrong && f.reps[p.author] != nil {
				holders = append(append([]int(nil), correct...), p.author)
			}
			for _, i := range holders {
				if !r.reached[i] && i != r.author && !f.reps[i].rejoining() && lv[i][p.value] {
					r.mayReturn[p] = true
					f.log("residue: r%d never heard the removal of %s and still holds it", i, p.value)
					break
				}
			}
		}
	}
}

func (f *propFleet) rebased(i int) {
	f.log("r%d rebased", i)
	f.forgetReached(i)
}

// rebasedFrom checks a rebase was due: the replica had not heard the fleet
// for most of T, by the fleet time it reported before the verdict.
func (f *propFleet) rebasedFrom(i int, since uint64) {
	gap := time.Duration(millis(f.now())-since) * time.Millisecond
	f.log("r%d rebased, %s after the fleet time it reported", i, gap)
	if !f.tvaries && i != f.wrong && gap < simRetention-3*simMargin {
		f.fail("r%d was rebased %s after the fleet time it reported, under T %s", i, gap, simRetention)
	}
	f.forgetReached(i)
}

func (f *propFleet) act() {
	f.step++
	f.replaced = map[int]bool{}
	f.keepLogs()
	i := f.pick()
	switch a := f.rng.Intn(100); {
	case a < 28:
		f.put(i, f.key())
	case a < 42:
		f.del(i, f.key())
	case a < 62:
		f.tick()
	case a < 68:
		if f.rng.Intn(3) == 0 {
			f.healAll()
		} else {
			f.partition()
		}
	case a < 72:
		switch f.rng.Intn(4) {
		case 0:
			f.advance(simRetention / 3)
		case 1:
			f.advance(simRetention + simMargin)
		case 2:
			f.advance(3 * simRetention)
		default:
			f.advance(time.Duration(f.rng.Int63n(int64(simRetention))))
		}
	case a < 74:
		f.kill(i)
	case a < 78:
		f.restart(i, f.rng.Intn(2) == 0)
	case a < 81:
		f.backup(i)
	case a < 84:
		f.restore(i)
	case a < 86:
		if len(f.live()) < 7 {
			f.clone(i)
		}
	case a < 88:
		if len(f.live()) < 7 {
			f.add(0)
		}
	case a < 94:
		if _, err := f.reps[i].r.Compact(context.Background()); err != nil {
			f.t.Fatal(err)
		}
		f.log("r%d compact", i)
	case a < 96:
		if f.tvaries {
			f.setT(i)
		}
	default:
		j := f.pick()
		if j != i {
			f.reps[i].beat()
			for _, data := range f.reps[i].out.take() {
				f.deliver(i, j, data)
			}
			f.log("r%d -> r%d once", i, j)
		}
	}
	f.observe()
}

func (f *propFleet) keepLogs() {
	f.logs = map[int]map[dot]*pb.Delta{}
	for _, i := range f.live() {
		f.logs[i] = f.ownLog(i)
	}
}

// rewritten marks what replica j put back after a rebase that a removal had
// already reached and that its removal's refutation is gone for: the
// replica's own elements come back under their own dots, so a removal still
// known anywhere refutes them; one already forgotten cannot (the note's
// re-issue window: an acknowledgement lost to a cut).
func (f *propFleet) rewritten(j int) {
	now := f.now()
	for d := range f.logs[j] {
		for _, p := range f.puts {
			if p.dot != d || len(p.reached) == 0 {
				continue
			}
			for _, r := range f.rems {
				if now.Before(r.at.Add(r.ttl + simMargin - 2*simS)) {
					continue
				}
				for _, q := range r.removes {
					if q == p && !r.mayReturn[p] {
						r.mayReturn[p] = true
						f.log("re-issue: r%d wrote %s again after a rebase", j, p.value)
					}
				}
			}
		}
	}
}

func (f *propFleet) key() string {
	if f.rng.Intn(4) == 0 {
		return fmt.Sprintf("/u%d", f.step)
	}
	return fmt.Sprintf("/k%d", f.rng.Intn(4))
}

func runFleet(t *testing.T, seed int64, length int, tvaries bool) {
	st := newSimTime()
	rng := rand.New(rand.NewSource(seed))
	f := &propFleet{t: t, seed: seed, st: st, rng: rng, wrong: -1, cut: map[[2]int]bool{}, puts: map[string]*propPut{}, gens: map[int]int{}, lastHeard: map[int]time.Time{}, tvaries: tvaries, incLast: map[string]time.Time{}, rejoins: map[int]bool{}, replaced: map[int]bool{}}
	defer func() {
		for _, sr := range f.reps {
			if sr != nil {
				sr.release()
			}
		}
	}()
	n := 2 + rng.Intn(5)
	wrong := rng.Intn(3) == 0
	if wrong && n == 2 {
		// A wrong clock runs beside two correct ones at least: a correct
		// replica alone with it hears no clock that agrees with its own,
		// as the wrong one does not, and no judge can ever vouch for
		// either.
		n = 3
	}
	for i := range n {
		offset := time.Duration(rng.Int63n(int64(simS))) - simS/2
		if wrong && i == n-1 {
			k := time.Duration(2+rng.Intn(9)) * simRetention
			if rng.Intn(2) == 0 {
				k = -k
			}
			offset = k
			f.wrong = n - 1
		}
		j := f.add(offset)
		if j == f.wrong {
			f.wrongName = f.reps[j].name
		}
		if wrong && i == n-1 {
			f.log("r%d has a clock off by %s", i, offset)
		}
	}
	f.tick()
	steps := length * (60 + rng.Intn(80))
	for range steps {
		f.act()
	}

	// The quiesce: every cut healed, long enough for every rejoin, rebase
	// and acknowledgement, then 3T with no writes.
	f.healAll()
	f.advance(20 * propTick)
	f.settle()
	f.compactAll()
	f.settle()
	f.check("after the heal")
	f.advance(3 * simRetention)
	f.settle()
	f.compactAll()
	f.settle()
	f.check("after 3T")
	f.bound()
}

func (f *propFleet) compactAll() {
	for _, i := range f.live() {
		if _, err := f.reps[i].r.Compact(context.Background()); err != nil {
			f.t.Fatal(err)
		}
	}
}

// settle lets what the last maintenance wrote reach everyone: two rounds of
// heartbeats, with no time passing and nothing maintained.
func (f *propFleet) settle() {
	for range 2 {
		live := f.live()
		for _, i := range live {
			f.reps[i].beat()
		}
		for round := 0; round < 6; round++ {
			quiet := true
			for _, i := range live {
				msgs := f.reps[i].out.take()
				if len(msgs) == 0 {
					continue
				}
				quiet = false
				for _, j := range live {
					if j != i && !f.cut[[2]int{i, j}] {
						for _, data := range msgs {
							f.deliver(i, j, data)
						}
					}
				}
			}
			if quiet {
				break
			}
		}
	}
	f.observe()
}

func (f *propFleet) check(when string) {
	f.t.Helper()
	correct := f.correct()
	if len(correct) == 0 {
		return
	}
	for _, i := range correct {
		if f.reps[i].rejoining() {
			f.fail("%s: r%d is still rejoining", when, i)
		}
	}
	var rs []*Datastore
	var names []string
	for _, i := range correct {
		rs = append(rs, f.reps[i].r)
		names = append(names, fmt.Sprintf("r%d", i))
	}
	// Each replica forgets by its own clock, and correct clocks differ by
	// up to S, plus how stale a fleet time a replica holds: whatever expires
	// within that window of now may be forgotten on one and not yet on
	// another. Everything else must be identical.
	lo, hi := ^uint64(0), uint64(0)
	for _, r := range rs {
		d := r.dropClockNow()
		lo, hi = min(lo, d), max(hi, d)
	}
	w := durationMillis(2*simS + simD + 2*propTick)
	edge := func(expires uint64) bool {
		at := expires + durationMillis(simMargin)
		return at+w >= lo && at <= hi+w
	}
	want := comparableState(f.t, rs[0], edge)
	for k, r := range rs[1:] {
		if got := comparableState(f.t, r, edge); got != want {
			f.fail("%s: %s diverged from %s:\n--- %s\n%s\n--- %s\n%s", when, names[k+1], names[0], names[0], want, names[k+1], got)
		}
	}
	ref := correct[0]
	for _, p := range f.puts {
		live := f.holds(ref, p)
		free := p.fragile || p.byWrong
		removed := false
		for _, r := range f.rems {
			for _, q := range r.removes {
				if q != p {
					continue
				}
				if r.fragile || r.byWrong || r.mayReturn[p] {
					free = true
				} else {
					removed = true
				}
			}
		}
		switch {
		case removed && live:
			f.fail("%s: %s=%s was removed and is live on every correct replica", when, p.key, p.value)
		case !removed && !live && !free:
			f.fail("%s: %s=%s was never removed and is lost", when, p.key, p.value)
		}
	}
}

// An incarnation id is the replica's name followed by a 16-byte nonce.
func (f *propFleet) wrongAuthor(d dot) bool {
	return f.wrong >= 0 && len(d.replica) > 16 && d.replica[:len(d.replica)-16] == f.wrongName
}

// comparableState leaves out pending removals and context entries near enough
// to expiry that replicas may differ on them.
func comparableState(t testing.TB, r *Datastore, edge func(expires uint64) bool) string {
	t.Helper()
	ctx := context.Background()
	var lines []string
	add := func(prefix ds.Key, label string, keep func(k string, v []byte) bool) {
		err := scanStore(ctx, r.store, prefix, func(k string, v []byte) error {
			if keep(k, v) {
				lines = append(lines, fmt.Sprintf("%s %s=%x", label, strings.TrimPrefix(k, prefix.String()), v))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	all := func(string, []byte) bool { return true }
	add(r.set.elemsPrefix(), "elem", all)
	add(r.set.pendingPrefix(), "pending", func(k string, v []byte) bool {
		rm, err := r.set.parsePending(k, v)
		return err != nil || !edge(rm.expires())
	})
	add(r.set.namespace.ChildString(keysNs), "value", all)
	sort.Strings(lines)
	r.mu.Lock()
	for _, rep := range r.cc.replicas() {
		k := r.cc.inc[rep]
		if !edge(k.expires) {
			lines = append(lines, fmt.Sprintf("entry %x %v changed %d expires %d", rep, k.spans, k.changed, k.expires))
		}
	}
	r.mu.Unlock()
	hs, _ := r.heads.List()
	var heads []string
	for _, h := range hs {
		heads = append(heads, h.Cid.String())
	}
	sort.Strings(heads)
	lines = append(lines, "heads "+strings.Join(heads, " "))
	return strings.Join(lines, "\n")
}

// bound asserts property 3 after 3T with no writes. An incarnation is known
// for T and a margin past its last write or the last removal of one of its
// values, so what a replica may still know is one entry for each incarnation
// some replica wrote under or removed from in that window: the current one of
// each live replica, and those a rebase or a restart replaced within it.
//
// An own operation is kept until a peer acknowledges it or its delta
// expires, and a T may outlast the quiesce: one still within its delta that
// no correct peer covers is one nobody can acknowledge, as what a rebase
// writes again under its old dot is.
func (f *propFleet) bound() {
	f.t.Helper()
	window := map[string]bool{}
	longest := simRetention
	if f.tvaries {
		longest = 4 * simRetention
	}
	since := f.now().Add(-(longest + 2*simMargin))
	for inc, last := range f.incLast {
		if last.After(since) {
			window[inc] = true
		}
	}
	for _, i := range f.live() {
		window[f.reps[i].incarnation()] = true
	}
	live := len(window)
	for _, i := range f.correct() {
		r := f.reps[i].r
		// An erased record is the entry of the incarnation a rebase replaced,
		// kept as long as that entry would be.
		r.mu.Lock()
		known := map[string]bool{}
		for rep := range r.cc.inc {
			known[rep] = true
		}
		for rep := range r.erased {
			known[rep] = true
		}
		entries := len(known)
		r.mu.Unlock()
		// A removal still within its own window is a write of the window, as
		// a wrong clock's rebases keep writing; one past it must be gone.
		pending := 0
		dropAt := r.dropClockNow()
		scanStore(context.Background(), r.store, r.set.pendingPrefix(), func(k string, v []byte) error {
			if rm, err := r.set.parsePending(k, v); err == nil && r.expired(rm.expires(), dropAt) {
				pending++
			}
			return nil
		})
		r.mu.Lock()
		for _, k := range r.cc.inc {
			if r.expired(k.expires, dropAt) {
				pending++
			}
		}
		for _, k := range r.erased {
			if r.expired(k.expires, dropAt) {
				pending++
			}
		}
		r.mu.Unlock()
		// A missing parent is in flight until its expiry: after 3T the only
		// ones left are blocks of the wrong clock's replica, refused as ahead
		// of time until the clock reached them, by when it had purged their
		// parents. None may outlive its expiry.
		missing := 0
		mp := r.missingPrefix()
		scanStore(context.Background(), r.store, mp, func(k string, v []byte) error {
			d, err := parseDotSegment(strings.TrimPrefix(k, mp.String()+"/"))
			if err != nil {
				return err
			}
			l, err := decodeLink(d, v)
			if err != nil {
				return err
			}
			if r.gone(l, dropAt) || !f.wrongAuthor(d) {
				missing++
			}
			return nil
		})
		retry := countEntries(f.t, r, r.retryPrefix())
		own := 0
		for d, rec := range f.ownLog(i) {
			if r.expired(rec.GetAt()+rec.GetTtl(), dropAt) || f.coveredElsewhere(i, d) {
				own++
			}
		}
		blocks := len(indexedBlocks(f.t, r))
		if entries > live || pending != 0 || missing != 0 || retry != 0 || own != 0 || blocks > live+1 {
			var incs []string
			r.mu.Lock()
			for rep, k := range r.cc.inc {
				incs = append(incs, fmt.Sprintf("%x changed %d expires %d", rep, k.changed, k.expires))
			}
			for rep, k := range r.erased {
				incs = append(incs, fmt.Sprintf("erased %x %v changed %d expires %d", rep, k.spans, k.changed, k.expires))
			}
			r.mu.Unlock()
			scanStore(context.Background(), r.store, r.set.pendingPrefix(), func(k string, v []byte) error {
				if rm, err := r.set.parsePending(k, v); err == nil {
					incs = append(incs, fmt.Sprintf("pending %s %s at %d ttl %d known %t", rm.key, rm.dot, rm.at, rm.ttl, r.cc.knows(rm.dot.replica)))
				}
				return nil
			})
			mp := r.missingPrefix()
			scanStore(context.Background(), r.store, mp, func(k string, v []byte) error {
				d, _ := parseDotSegment(strings.TrimPrefix(k, mp.String()+"/"))
				l, err := decodeLink(d, v)
				if err == nil {
					incs = append(incs, fmt.Sprintf("missing %s at %d ttl %d covered %t gone %t dirty %t", d, l.at, l.ttl, r.covered(d), r.gone(l, r.dropClockNow()), r.IsDirty(context.Background())))
				}
				return nil
			})
			for d, rec := range f.ownLog(i) {
				var by []string
				for _, j := range f.correct() {
					by = append(by, fmt.Sprintf("r%d:%t", j, f.reps[j].r.covered(d)))
				}
				incs = append(incs, fmt.Sprintf("own %s at %d ttl %d covered %s", d, rec.GetAt(), rec.GetTtl(), strings.Join(by, " ")))
			}
			sort.Strings(incs)
			f.fail("r%d after 3T (now %d, drop clock %d): %d entries for %d live incarnations, %d expired kept, %d missing, %d retry, %d own, %d blocks\n%s",
				i, millis(f.reps[i].r.opts.Clock()), f.reps[i].r.dropClockNow(), entries, live, pending, missing, retry, own, blocks, strings.Join(incs, "\n"))
		}
	}
}
