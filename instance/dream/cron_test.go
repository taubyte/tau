//go:build dreaming

package dream_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	nodeDream "github.com/taubyte/tau/instance/dream"
	"github.com/taubyte/tau/pkg/invoke"
)

// A schedule firing with only the cloud watching the clock: two nodes holding one
// deployment fire it once, decided by a replicated write. projects/with/cron is
// compiled.

func TestAScheduleFiresOnceAcrossAFleet(t *testing.T) {
	fired := &firing{name: "wasm"}
	defer nodeDream.Runtimes(nodeDream.Runtimes(fired)...)

	// No daylight-saving edge, so every slot walked is sixty seconds; the project says
	// `timezone: UTC` too.
	at := clockFrom(time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC))
	defer nodeDream.Clock(nodeDream.Clock(at.Now))

	// The host is inert: a cron reaches its target through the worker.
	cloud := deployFleet(t, 2, "with/cron/skip", "cron.example.com", cacherWASM(t))
	first, second := cloud[0].node, cloud[1].node

	// Both nodes must hold it; the budget is a stall report.
	holds := func(n *instance.Instance) bool {
		_, err := n.Worker().Resolve(t.Context(), cloud[0].function("cacher"))
		return err == nil
	}
	require.Eventually(t, func() bool { return holds(first) && holds(second) },
		formingBudget, time.Second, "the project never reached both nodes")

	// One fire. The clock is walked a minute at a time until a tasking lands. The
	// budget: the primitives group forming plus two 30s DefaultSweep turns (one
	// recording the clock, one handing out the window) and 1s polling.
	require.Eventually(t, func() bool {
		at.add(time.Minute)
		return fired.count() > 0
	}, formingBudget+90*time.Second, time.Second, "the schedule never fired")

	req := fired.seen()[0]
	require.Equal(t, "POST", req.Method)
	require.Equal(t, []string{"everyminute"}, req.Headers["X-Taubyte-Cron"])
	require.NotEmpty(t, req.Headers["X-Taubyte-Fired-At"])

	// The whole identity: the cron's id from the compiled project
	// (cron/everyminute.yaml).
	require.Equal(t, []string{"QmREVyn7gTwpYmasPnrCgkT9CFHi2YX8w3nmqsd3PzQvvU"},
		req.Headers["X-Taubyte-Cron-Id"])
	require.Equal(t, []string{"/everyminute"}, req.Headers["X-Taubyte-Cron-Path"],
		"at the project root, which is where this project puts it")

	// And only one node fired it: the rest of the slot is waited out. A duplicate would
	// need two leaders, a minute handed out twice, or a redelivery, none possible here,
	// so a failure is a real duplicate. Later turns hand out later slots.
	slot := req.Headers["X-Taubyte-Fired-At"][0]
	require.Never(t, func() bool { return fired.countOf(slot) > 1 },
		20*time.Second, time.Second, "one slot was fired more than once")
}

// steppable is a test-moved clock running at real rate plus an offset. Real rate
// matters: pkg/membership promotes a peer to stable after dreamStabilize of real
// elapsed time, so a frozen clock would leave placement empty forever. Adding to it
// jumps whole slots. Guarded, as both nodes read it.
type steppable struct {
	// base is where this cloud's clock reads at from, and from is the real
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
