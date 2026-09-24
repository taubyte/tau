package primitives

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/queue"
	"github.com/taubyte/tau/pkg/raft"
)

// cron_test drives the clock rather than waiting for it, which is the whole
// reason the leader's turn takes a `now`: every branch that decides whether
// somebody's job runs — and how many times — is reachable without a sleep.
//
// WHAT A TURN IS HERE IS TWO ACTS AND NOT ONE. The leader hands a WINDOW of
// minutes to a node, and the node works out what the window owes, pushes it and
// claims it back. The second act is a CronTask call, so most tests below keep
// both in this process — a service composed with no view of a fleet tasks
// itself — and the ones about the tasking itself say where the window went.

const cronID = "QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zc"

// clocking is a service with one schedule and somewhere for its fires to land.
func clocking(t *testing.T, s Schedule) (*harness, *fires) {
	t.Helper()
	return clockingOn(t, newCluster(), s)
}

// clockingOn is the same over a cluster the test supplies, for the cases about
// which node may write.
func clockingOn(t *testing.T, cluster leader, s Schedule) (*harness, *fires) {
	t.Helper()

	fired := &fires{status: 200}
	held := &listing{schedules: []Schedule{s}}
	h := startingOn(t, cluster,
		WithSchedules(held.of),
		WithFire(fired.serve))

	return h, fired
}

// tasking is a clock with somewhere to send a window that is NOT this node, so
// a test can see the message the leader's turn is made of.
func tasking(t *testing.T, s Schedule) (*taskings, *harness, *fires) {
	t.Helper()

	sent := &taskings{}
	fired := &fires{status: 200}
	held := &listing{schedules: []Schedule{s}}
	h := startingOn(t, newCluster(),
		WithSchedules(held.of),
		WithFire(fired.serve),
		WithTasking(sent.take))

	return sent, h, fired
}

// nightly is the fixture's schedule: one cron edging to one function.
func nightly(spec string) Schedule {
	return Schedule{
		Handle: projectID + "/prod/" + cronID,
		Name:   "nightly",
		ID:     cronID,
		Path:   "/billing/nightly",
		Spec:   spec,
		Fires:  []Target{{Project: projectID, Environment: "prod", Kind: "function", Name: "checkout"}},
	}
}

// at is a moment in UTC, for a schedule written in UTC.
func at(hour, minute int) time.Time {
	return time.Date(2026, 9, 5, hour, minute, 0, 0, time.UTC)
}

// turn is one whole turn of the leader's clock at a stated moment: the tasking,
// and the pass the tasked node runs.
//
// IT MOVES THE SERVICE'S CLOCK AND NOT A SECOND ONE. Everything a queue decides
// — when a claim expires, when a backoff is up — is decided against the same
// time the schedule is, which is what a universe driving the clock also gets.
func (h *harness) turn(t *testing.T, now time.Time) {
	t.Helper()

	h.clock.set(now)

	cluster, err := h.consensus()
	require.NoError(t, err)
	if !cluster.IsLeader() {
		return
	}

	// NO BARRIER HERE, WHICH IS THE SERVICE'S OWN SHAPE. The leader's turn
	// reads nothing replicated; the barrier belongs to the pass the tasked node
	// runs, and [crons.accept] takes it where that pass happens.
	h.crons.tick(t.Context(), now)
}

// tasked is the minute this node's clock has already handed out.
func (h *harness) tasked() time.Time {
	h.crons.mu.Lock()
	defer h.crons.mu.Unlock()
	return h.crons.tasked
}

// primed is a clock that has already taken its first turn, which is the
// ordinary state: the first turn a leader takes records where the clock is and
// hands out nothing.
func primed(t *testing.T, s Schedule, since time.Time) (*harness, *fires) {
	t.Helper()

	h, fired := clocking(t, s)
	h.turn(t, since)
	require.Empty(t, fired.seen(), "the first turn a leader takes fires nothing")
	require.Equal(t, since, h.tasked(), "the first turn did not record where the clock was")
	return h, fired
}

// TestTHEFIRSTTURNALEADERTAKESFIRESNOTHING. Deploying a nightly job at noon
// must not immediately run last night — and a node that has just won an
// election must not re-run the minute its predecessor was already handing out,
// which is the same sentence read from the other side.
func TestTheFirstTurnALeaderTakesFiresNothing(t *testing.T) {
	// A minutely cron is due in every minute there is, so anything that fired
	// on a first turn would fire here.
	h, fired := clocking(t, nightly("* * * * *"))

	h.turn(t, at(12, 0))
	require.Empty(t, fired.seen(), "a schedule ran before the clock said where it was")

	// AND WHERE THE CLOCK IS, IS WHAT THE TURN RECORDED, so the next minute
	// fires and nothing before it ever does.
	require.Equal(t, at(12, 0), h.tasked())

	h.turn(t, at(12, 1))
	require.Eventually(t, func() bool { return len(fired.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"2026-09-05T12:01:00Z"}, fired.seen()[0].Headers[headerFiredAt])
}

// TestTheClockCOMESROUNDAndTheTargetIsInvoked, with nothing asking for it.
func TestACronFiresItsTarget(t *testing.T) {
	h, fired := primed(t, nightly("0 3 * * *"), at(2, 30))

	h.turn(t, at(2, 59))
	require.Empty(t, fired.seen(), "not yet")

	h.turn(t, at(3, 0))
	require.Eventually(t, func() bool { return len(fired.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)
	req := fired.seen()[0]
	require.Equal(t, "POST", req.Method)
	require.Equal(t, []string{"nightly"}, req.Headers[headerCron])
	require.Equal(t, []string{cronID}, req.Headers[headerCronID],
		"the name is scoped and renameable; the id is what the callee can look up")
	require.Equal(t, []string{"/billing/nightly"}, req.Headers[headerCronPath],
		"where it lives, which the name alone does not say")
	require.Equal(t, []string{"2026-09-05T03:00:00Z"}, req.Headers[headerFiredAt],
		"the SLOT and not now, so a late run says what it is a run of")
}

// TestAFIREISANITEMBeforeItIsAnInvocation: the tasked node pushes, and
// something else invokes.
func TestAFireIsAnItemInTheSchedulesOwnQueue(t *testing.T) {
	h, fired := primed(t, nightly("0 3 * * *"), at(2, 30))
	fired.hold()

	h.turn(t, at(3, 0))

	// The same pass claimed it back and the fire is blocked inside the
	// invocation — so what the namespace holds is a CLAIMED item rather than
	// nothing at all.
	held, err := h.crons.open(nightly("0 3 * * *"))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_, claimed := held.Depth()
		return claimed == 1
	}, 5*time.Second, 10*time.Millisecond, "the fire is not an item")

	fired.release()
	require.Eventually(t, func() bool {
		ready, claimed := held.Depth()
		return ready == 0 && claimed == 0
	}, 5*time.Second, 10*time.Millisecond, "a fire that answered 2xx was not acknowledged")
}

// TestONCEPerMinute, however often the clock is looked at. A turn that finds
// the clock still inside a minute it has already handed out does nothing at
// all.
func TestAMinuteIsHandedOutOnceHoweverOftenItIsChecked(t *testing.T) {
	h, fired := primed(t, nightly("0 3 * * *"), at(2, 30))

	h.turn(t, at(3, 0))
	for range 5 {
		h.turn(t, at(3, 0).Add(7*time.Second))
	}
	require.Eventually(t, func() bool { return len(fired.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)
	require.Never(t, func() bool { return len(fired.seen()) > 1 }, time.Second, 50*time.Millisecond)
}

// TestTHECLOCKSMEMORYISONELEADERSPROCESSANDITFAILSBYLOSING. There is no
// replicated record of a tick, so a node that wins an election has no memory of
// the minute its predecessor was handing out — and what it does about that is
// record where the clock is and hand out nothing, which loses at most one
// minute's slots rather than running somebody's billing twice.
func TestAFreshLeaderDoesNotRefireTheMinuteItInherited(t *testing.T) {
	h, fired := primed(t, nightly("* * * * *"), at(2, 59))

	h.turn(t, at(3, 0))
	require.Eventually(t, func() bool { return len(fired.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)

	// A clock that has just started over the same namespace, half a minute in.
	fresh := &crons{service: h.Service}
	fresh.tick(t.Context(), at(3, 0).Add(30*time.Second))

	require.Equal(t, at(3, 0), fresh.tasked, "a fresh leader did not record where the clock was")
	require.Never(t, func() bool { return len(fired.seen()) > 1 }, time.Second, 50*time.Millisecond,
		"the minute its predecessor had already handed out was handed out again")
}

// TestTHENEXTSlotDoesFire, so one tasking bounds one run and not all of them.
func TestTheFollowingDayFiresAgain(t *testing.T) {
	h, fired := primed(t, nightly("0 3 * * *"), at(2, 30))

	h.turn(t, at(3, 0))
	require.Eventually(t, func() bool { return len(fired.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)

	h.turn(t, at(3, 0).Add(24*time.Hour))
	require.Eventually(t, func() bool { return len(fired.seen()) == 2 }, 5*time.Second, 10*time.Millisecond)
}

// TestAMISSEDGapIsCaughtUpONCE. A cloud down from 03:00 to 03:55 owes the job
// one run, not eleven — the window is handed over whole and the tasked node
// takes the most recent slot in it.
func TestAMissedGapFiresOnceWhenTheCloudComesBack(t *testing.T) {
	h, fired := primed(t, nightly("*/5 * * * *"), at(3, 1))

	// Nothing looked at the clock between 03:01 and 03:55.
	h.turn(t, at(3, 55))
	require.Eventually(t, func() bool { return len(fired.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"2026-09-05T03:55:00Z"}, fired.seen()[0].Headers[headerFiredAt],
		"the most recent slot in the gap, and the rest of the gap skipped")
	require.Never(t, func() bool { return len(fired.seen()) > 1 }, time.Second, 50*time.Millisecond)
}

// TestAGAPLongerThanADayIsNotMadeUp. A cloud that was down for a month should
// come back and do today's work.
func TestAVeryLateScheduleIsNotReplayed(t *testing.T) {
	h, fired := primed(t, nightly("0 3 * * *"), at(3, 30).Add(-30*24*time.Hour))

	h.turn(t, at(3, 30))
	require.Eventually(t, func() bool { return len(fired.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"2026-09-05T03:00:00Z"}, fired.seen()[0].Headers[headerFiredAt],
		"today's run, not a month of them")
}

// TestTHEZONEIsTheSchedulesOwn. "0 3 * * *" is a different instant in every
// zone, and a fleet spread across regions firing at whichever local time each
// node held would be a job that happens several times.
func TestTheScheduleFiresInTheZoneItWasWrittenIn(t *testing.T) {
	s := nightly("0 3 * * *")
	s.Timezone = "Europe/Paris"
	h, fired := primed(t, s, at(0, 30))

	// Paris is UTC+2 in September, so 03:00 there is 01:00 UTC.
	h.turn(t, at(1, 0))
	require.Eventually(t, func() bool { return len(fired.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"2026-09-05T01:00:00Z"}, fired.seen()[0].Headers[headerFiredAt])
}

// TestAZoneThisMachineDoesNotHaveIsNotAGuess.
func TestAnUnknownTimezoneFiresNothing(t *testing.T) {
	s := nightly("0 3 * * *")
	s.Timezone = "Mars/Olympus"
	h, fired := primed(t, s, at(2, 30))

	h.turn(t, at(3, 0))
	require.Empty(t, fired.seen())
}

// TestAnExpressionThatIsNotOneFiresNothing, rather than being read as some
// other schedule.
func TestAMalformedScheduleFiresNothing(t *testing.T) {
	h, fired := primed(t, nightly("every tuesday-ish"), at(2, 30))

	h.turn(t, at(3, 0))
	require.Empty(t, fired.seen())
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

// TestSLOTATIsTheMostRecentSlotInTheWINDOW, which is the whole of what a
// tasking hands over and the whole of what it answers.
func TestSlotAtIsTheMostRecentSlotInTheWindow(t *testing.T) {
	slot, ok := slotAt(Schedule{Name: "t", Spec: "*/15 * * * *", Timezone: "UTC"}, at(3, 1), at(3, 44))
	require.True(t, ok)
	require.Equal(t, at(3, 30), slot, "the most recent, and the one at 03:15 skipped")

	_, ok = slotAt(Schedule{Name: "t", Spec: "0 3 * * *", Timezone: "UTC"}, at(3, 0), at(3, 2))
	require.False(t, ok, "the slot at 03:00 is behind the window this tasking opened")

	// An empty zone is the vocabulary's own default rather than a refusal.
	slot, ok = slotAt(Schedule{Name: "t", Spec: "0 3 * * *"}, at(2, 30), at(3, 0))
	require.True(t, ok)
	require.Equal(t, at(3, 0), slot)
}

// ------------------------------------------------------------------ overlap

// TestOVERLAPIsSkippedByDefault, which is the only value that cannot make
// things worse.
//
// AND THE FACT IT DECIDES ON IS THE QUEUE'S. What says "a run is still going"
// is an item this namespace holds, not a flag in the memory of whichever node
// led when the run started — so a schedule that is skipped is skipped on the
// evidence and not on a guess.
func TestASecondTickIsSkippedWhileTheFirstRuns(t *testing.T) {
	h, fired := primed(t, nightly("* * * * *"), at(2, 59))
	fired.hold()

	h.turn(t, at(3, 0))
	require.Eventually(t, func() bool { return fired.inflight() == 1 }, 5*time.Second, 10*time.Millisecond)

	// The clock comes round with the first run still going: the slot is
	// dropped, which is what `skip` means.
	h.turn(t, at(3, 1))
	require.Never(t, func() bool { return len(fired.seen()) > 1 }, time.Second, 50*time.Millisecond)

	fired.release()
	require.Never(t, func() bool { return len(fired.seen()) > 1 }, time.Second, 50*time.Millisecond)
}

// TestOVERLAPQueuedRunsAfterwards, and it runs the period it was queued FOR.
//
// THE QUEUE IS THE BACKLOG, which is the one thing that changed with the
// replicated hold this replaces: a slot that comes round while a run is going
// is pushed as an item behind it rather than kept as a key one deep. The
// vocabulary's own warning about this value — that it "turns a slow job into an
// unbounded backlog" — is now literally what it does.
func TestAQueuedTickRunsWhenTheFirstEnds(t *testing.T) {
	s := nightly("* * * * *")
	s.OnOverlap = overlapQueue

	h, fired := primed(t, s, at(2, 59))
	fired.hold()

	h.turn(t, at(3, 0))
	require.Eventually(t, func() bool { return fired.inflight() == 1 }, 5*time.Second, 10*time.Millisecond)

	// 03:01 comes round while 03:00 is still going, so it waits as an item
	// rather than being dropped.
	h.turn(t, at(3, 1))
	require.Never(t, func() bool { return len(fired.seen()) > 1 }, time.Second, 50*time.Millisecond)

	fired.release()

	// THE RUN HAS ENDED WHEN ITS ITEM IS SETTLED, not when the invocation
	// returned — the ack is the delivery goroutine's last act, and what the
	// waiting slot needs is a schedule with nothing out.
	require.Eventually(t, func() bool { return idle(t, h, s) }, 5*time.Second, 10*time.Millisecond)

	h.turn(t, at(3, 2))
	require.Eventually(t, func() bool { return len(fired.seen()) == 2 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"2026-09-05T03:01:00Z"}, fired.seen()[1].Headers[headerFiredAt],
		"the period that was queued, and not the one that came round since")
}

// TestOVERLAPAllowedRunsAlongside.
func TestAnAllowedOverlapRunsAlongside(t *testing.T) {
	s := nightly("* * * * *")
	s.OnOverlap = overlapAllow

	h, fired := primed(t, s, at(2, 59))
	fired.hold()

	h.turn(t, at(3, 0))
	require.Eventually(t, func() bool { return fired.inflight() == 1 }, 5*time.Second, 10*time.Millisecond)

	h.turn(t, at(3, 1))
	require.Eventually(t, func() bool { return fired.inflight() == 2 }, 5*time.Second, 10*time.Millisecond)
	fired.release()
}

// ------------------------------------------------------- who may fire at all

// TestAFOLLOWERTASKSNOTHING, which is what makes a fleet fire once: raft
// already elected one node to be the clock.
func TestAFollowerFiresNothing(t *testing.T) {
	h, fired := primed(t, nightly("0 3 * * *"), at(2, 30))

	h.clock.set(at(3, 0))
	h.cluster.stepDown()
	h.cycle(t.Context())
	require.Empty(t, fired.seen())

	// AND THE CLOCK IS UNTOUCHED, so the node that does lead still owes the
	// minute rather than finding this one has spent it.
	require.Equal(t, at(2, 30), h.tasked())
}

// TestACLOCKWITHONLYONEOFITSHALVESISREFUSED. A service told which schedules
// exist and given no way to fire one hands out every minute in the cloud and
// delivers nothing, which is worse than not running a clock at all.
func TestAServiceGivenHalfAClockIsRefused(t *testing.T) {
	_, err := New(nil)
	require.ErrorIs(t, err, ErrNoHost)

	h := starting(t)
	_, err = New(h.host,
		WithRegister(&published{revision: 1, object: compiled()}),
		WithConsensus(func(string) (raft.Cluster, error) { return newCluster(), nil }),
		WithRecords(newStores()),
		WithSchedules(func(context.Context) []Schedule { return nil }))
	require.ErrorIs(t, err, ErrHalfAClock)
}

// TestANODEWITHNOSCHEDULESOURCERUNSNOCLOCK, which is the composition a node
// that holds the namespace and no worker has.
func TestAServiceWithNoScheduleSourceRunsNoClock(t *testing.T) {
	h := starting(t)
	require.Nil(t, h.Service.schedules)

	// The turn still runs, because the lifecycle pass rides the same one.
	h.cycle(t.Context())
}

// TestAWINDOWNOTHINGCOULDCOMMITISNOTSPENT. Every push being refused is one
// condition and not many — this node stopped being the one that may write — so
// the window goes back unspent, with nothing already committed for the next
// tasking to duplicate.
func TestAWindowNothingCouldCommitIsNotSpent(t *testing.T) {
	cluster := &brittle{fakeCluster: newCluster()}
	h, fired := clockingOn(t, cluster, nightly("0 3 * * *"))
	h.turn(t, at(2, 30))

	cluster.refuseWrites(true)
	h.turn(t, at(3, 0))
	require.Empty(t, fired.seen())
	require.Equal(t, at(2, 30), h.tasked(),
		"a window nothing could commit was handed out anyway")

	// AND THE NEXT TURN THAT CAN WRITE RUNS IT, once rather than never.
	cluster.refuseWrites(false)
	h.turn(t, at(3, 0).Add(30*time.Second))
	require.Eventually(t, func() bool { return len(fired.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"2026-09-05T03:00:00Z"}, fired.seen()[0].Headers[headerFiredAt])
	require.Never(t, func() bool { return len(fired.seen()) > 1 }, time.Second, 50*time.Millisecond)
}

// ------------------------------------------------------------- the tasking

// TestTHEWINDOWLEAVESTHELEADER, which is the whole point of the minute being a
// MESSAGE: the walk over every schedule belongs to the node that was tasked,
// and the leader's turn is one call whether the cloud holds ten crons or ten
// million.
func TestTheWindowLeavesTheLeaderThroughTheTaskingItWasGiven(t *testing.T) {
	sent, h, fired := tasking(t, nightly("0 3 * * *"))

	h.turn(t, at(2, 30))
	require.Empty(t, sent.seen(), "the first turn a leader takes tasks nobody")

	h.turn(t, at(3, 0))
	require.Equal(t, [][2]time.Time{{at(2, 30), at(3, 0)}}, sent.seen())

	// AND THIS NODE GENERATED NOTHING. The window went somewhere; a leader that
	// also quietly did the work would be two nodes owed the same minute.
	require.Empty(t, fired.seen())
}

// TestAWINDOWNOBODYTOOKISNOTSPENT. Re-tasking is the whole of the recovery —
// there is no task record, no lease and no retry queue — so a leader that
// advanced past a refused window would lose the minute outright.
func TestAWindowTheTaskedNodeWouldNotTakeIsNotSpent(t *testing.T) {
	sent, h, _ := tasking(t, nightly("0 3 * * *"))
	h.turn(t, at(2, 30))

	sent.refuse(errors.New("that node has gone"))
	h.turn(t, at(3, 0))
	require.Equal(t, at(2, 30), h.tasked(), "a window nobody took was recorded as handed out")

	// AND THE NEXT TURN HANDS OUT THE SAME WINDOW, WIDENED — which is why a
	// missed minute is caught up once rather than replayed.
	sent.refuse(nil)
	h.turn(t, at(3, 1))
	require.Equal(t, [][2]time.Time{
		{at(2, 30), at(3, 0)},
		{at(2, 30), at(3, 1)},
	}, sent.seen())
	require.Equal(t, at(3, 1), h.tasked())
}

// TestATASKINGTHATLANDSWHEREITCANNOTCOMMITISSENTON. A fire is a write and a
// write is refused off the leader, so a node that generated anyway would make
// work that cannot land — services/database/forward.go's shape, and its reason.
func TestATaskingThatLandsWhereItCannotCommitIsSentToTheNodeThatCan(t *testing.T) {
	h, fired := clocking(t, nightly("0 3 * * *"))
	h.clock.set(at(3, 0))
	h.cluster.stepDown()

	err := h.Local().CronTask(t.Context(), at(2, 59), at(3, 0))
	require.Error(t, err, "a node that may not write took the window anyway")
	require.ErrorContains(t, err, elsewhere.String(),
		"the refusal does not say which node the window went to")

	// AND NOTHING WAS GENERATED HERE, which is the half that would be silent:
	// items pushed on a follower are refused one at a time and the slot is gone.
	require.Empty(t, fired.seen())
}

// TestATASKINGTHATHASCOMEFARENOUGHISSERVEDHEREORREFUSEDHERE. Two nodes whose
// views of the group differ would otherwise pass one window to each other for
// as long as they disagreed; a refusal costs the leader one more minute.
func TestATaskingThatHasComeFarEnoughIsRefusedRatherThanSentOnAgain(t *testing.T) {
	h, fired := clocking(t, nightly("0 3 * * *"))
	h.clock.set(at(3, 0))
	h.cluster.stepDown()

	ctx := hopsIn(t.Context(), http.Header{hopHeader: []string{strconv.Itoa(maxHops)}})
	require.ErrorIs(t, h.crons.accept(ctx, at(2, 59), at(3, 0)), ErrNotLeading)
	require.Empty(t, fired.seen())
}

// TestAHOPCOUNTTHISNODECANNOTREADISTHECEILING. A header a node cannot
// understand must not be the thing that lets a window go round again.
func TestAnUnreadableHopCountIsReadAsTheCeiling(t *testing.T) {
	for _, raw := range []string{"not a number", "-1"} {
		require.Equal(t, maxHops,
			hopsOf(hopsIn(t.Context(), http.Header{hopHeader: []string{raw}})), raw)
	}

	// AND A TASKING THAT CARRIES NO COUNT HAS COME NOWHERE, which is the
	// leader's own turn and the local arm both.
	require.Equal(t, 0, hopsOf(hopsIn(t.Context(), http.Header{})))
}

// TestAWINDOWHANDEDTOANODETHATRUNSNOCLOCKISREFUSEDBYNAME. It holds the
// namespace and runs no compute, so it has no deployments to enumerate crons
// from — and answering "done" would be a minute nobody ever fires.
func TestAWindowHandedToANodeThatRunsNoClockIsRefusedByName(t *testing.T) {
	h := starting(t)
	require.ErrorIs(t, h.Local().CronTask(t.Context(), at(2, 59), at(3, 0)), ErrNoClock)
}

// --------------------------------------------------------------- the handoff

// TestAFIRETHATFAILSISDELIVEREDAGAIN, which is the whole reason a fire is an
// item: before this, a failed invocation was a log line, a spent slot and work
// that silently never happened.
func TestAFireThatFailsIsDeliveredAgain(t *testing.T) {
	h, fired := primed(t, nightly("* * * * *"), at(2, 59))
	fired.answer(500)

	h.turn(t, at(3, 0))
	require.Eventually(t, func() bool { return len(fired.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)

	// The nack put it back with fireRetry of backoff on it, so it is deliverable
	// again once that has gone by — and it is the SAME slot, not a new one.
	held, err := h.crons.open(nightly("* * * * *"))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		ready, _ := held.Depth()
		return ready == 1
	}, 5*time.Second, 10*time.Millisecond, "a failed fire was dropped")

	fired.answer(200)
	h.turn(t, at(3, 1))
	require.Eventually(t, func() bool { return len(fired.seen()) == 2 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, fired.seen()[0].Headers[headerFiredAt], fired.seen()[1].Headers[headerFiredAt],
		"the redelivery is a run of the same period")
}

// TestAMINUTEISHANDEDOUTONCEEVENWHENITSFIREISDELIVEREDTWICE. The guarantee this
// mechanism makes is worth writing down from both sides: the MINUTE is handed
// out exactly once and the FIRE is delivered at least once.
func TestASlotIsSpentOnceEvenWhenItsFireIsDeliveredTwice(t *testing.T) {
	h, fired := primed(t, nightly("* * * * *"), at(2, 59))
	fired.answer(500)

	h.turn(t, at(3, 0))
	require.Eventually(t, func() bool { return len(fired.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)

	// THE NACK IS WAITED FOR AND NOT ASSUMED, which is the wait the test above
	// already makes for the same reason. `fired.seen()` records that the
	// invocation STARTED; the item is given back afterwards, by the goroutine
	// that drove it. A second turn taken in between claims nothing — the item
	// is still invisible — and a redelivery that never comes then waits out a
	// deadline for an item nothing will hand out again on this clock.
	held, err := h.crons.open(nightly("* * * * *"))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		ready, _ := held.Depth()
		return ready == 1
	}, 5*time.Second, 10*time.Millisecond, "the failed fire was not given back")

	fired.answer(200)
	h.turn(t, at(3, 1))
	require.Eventually(t, func() bool { return len(fired.seen()) == 2 }, 5*time.Second, 10*time.Millisecond)

	// TWO DELIVERIES AND ONE SLOT. The 03:01 slot was not pushed at all — the
	// schedule still had a fire outstanding, which is what `skip` reads — so
	// both invocations are runs of 03:00.
	require.Equal(t, []string{"2026-09-05T03:00:00Z"}, fired.seen()[0].Headers[headerFiredAt])
	require.Equal(t, []string{"2026-09-05T03:00:00Z"}, fired.seen()[1].Headers[headerFiredAt])
}

// TestAFIRETHATKEEPSFAILINGENDSSOMEWHEREVISIBLE rather than in a log line.
func TestAFireThatExhaustsItsBudgetIsDeadLettered(t *testing.T) {
	s := nightly("0 3 * * *")
	h, fired := primed(t, s, at(2, 59))
	fired.answer(500)

	h.turn(t, at(3, 0))

	held, err := h.crons.open(s)
	require.NoError(t, err)

	// Every attempt the platform allows, each after its backoff. The pass that
	// hands a fire out is driven directly here, because what is being exercised
	// is the attempt budget rather than the clock: this schedule's next slot is
	// a day away.
	now := at(3, 0)
	for range queue.DefaultMaxAttempts {
		require.Eventually(t, func() bool {
			ready, claimed := held.Depth()
			return ready+claimed == 0 || ready == 1
		}, 5*time.Second, 10*time.Millisecond)
		now = now.Add(fireRetryCeiling + time.Second)
		h.clock.set(now)
		h.crons.drain(s, held)
	}

	dead, err := held.Dead()
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		buried, _ := dead.Depth()
		return buried == 1
	}, 5*time.Second, 10*time.Millisecond, "a fire that gave up vanished instead of being buried")
}

// TestJITTERIsWaitedOutAFTERTheCLAIM. A node that slept before claiming would
// leave the fire unclaimed for the length of the window, which is exactly long
// enough for somebody else to be handed the same work.
func TestTheSlotIsClaimedBeforeTheJitterIsWaitedOut(t *testing.T) {
	s := nightly("0 3 * * *")
	s.Jitter = 300 * time.Millisecond

	h, fired := primed(t, s, at(2, 30))
	h.turn(t, at(3, 0))

	held, err := h.crons.open(s)
	require.NoError(t, err)

	// The item is already CLAIMED — invisible to anybody else — while the fire
	// is still waiting out its window.
	require.Eventually(t, func() bool {
		_, claimed := held.Depth()
		return claimed == 1
	}, 5*time.Second, 10*time.Millisecond, "the fire was not claimed before the wait")

	require.Eventually(t, func() bool { return len(fired.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)
	require.Never(t, func() bool { return len(fired.seen()) > 1 }, time.Second, 50*time.Millisecond)
}

// TestEVERYTARGETOFASLOTFIRESINTHESAMEPASS. A slot's targets are one run of
// the cron — "it fires whatever it edges to" is a sentence about one moment —
// so handing them out one per turn would spread a single slot over a minute per
// target and leave the schedule reading as busy for the whole of it.
func TestEveryTargetOfASlotFiresInTheSameTurn(t *testing.T) {
	s := nightly("0 3 * * *")
	s.Fires = []Target{
		{Project: projectID, Environment: "prod", Kind: "function", Name: "checkout"},
		{Project: projectID, Environment: "prod", Kind: "function", Name: "invoices"},
		{Project: projectID, Environment: "prod", Kind: "function", Name: "ledger"},
	}

	h, fired := primed(t, s, at(2, 30))
	h.turn(t, at(3, 0))

	require.Eventually(t, func() bool { return len(fired.seen()) == 3 }, 5*time.Second, 10*time.Millisecond,
		"one turn delivered part of a slot")
	require.Never(t, func() bool { return len(fired.seen()) > 3 }, time.Second, 50*time.Millisecond)
}

// -------------------------------------------------------------- a run begins

// beginning is a cron that begins a workflow run and fires nothing, which is
// the shape the fixture's own cron has: a clock that kicks off a pipeline.
func beginning(id string) Schedule {
	return Schedule{
		Handle: projectID + "/prod/" + cronID,
		Name:   "nightly",
		Spec:   "0 3 * * *",
		Starts: []invoke.Grant{grantFor(id)},
	}
}

// TestASTARTISANITEMLIKEEVERYOTHERFIRE. It used to be sent straight out of the
// turn that spent the slot — a write with no queue of its own — so a send
// refused by a group still forming spent the period and started nothing, with
// nothing anywhere to try it again.
func TestACronThatStartsAWorkflowSendsFromTheDelivery(t *testing.T) {
	s := beginning(jobsID)
	h, _ := primed(t, s, at(2, 30))
	h.turn(t, at(3, 0))

	// THE RUN LANDS IN THE WORKFLOW'S OWN FIRST STEP'S QUEUE, under an id that
	// is the cron and the SLOT — so a fire this queue delivered twice is still
	// one run.
	declared, err := h.Service.declared(t.Context(), address{project: projectID, environment: "prod", id: jobsID}, queueType)
	require.NoError(t, err)
	opened, err := h.Service.work.open(declared)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		ready, _ := opened.Depth()
		return ready == 1
	}, 5*time.Second, 10*time.Millisecond, "the run never reached the workflow's queue")

	item, err := opened.Claim()
	require.NoError(t, err)
	require.Equal(t, startRunID("nightly", at(3, 0)), item.ID)
}

// TestASTARTTHATCOULDNOTBESENTCOMESBACK, which is what being an item buys it:
// the work is retried by the queue rather than lost with the turn that made it.
func TestAStartThatCouldNotBeSentIsDeliveredAgain(t *testing.T) {
	// A handle naming nothing this project declares: the send is refused every
	// time, exactly as a group still forming would refuse it once.
	s := beginning("QmYwAPJzv5CZsnAzt8auVTL6aKYQYiKRcHTbLuxSXKZJgp")

	h, _ := primed(t, s, at(2, 30))
	h.turn(t, at(3, 0))

	opened, err := h.crons.open(s)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		ready, claimed := opened.Depth()
		return ready == 1 && claimed == 0
	}, 5*time.Second, 10*time.Millisecond, "a start that was refused was acknowledged anyway")
}

// TestACRONTHATSTARTSANDFIRESOWESTWOITEMS, so one failure does not nack the
// other and each gets its own attempt budget.
//
// THE TWO ITEMS ARE CAUGHT IN TWO STATES THAT LAST, which is what makes this a
// test of the claim rather than of the machine it runs on. It used to hold the
// fire and let the START SUCCEED, so the two-item depth it asserted existed for
// the few hundred microseconds between the drain's claim and the start's ack —
// measured at ~600µs — and passed only while testify's first check won that
// race. Under contention the check lost it and the case failed describing
// nothing. So the start here names a queue nothing declares: it is refused,
// nacked back to ready behind its retry interval, and STAYS there, beside a
// fire still inside its invocation. One item failed, the other did not, and
// both are still the slot's own.
func TestAStartAndAFireOfOneSlotAreTwoItems(t *testing.T) {
	s := nightly("0 3 * * *")
	s.Starts = []invoke.Grant{grantFor("QmYwAPJzv5CZsnAzt8auVTL6aKYQYiKRcHTbLuxSXKZJgp")}

	h, fired := primed(t, s, at(2, 30))
	fired.hold()
	t.Cleanup(fired.release)
	h.turn(t, at(3, 0))

	opened, err := h.crons.open(s)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		ready, claimed := opened.Depth()
		return ready == 1 && claimed == 1
	}, 5*time.Second, 10*time.Millisecond, "one slot owed one item for two different things")

	// AND IT STAYS TWO. A single item for both would have taken the fire down
	// with the start's failure, or acknowledged the start along with the fire.
	require.Never(t, func() bool {
		ready, claimed := opened.Depth()
		return ready != 1 || claimed != 1
	}, time.Second, 50*time.Millisecond, "the two items settled as one")
}

// --------------------------------------------------------------- fixtures

// idle is whether this schedule has no fire out with anybody, which is what
// "the previous run has ended" means to everything that reads it.
func idle(t *testing.T, h *harness, s Schedule) bool {
	t.Helper()

	q, err := h.crons.open(s)
	require.NoError(t, err)
	_, claimed := q.Depth()
	return claimed == 0
}

// listing is what the composer answers: which schedules this node's worker
// holds.
type listing struct {
	mu        sync.Mutex
	schedules []Schedule
}

func (l *listing) of(context.Context) []Schedule {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Schedule(nil), l.schedules...)
}

// taskings is the node the leader hands a window to, standing in for a stream:
// every window it was given, in order, and whether it took them.
type taskings struct {
	mu      sync.Mutex
	handed  [][2]time.Time
	refusal error
}

func (k *taskings) take(_ context.Context, since, upto time.Time) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.handed = append(k.handed, [2]time.Time{since, upto})
	return k.refusal
}

func (k *taskings) seen() [][2]time.Time {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([][2]time.Time(nil), k.handed...)
}

// refuse is a tasked node that will not take the window, which is the one thing
// the leader acts on.
func (k *taskings) refuse(err error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.refusal = err
}

// fires is where a claimed fire lands: what was invoked, with what, and whether
// it is still going.
type fires struct {
	mu       sync.Mutex
	reqs     []*invoke.Request
	running  int
	status   int
	blocking chan struct{}
}

func (f *fires) serve(ctx context.Context, _ Target, req *invoke.Request) (*invoke.Response, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.running++
	block, status := f.blocking, f.status
	f.mu.Unlock()

	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
		}
	}

	f.mu.Lock()
	f.running--
	f.mu.Unlock()
	return &invoke.Response{Status: status}, nil
}

// hold makes every fire from here on stay inside the invocation, which is what
// "a run is still going" has to mean for an overlap test.
func (f *fires) hold() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blocking = make(chan struct{})
}

func (f *fires) release() {
	f.mu.Lock()
	block := f.blocking
	f.blocking = nil
	f.mu.Unlock()
	if block != nil {
		close(block)
	}
}

func (f *fires) answer(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = status
}

func (f *fires) seen() []*invoke.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*invoke.Request(nil), f.reqs...)
}

func (f *fires) inflight() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}
