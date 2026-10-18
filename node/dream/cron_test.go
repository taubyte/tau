//go:build dreaming

package dream_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
)

// cron_test is a schedule firing with nobody watching the clock but the cloud
// itself, across two nodes and one raft cluster.
//
// WHAT ONLY A UNIVERSE CAN SHOW is that TWO nodes holding one deployment fire
// it ONCE. Every node runs the same loop and reads the same expression; what
// makes it once is a replicated write, and nothing short of real raft between
// real peers proves that the write is the thing deciding.
//
// AND THE PROJECT IS AUTHORED RATHER THAN TRANSCRIBED, for project_test's
// reason. The object this test used to publish was written by hand, so it was a
// statement of what the compiler was believed to produce and nothing ever
// checked it — and it had already drifted in three places. It omitted
// `execution/timeout`, which is Required(), so tcc would have REFUSED the very
// project this test claimed to be running; it omitted `implements`, which the
// vocabulary defaults to `handler`; and it omitted `on_overlap`, which
// Default("skip") materialises on every cron there is. So the schedule under
// test was one no author could have written, and the one field of it that
// decides what a second tick does was never in the fixture at all.
//
// The yaml under projects/with/cron is compiled here, which is the only way a
// schedule's declaration and a schedule's firing are tested as one thing.

// TestAScheduleFiresOnceAcrossAFleet.
//
// THE UNIVERSE'S CLOCK IS THE TEST'S, which is what makes this a test of the
// claim rather than of how busy the machine is. It used to wait up to 180
// seconds for a real minute boundary to come round: nothing about that is a
// property of the code, and on a loaded box the fleet was still booting when the
// budget ran out — "the schedule never fired", from a schedule that was fine.
func TestAScheduleFiresOnceAcrossAFleet(t *testing.T) {
	fired := &firing{name: "wasm"}
	defer nodeDream.Runtimes(nodeDream.Runtimes(fired)...)

	// Somewhere with no daylight-saving edge in it, so the slots this test walks
	// are all sixty seconds long. The project says `timezone: UTC` for the same
	// reason, and says it rather than leans on the default so the file states
	// what this test rests on.
	at := clockFrom(time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC))
	defer nodeDream.Clock(nodeDream.Clock(at.Now))

	// THE HOST IS INERT. deployFleet names one because a register keeps its
	// pointers by fqdn, but nothing here is asked over HTTP: a cron reaches its
	// target through the worker, and the project declares no zone and no
	// gateway — exactly as the object it replaces did.
	cloud := deployFleet(t, 2, "with/cron/skip", "cron.example.com", cacherWASM(t))
	first, second := cloud[0].node, cloud[1].node

	// BOTH NODES HAVE TO HOLD IT, which is what makes this a test of the claim
	// rather than of which node happened to have the project. A node drains and
	// fires only what it is already serving.
	//
	// THE BUDGET IS A STALL REPORT AND NOT A BOUND. deployFleet has already
	// waited for every node to resolve the project, the fleet and the placement,
	// so all that is left here is the worker turning that object into a grant —
	// local, and true by this line or never. It keeps formingBudget because a
	// number that large is only ever spent on a universe that has gone wrong.
	holds := func(n *node.Node) bool {
		_, err := n.Worker().Resolve(t.Context(), scheduleTarget())
		return err == nil
	}
	require.Eventually(t, func() bool { return holds(first) && holds(second) },
		formingBudget, time.Second, "the project never reached both nodes")

	// ONE FIRE. The first turn a leader takes records where the clock is and
	// hands out nothing, so the clock is walked forward a minute at a time
	// until a tasking lands rather than advanced once and hoped over — which
	// turn sees which minute is the cloud's business, not this test's.
	//
	// WHAT IS LEFT TO WAIT FOR IS TWO TURNS, and the budget is that priced: the
	// primitives namespace's group still has to form (formingBudget, and a
	// follower tasks nothing), then TWO real turns of that service's ONE period
	// — DefaultSweep, thirty seconds, one to record where the clock is and one
	// to hand the window out — plus this loop's own 1s granularity. 60s of
	// turns inside 90s of margin.
	//
	// THE CLOCK IS THE ONE THING THIS LOOP DRIVES. A turn only hands out a
	// minute the leader has not handed out already, so what the added minute
	// buys is that consecutive turns land in different minutes without waiting
	// a real one out.
	require.Eventually(t, func() bool {
		at.add(time.Minute)
		return fired.count() > 0
	}, formingBudget+90*time.Second, time.Second, "the schedule never fired")

	req := fired.seen()[0]
	require.Equal(t, "POST", req.Method)
	require.Equal(t, []string{"everyminute"}, req.Headers["X-Taubyte-Cron"])
	require.NotEmpty(t, req.Headers["X-Taubyte-Fired-At"])

	// AND THE WHOLE IDENTITY AND NOT JUST THE NAME. A function fired by a clock
	// has no request to read, so a name it cannot look up and that three
	// applications could each hold leaves it nothing to act on. The id is the
	// cron out of the object this project compiled to — projects/with/cron's
	// own cron/everyminute.yaml — which is what makes this an end-to-end claim
	// and not a restatement of what the service put in the map.
	require.Equal(t, []string{"QmREVyn7gTwpYmasPnrCgkT9CFHi2YX8w3nmqsd3PzQvvU"},
		req.Headers["X-Taubyte-Cron-Id"])
	require.Equal(t, []string{"/everyminute"}, req.Headers["X-Taubyte-Cron-Path"],
		"at the project root, which is where this project puts it")

	// AND ONLY ONE NODE FIRED IT. A second fire of the same slot would be two
	// nodes agreeing to do the same job, which is exactly what one leader
	// handing one minute to one node exists to prevent — so this waits out the
	// rest of the slot rather than asserting on the instant.
	//
	// WHAT COULD MAKE IT FAIL IS WORTH KNOWING, because none of it can happen
	// here. A second node would have to be handing out minutes, which needs two
	// leaders of one raft namespace. The same node would have to hand the same
	// minute out twice, which its own memory refuses. Or the fire would have to
	// be redelivered, which needs a nack or a claim that expired — and the tier
	// below answers 200 in microseconds against a two-minute visibility
	// timeout. A failure here is a real duplicate and the budget is not the
	// thing to change.
	//
	// The turns that keep happening during this window hand out LATER minutes,
	// which are later slots: this counts one slot and not every fire.
	slot := req.Headers["X-Taubyte-Fired-At"][0]
	require.Never(t, func() bool { return fired.countOf(slot) > 1 },
		20*time.Second, time.Second, "one slot was fired more than once")
}

// steppable is a clock a test moves: the wall clock, started somewhere else and
// carried forward by whatever has been added to it.
//
// IT RUNS AT REAL RATE, AND THAT IS THE LOAD-BEARING HALF rather than an
// implementation detail of how the offset is kept. A clock that moves only when
// a test moves it stops a node's presence machine dead: pkg/membership promotes
// a peer to stable when now.Sub(liveSince) reaches the stabilize window
// (membership.go:305), which is dreamStabilize for these nodes
// (node/dream/init.go, and services/discovery/config.go for the minute a cloud
// keeps) — so under a frozen clock that difference is zero forever, no peer
// ever becomes stable,
// and Discovery().Placement() is EMPTY for as long as the universe runs.
// services/discovery/placement_test.go:50 asserts exactly that, as correct
// behaviour: a peer that has just appeared holds nothing.
//
// So a frozen clock is not a slow universe, it is a universe in which no
// database can ever be placed on anything — and the object-by-hand test this
// one replaces published straight into the register and never asked the fleet a
// question that would have shown it. deployFleet asks it, which is how this
// surfaced.
//
// ADDING TO IT STILL JUMPS WHOLE SLOTS, which is all this test ever wanted from
// a clock: the offset moves a minute at a time while the wall clock underneath
// carries presence at the rate it was written for.
//
// Both nodes read it from their own goroutines while this one writes it, so it
// is guarded rather than a field somebody remembers not to touch.
type steppable struct {
	// base is where this universe's clock reads at from, and from is the real
	// instant that reading was taken. Neither moves again.
	base time.Time
	from time.Time

	mu     sync.Mutex
	offset time.Duration
}

// clockFrom starts a steppable at a stated time.
func clockFrom(at time.Time) *steppable {
	return &steppable{base: at, from: time.Now()}
}

func (s *steppable) Now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.base.Add(s.offset + time.Since(s.from))
}

func (s *steppable) add(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.offset += d
}

// scheduleTarget is what projects/with/cron's schedule edges to.
//
// ITS OWN, AND NOT cache_test's target(), which resolves to the same four
// strings today. That they agree is a coincidence of two projects naming their
// function `cacher`, not a fact either one depends on — and a cron that read
// another project's address would go looking somewhere else the day that
// project renamed anything.
func scheduleTarget() specs.Address {
	return specs.Address{Project: "shop", Environment: "prod", Kind: "function", Name: "cacher"}
}

// firing is a tier that records every request its instances are handed.
type firing struct {
	name string

	mu   sync.Mutex
	reqs []*invoke.Request
}

func (f *firing) Name() string { return f.name }

func (f *firing) Start(context.Context, invoke.Spec) (invoke.Instance, error) {
	return &firingInstance{tier: f}, nil
}

func (f *firing) seen() []*invoke.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*invoke.Request(nil), f.reqs...)
}

func (f *firing) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

// countOf is how many times one slot was fired.
func (f *firing) countOf(slot string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	var n int
	for _, req := range f.reqs {
		if at := req.Headers["X-Taubyte-Fired-At"]; len(at) > 0 && at[0] == slot {
			n++
		}
	}
	return n
}

type firingInstance struct{ tier *firing }

func (i *firingInstance) Serve(_ context.Context, req *invoke.Request) (*invoke.Response, error) {
	i.tier.mu.Lock()
	i.tier.reqs = append(i.tier.reqs, req)
	i.tier.mu.Unlock()
	return &invoke.Response{Status: http.StatusOK}, nil
}

func (i *firingInstance) Close() error { return nil }
