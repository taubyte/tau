package node

import (
	"encoding/json"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/raft"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/worker"
)

// cron_test drives the clock rather than waiting for it, which is the whole
// reason step takes a `now`: every branch that decides whether somebody's job
// runs — and how many times — is reachable without a sleep.

const cronID = "QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zc"

// schedule is the fixture's knobs.
type schedule struct {
	Spec      string
	Timezone  string
	OnOverlap string
	Jitter    time.Duration
}

// withCron is a project whose one cron fires its one function.
func withCron(c schedule) []byte {
	target := map[string]any{
		"name":     "nightly",
		"schedule": c.Spec,
		"function": map[string]any{"checkout": map[string]any{"ops": []any{"invoke"}}},
	}
	if c.Timezone != "" {
		target["timezone"] = c.Timezone
	}
	if c.OnOverlap != "" {
		target["on_overlap"] = c.OnOverlap
	}
	if c.Jitter > 0 {
		target["jitter"] = float64(c.Jitter)
	}

	doc := map[string]any{
		"object": map[string]any{
			"id":   projectID,
			"name": "shop",
			"function": map[string]any{
				"QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY": map[string]any{
					"name":      "checkout",
					"execution": map[string]any{"call": "handle", "memory": 33554432},
					"source":    map[string]any{"kind": "component", "component": "QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVv"},
				},
			},
			"cron": map[string]any{cronID: target},
		},
		"indexes": map[string]any{
			"projects/" + projectID + "/function/checkout/links": []any{
				"branches/main/projects/" + projectID + "/function/QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY"},
			"projects/" + projectID + "/cron/nightly/links": []any{
				"branches/main/projects/" + projectID + "/cron/" + cronID},
		},
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return raw
}

// scheduling is a node holding a project with a cron, and the loop that fires
// it.
func scheduling(t *testing.T, answers *answering, compiled []byte) (*Node, *crons) {
	t.Helper()

	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}},
		WithRuntimes(answers))
	require.NoError(t, n.Start(t.Context()))

	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled,
	})
	require.NoError(t, err)

	_, err = n.Worker().Resolve(t.Context(), workerTarget())
	require.NoError(t, err)
	<-n.work.forming

	return n, &crons{node: n}
}

// primed is a schedule the node has already seen, which is the ordinary state:
// the first tick records where the clock is and fires nothing, so that
// deploying a nightly job at noon does not immediately run last night.
func primed(t *testing.T, answers *answering, compiled []byte, since time.Time) (*Node, *crons) {
	t.Helper()

	n, c := scheduling(t, answers, compiled)
	require.Equal(t, 0, c.step(t.Context(), since), "the first sight of a schedule fires nothing")
	require.Empty(t, answers.seen())
	return n, c
}

// at is a moment in UTC, for a schedule written in UTC.
func at(hour, minute int) time.Time {
	return time.Date(2026, 9, 5, hour, minute, 0, 0, time.UTC)
}

// TestASCHEDULESeenForTheFirstTimeDoesNotFireItsPast. Deploying a nightly job
// at noon must not immediately run last night, which is the one thing a new
// deployment must not do.
func TestTheFirstSightOfAScheduleFiresNothing(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, c := scheduling(t, answers, withCron(schedule{Spec: "0 3 * * *"}))

	require.Equal(t, 0, c.step(t.Context(), at(12, 0)))
	require.Empty(t, answers.seen())

	// AND WHERE THE CLOCK IS, IS RECORDED — so the next slot fires and the
	// previous one never does.
	cluster, err := n.work.consensus()
	require.NoError(t, err)
	recorded, held := cluster.Get(pastPrefix + "shop/prod/" + cronID)
	require.True(t, held, "the position was not recorded")
	require.Equal(t, strconv.FormatInt(at(12, 0).Unix(), 10), string(recorded))
}

// TestTheClockCOMESROUNDAndTheTargetIsInvoked, with nothing asking for it.
func TestACronFiresItsTarget(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	_, c := primed(t, answers, withCron(schedule{Spec: "0 3 * * *"}), at(2, 30))

	require.Equal(t, 0, c.step(t.Context(), at(2, 59)), "not yet")
	require.Equal(t, 1, c.step(t.Context(), at(3, 0)))

	require.Eventually(t, func() bool { return len(answers.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)
	req := answers.seen()[0]
	require.Equal(t, "POST", req.Method)
	require.Equal(t, []string{"nightly"}, req.Headers[headerCron])
	require.Equal(t, []string{"2026-09-05T03:00:00Z"}, req.Headers[headerFiredAt],
		"the SLOT and not now, so a late run says what it is a run of")
}

// TestONCEPerSlot, however often the clock is looked at. A ten-second tick over
// a one-minute slot would otherwise fire six times.
func TestASlotFiresOnceHoweverOftenItIsChecked(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	_, c := primed(t, answers, withCron(schedule{Spec: "0 3 * * *"}), at(2, 30))

	require.Equal(t, 1, c.step(t.Context(), at(3, 0)))
	for range 5 {
		require.Equal(t, 0, c.step(t.Context(), at(3, 0)))
	}
	require.Eventually(t, func() bool { return len(answers.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)
	require.Never(t, func() bool { return len(answers.seen()) > 1 }, time.Second, 50*time.Millisecond)
}

// TestTHERECORDIsReplicatedAndNotInMemory. A leader that kept the last slot in
// its own process would fire the 03:00 run again the moment leadership moved or
// the process restarted — and the vocabulary's own case for `on_overlap: queue`
// is a billing run where every period happens exactly once.
func TestAFreshLoopDoesNotRefireAClaimedSlot(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, c := primed(t, answers, withCron(schedule{Spec: "0 3 * * *"}), at(2, 30))

	require.Equal(t, 1, c.step(t.Context(), at(3, 0)))

	// A different loop over the same node: the in-memory state is gone and the
	// raft record is not.
	fresh := &crons{node: n}
	require.Equal(t, 0, fresh.step(t.Context(), at(3, 0)))

	cluster, err := n.work.consensus()
	require.NoError(t, err)
	recorded, held := cluster.Get(pastPrefix + "shop/prod/" + cronID)
	require.True(t, held, "nothing was recorded")
	require.Equal(t, strconv.FormatInt(at(3, 0).Unix(), 10), string(recorded))
}

// TestTHENEXTSlotDoesFire, so the record bounds one run and not all of them.
func TestTheFollowingDayFiresAgain(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	_, c := primed(t, answers, withCron(schedule{Spec: "0 3 * * *"}), at(2, 30))

	require.Equal(t, 1, c.step(t.Context(), at(3, 0)))
	require.Eventually(t, func() bool { return len(answers.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)

	require.Equal(t, 1, c.step(t.Context(), at(3, 0).Add(24*time.Hour)))
	require.Eventually(t, func() bool { return len(answers.seen()) == 2 }, 5*time.Second, 10*time.Millisecond)
}

// TestAMISSEDGapIsCaughtUpONCE. A cloud down from 03:00 to 03:55 owes the job
// one run, not eleven.
func TestAMissedGapFiresOnceWhenTheCloudComesBack(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	_, c := primed(t, answers, withCron(schedule{Spec: "*/5 * * * *"}), at(3, 1))

	// Nothing looked at the clock between 03:01 and 03:55.
	require.Equal(t, 1, c.step(t.Context(), at(3, 55)))
	require.Eventually(t, func() bool { return len(answers.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"2026-09-05T03:55:00Z"}, answers.seen()[0].Headers[headerFiredAt],
		"the most recent slot in the gap, and the rest of the gap skipped")
	require.Never(t, func() bool { return len(answers.seen()) > 1 }, time.Second, 50*time.Millisecond)
}

// TestAGAPLongerThanADayIsNotMadeUp. A cloud that was down for a month should
// come back and do today's work.
func TestAVeryLateScheduleIsNotReplayed(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	_, c := primed(t, answers, withCron(schedule{Spec: "0 3 * * *"}), at(3, 30).Add(-30*24*time.Hour))

	require.Equal(t, 1, c.step(t.Context(), at(3, 30)))
	require.Eventually(t, func() bool { return len(answers.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"2026-09-05T03:00:00Z"}, answers.seen()[0].Headers[headerFiredAt],
		"today's run, not a month of them")
}

// TestTHEZONEIsTheSchedulesOwn. "0 3 * * *" is a different instant in every
// zone, and a fleet spread across regions firing at whichever local time each
// node held would be a job that happens several times.
func TestTheScheduleFiresInTheZoneItWasWrittenIn(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	_, c := primed(t, answers, withCron(schedule{Spec: "0 3 * * *", Timezone: "Europe/Paris"}), at(0, 30))

	// Paris is UTC+2 in September, so 03:00 there is 01:00 UTC.
	require.Equal(t, 1, c.step(t.Context(), at(1, 0)))
	require.Eventually(t, func() bool { return len(answers.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"2026-09-05T01:00:00Z"}, answers.seen()[0].Headers[headerFiredAt])
}

// TestAZoneThisMachineDoesNotHaveIsNotAGuess.
func TestAnUnknownTimezoneFiresNothing(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	_, c := primed(t, answers, withCron(schedule{Spec: "0 3 * * *", Timezone: "Mars/Olympus"}), at(2, 30))

	require.Equal(t, 0, c.step(t.Context(), at(3, 0)))
	require.Empty(t, answers.seen())
}

// TestAnExpressionThatIsNotOneFiresNothing, rather than being read as some
// other schedule.
func TestAMalformedScheduleFiresNothing(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	_, c := primed(t, answers, withCron(schedule{Spec: "every tuesday-ish"}), at(2, 30))

	require.Equal(t, 0, c.step(t.Context(), at(3, 0)))
	require.Empty(t, answers.seen())
}

// TestFIVEFIELDSAndNotSix. A parser with a seconds field silently reads
// "0 3 * * *" as 03:00 on every minute of the third second, which is a
// different job entirely.
func TestTheExpressionIsTheFiveFieldForm(t *testing.T) {
	_, err := cronParser.Parse("0 3 * * *")
	require.NoError(t, err)

	_, err = cronParser.Parse("0 0 3 * * *")
	require.Error(t, err, "six fields would mean every existing expression changed meaning")
}

// TestOVERLAPIsSkippedByDefault, which is the only value that cannot make
// things worse — and the dropped tick is STILL RECORDED, because a slot left
// unrecorded would be tried again ten seconds later against the same run.
func TestASecondTickIsSkippedWhileTheFirstRuns(t *testing.T) {
	answers := &answering{name: "wasm", status: 200, block: make(chan struct{})}
	_, c := primed(t, answers, withCron(schedule{Spec: "*/5 * * * *"}), at(2, 59))

	require.Equal(t, 1, c.step(t.Context(), at(3, 0)))
	require.Eventually(t, func() bool { return answers.inflight() == 1 }, 5*time.Second, 10*time.Millisecond)

	// The clock comes round with the first run still going: the tick is
	// dropped, which is what `skip` means.
	require.Equal(t, 0, c.step(t.Context(), at(3, 5)))
	close(answers.block)

	require.Never(t, func() bool { return len(answers.seen()) > 1 }, time.Second, 50*time.Millisecond)
}

// TestOVERLAPQueuedRunsAfterwards, exactly once — the vocabulary's own warning
// about this value is that it turns a slow job into an unbounded backlog, so
// what is held is one tick and not all of them.
func TestAQueuedTickRunsWhenTheFirstEnds(t *testing.T) {
	answers := &answering{name: "wasm", status: 200, block: make(chan struct{})}
	_, c := primed(t, answers, withCron(schedule{Spec: "*/5 * * * *", OnOverlap: "queue"}), at(2, 59))

	require.Equal(t, 1, c.step(t.Context(), at(3, 0)))
	require.Eventually(t, func() bool { return answers.inflight() == 1 }, 5*time.Second, 10*time.Millisecond)

	// Three more ticks while it runs, and one of them is owed.
	for _, m := range []int{5, 10, 15} {
		c.step(t.Context(), at(3, m))
	}
	close(answers.block)

	require.Eventually(t, func() bool { return len(answers.seen()) == 2 }, 5*time.Second, 10*time.Millisecond)
	require.Never(t, func() bool { return len(answers.seen()) > 2 }, time.Second, 50*time.Millisecond)
}

// TestOVERLAPAllowedRunsAlongside.
func TestAnAllowedOverlapRunsAlongside(t *testing.T) {
	answers := &answering{name: "wasm", status: 200, block: make(chan struct{})}
	_, c := primed(t, answers, withCron(schedule{Spec: "*/5 * * * *", OnOverlap: "allow"}), at(2, 59))

	require.Equal(t, 1, c.step(t.Context(), at(3, 0)))
	require.Eventually(t, func() bool { return answers.inflight() == 1 }, 5*time.Second, 10*time.Millisecond)

	require.Equal(t, 1, c.step(t.Context(), at(3, 5)))
	require.Eventually(t, func() bool { return answers.inflight() == 2 }, 5*time.Second, 10*time.Millisecond)
	close(answers.block)
}

// TestAFOLLOWERFiresNothing, which is what makes a fleet fire once. A node with
// no consensus at all gets the same answer, because there is nothing to claim
// against.
func TestWithoutConsensusNothingFires(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, c := primed(t, answers, withCron(schedule{Spec: "0 3 * * *"}), at(2, 30))

	cluster, err := n.work.consensus()
	require.NoError(t, err)
	require.True(t, cluster.IsLeader(), "the mock cluster of one leads itself")

	bare, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}}, WithRuntimes(answers))
	require.Equal(t, 0, (&crons{node: bare}).step(t.Context(), at(3, 0)))
	require.Equal(t, 1, c.step(t.Context(), at(3, 0)))
}

// TestACRONInAProjectNobodyHoldsDoesNotFire, which is the enumeration rule and
// is a sharper consequence for a schedule than for a queue.
func TestAnUnheldDeploymentFiresNothing(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}},
		WithRuntimes(answers))
	require.NoError(t, n.Start(t.Context()))

	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: withCron(schedule{Spec: "0 3 * * *"}),
	})
	require.NoError(t, err)
	<-n.work.forming

	require.Equal(t, 0, (&crons{node: n}).step(t.Context(), at(3, 0)))
	require.Empty(t, n.Worker().Schedules(t.Context()))
}

// TestJITTERIsWaitedOutAFTERTheClaim. A node that slept first would leave the
// slot unclaimed for the length of the window, which is exactly long enough for
// another node to become leader and claim it too.
func TestTheSlotIsClaimedBeforeTheJitterIsWaitedOut(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, c := primed(t, answers, withCron(schedule{Spec: "0 3 * * *", Jitter: 30 * time.Second}), at(2, 30))

	require.Equal(t, 1, c.step(t.Context(), at(3, 0)))

	cluster, err := n.work.consensus()
	require.NoError(t, err)
	recorded, _ := cluster.Get(pastPrefix + "shop/prod/" + cronID)
	require.Equal(t, strconv.FormatInt(at(3, 0).Unix(), 10), string(recorded),
		"the slot was not claimed before the wait")
	require.Equal(t, 0, c.step(t.Context(), at(3, 0)), "and a second look claims nothing")
}

// scheduleFor is a Schedule a test drives directly, for the parts of slotAt a
// fixture cannot reach.
func scheduleFor(spec, zone string) worker.Schedule {
	return worker.Schedule{Name: "t", Spec: spec, Timezone: zone}
}

// TestSLOTATIsTheMostRecentSlotSinceTheSchedulesPosition, which is the whole of
// what it answers.
func TestSlotAtIsTheMostRecentSlotSincePast(t *testing.T) {
	slot, ok := slotAt(scheduleFor("*/15 * * * *", "UTC"), at(3, 1), at(3, 44))
	require.True(t, ok)
	require.Equal(t, at(3, 30), slot, "the most recent, and the one at 03:15 skipped")

	_, ok = slotAt(scheduleFor("0 3 * * *", "UTC"), at(3, 0), at(3, 2))
	require.False(t, ok, "the slot at 03:00 is already behind the position")

	// An empty zone is the vocabulary's own default rather than a refusal.
	slot, ok = slotAt(scheduleFor("0 3 * * *", ""), at(2, 30), at(3, 0))
	require.True(t, ok)
	require.Equal(t, at(3, 0), slot)
}

// unapplied is a node that has just won an election: entries its predecessor
// committed are in its log and not yet in its state. Get answers the stale
// applied view until Barrier is called, which is what Barrier is for —
// pkg/raft's own doc says Get "may return stale data" and "for strong
// consistency, call Barrier() first".
type unapplied struct {
	*raft.MockCluster

	mu      sync.Mutex
	stale   map[string][]byte
	applied bool
}

func withUnapplied(stale map[string][]byte) *unapplied {
	return &unapplied{MockCluster: raft.NewMockCluster(), stale: stale}
}

func (u *unapplied) Get(key string) ([]byte, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if !u.applied {
		value, held := u.stale[key]
		return value, held
	}
	return u.MockCluster.Get(key)
}

func (u *unapplied) Barrier(d time.Duration) error {
	u.mu.Lock()
	u.applied = true
	u.mu.Unlock()
	return u.MockCluster.Barrier(d)
}

// TestAFreshLeaderDoesNotFireASlotItsPredecessorAlreadyFired.
//
// Leading is not the same as knowing. A node that has just won an election
// holds committed entries it has not applied, and a schedule's position is read
// out of applied state — so without a barrier a new leader reads the position
// as its PREDECESSOR left it one slot ago, finds the current slot owing, and
// runs somebody's job a second time. Nothing errors and the position afterwards
// is exactly what it should be, so nothing anywhere ever notices.
//
// The vocabulary's example for this path is a billing run where every period
// must happen exactly once.
func TestAFreshLeaderDoesNotFireASlotItsPredecessorAlreadyFired(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	key := pastPrefix + "shop/prod/" + cronID

	// THE PREDECESSOR CLAIMED 03:00 AND DIED. Its entry is committed, so it is
	// in this node's log; this node's applied state still says 02:00.
	cluster := withUnapplied(map[string][]byte{
		key: []byte(strconv.FormatInt(at(2, 0).Unix(), 10)),
	})
	require.NoError(t, cluster.MockCluster.Set(key,
		[]byte(strconv.FormatInt(at(3, 0).Unix(), 10)), time.Second))

	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}},
		WithRuntimes(answers),
		WithRaft(func(string) (raft.Cluster, error) { return cluster, nil }))
	require.NoError(t, n.Start(t.Context()))

	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1,
		Compiled: withCron(schedule{Spec: "0 3 * * *"}),
	})
	require.NoError(t, err)
	_, err = n.Worker().Resolve(t.Context(), workerTarget())
	require.NoError(t, err)
	<-n.work.forming

	c := &crons{node: n}
	require.Equal(t, 0, c.step(t.Context(), at(3, 0).Add(5*time.Second)),
		"the 03:00 slot was already claimed by the node this one replaced")
	require.Empty(t, answers.seen(), "somebody's job ran twice")
}
