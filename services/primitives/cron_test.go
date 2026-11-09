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

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/queue"
)

// Drives the clock rather than waiting for it. Most tests keep both acts (the
// leader's tasking and the tasked pass) in this process; a service with no fleet view
// tasks itself.

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
		clockOf(held.of, fired.serve))

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
		clockOf(held.of, fired.serve),
		withTasking(sent.take))

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

// turn is one whole turn of the leader's clock at a stated moment, moving the
// service's own clock so queue deadlines and schedules agree.
func (h *harness) turn(t *testing.T, now time.Time) {
	t.Helper()

	h.clock.set(now)

	cluster, err := h.consensus()
	require.NoError(t, err)
	if !cluster.IsLeader() {
		return
	}

	// No barrier: the leader's turn reads nothing replicated; crons.accept takes it.
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
		_, claimed := depthOf(t, held)
		return claimed == 1
	}, 5*time.Second, 10*time.Millisecond, "the fire is not an item")

	fired.release()
	require.Eventually(t, func() bool {
		ready, claimed := depthOf(t, held)
		return ready == 0 && claimed == 0
	}, 5*time.Second, 10*time.Millisecond, "a fire that answered 2xx was not acknowledged")
}

func TestAMinuteIsHandedOutOnceHoweverOftenItIsChecked(t *testing.T) {
	h, fired := primed(t, nightly("0 3 * * *"), at(2, 30))

	h.turn(t, at(3, 0))
	for range 5 {
		h.turn(t, at(3, 0).Add(7*time.Second))
	}
	require.Eventually(t, func() bool { return len(fired.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)
	require.Never(t, func() bool { return len(fired.seen()) > 1 }, time.Second, 50*time.Millisecond)
}

func TestAFreshLeaderDoesNotRefireTheMinuteItInherited(t *testing.T) {
	h, fired := primed(t, nightly("* * * * *"), at(2, 59))

	h.turn(t, at(3, 0))
	require.Eventually(t, func() bool { return len(fired.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)

	// A clock that has just started over the same namespace, half a minute in.
	fresh := &crons{service: h.server}
	fresh.tick(t.Context(), at(3, 0).Add(30*time.Second))

	require.Equal(t, at(3, 0), fresh.tasked, "a fresh leader did not record where the clock was")
	require.Never(t, func() bool { return len(fired.seen()) > 1 }, time.Second, 50*time.Millisecond,
		"the minute its predecessor had already handed out was handed out again")
}

func TestALeaderLeadingAgainDoesNotRefireAMinuteAnotherHandedOut(t *testing.T) {
	s := nightly("* * * * *")
	h, fired := primed(t, s, at(2, 59))

	// Another leader hands out 03:00 while this one's process still holds 02:59, and
	// the fire settles, so the queue no longer holds it.
	other := &crons{service: h.server}
	other.advance(at(2, 59))
	h.clock.set(at(3, 0))
	other.tick(t.Context(), at(3, 0))
	require.Eventually(t, func() bool { return len(fired.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)
	held, err := h.crons.open(s)
	require.NoError(t, err)
	require.Eventually(t, func() bool { ready, claimed := depthOf(t, held); return ready+claimed == 0 },
		5*time.Second, 10*time.Millisecond, "the fire never settled")

	// This one leads again, its idea of the last minute a minute behind.
	h.turn(t, at(3, 0).Add(30*time.Second))
	require.Never(t, func() bool { return len(fired.seen()) > 1 }, time.Second, 50*time.Millisecond,
		"a minute another leader handed out was handed out again")
	require.Equal(t, at(3, 0), h.tasked(), "and it moves on past it")
}

func TestTheFollowingDayFiresAgain(t *testing.T) {
	h, fired := primed(t, nightly("0 3 * * *"), at(2, 30))

	h.turn(t, at(3, 0))
	require.Eventually(t, func() bool { return len(fired.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)

	h.turn(t, at(3, 0).Add(24*time.Hour))
	require.Eventually(t, func() bool { return len(fired.seen()) == 2 }, 5*time.Second, 10*time.Millisecond)
}

func TestAMissedGapFiresOnceWhenTheCloudComesBack(t *testing.T) {
	h, fired := primed(t, nightly("*/5 * * * *"), at(3, 1))

	// Nothing looked at the clock between 03:01 and 03:55.
	h.turn(t, at(3, 55))
	require.Eventually(t, func() bool { return len(fired.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"2026-09-05T03:55:00Z"}, fired.seen()[0].Headers[headerFiredAt],
		"the most recent slot in the gap, and the rest of the gap skipped")
	require.Never(t, func() bool { return len(fired.seen()) > 1 }, time.Second, 50*time.Millisecond)
}

func TestAVeryLateScheduleIsNotReplayed(t *testing.T) {
	h, fired := primed(t, nightly("0 3 * * *"), at(3, 30).Add(-30*24*time.Hour))

	h.turn(t, at(3, 30))
	require.Eventually(t, func() bool { return len(fired.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"2026-09-05T03:00:00Z"}, fired.seen()[0].Headers[headerFiredAt],
		"today's run, not a month of them")
}

func TestTheScheduleFiresInTheZoneItWasWrittenIn(t *testing.T) {
	s := nightly("0 3 * * *")
	s.Timezone = "Europe/Paris"
	h, fired := primed(t, s, at(0, 30))

	// Paris is UTC+2 in September, so 03:00 there is 01:00 UTC.
	h.turn(t, at(1, 0))
	require.Eventually(t, func() bool { return len(fired.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"2026-09-05T01:00:00Z"}, fired.seen()[0].Headers[headerFiredAt])
}

func TestAnUnknownTimezoneFiresNothing(t *testing.T) {
	s := nightly("0 3 * * *")
	s.Timezone = "Mars/Olympus"
	h, fired := primed(t, s, at(2, 30))

	h.turn(t, at(3, 0))
	require.Empty(t, fired.seen())
}

func TestAMalformedScheduleFiresNothing(t *testing.T) {
	h, fired := primed(t, nightly("every tuesday-ish"), at(2, 30))

	h.turn(t, at(3, 0))
	require.Empty(t, fired.seen())
}

func TestTheExpressionIsTheFiveFieldForm(t *testing.T) {
	_, err := cronParser.Parse("0 3 * * *")
	require.NoError(t, err)

	_, err = cronParser.Parse("0 0 3 * * *")
	require.Error(t, err, "six fields would mean every existing expression changed meaning")
}

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

func TestAServiceWithNoHostIsRefused(t *testing.T) {
	_, err := build(nil)
	require.ErrorIs(t, err, ErrNoHost)
}

func TestAServiceWithNoScheduleSourceRunsNoClock(t *testing.T) {
	h := starting(t)
	_, clocked := h.workers()
	require.False(t, clocked)

	// The turn still runs, because the lifecycle pass rides the same one.
	h.cycle(t.Context())
}

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

func TestATaskingThatHasComeFarEnoughIsRefusedRatherThanSentOnAgain(t *testing.T) {
	h, fired := clocking(t, nightly("0 3 * * *"))
	h.clock.set(at(3, 0))
	h.cluster.stepDown()

	ctx := hopsIn(t.Context(), http.Header{hopHeader: []string{strconv.Itoa(maxHops)}})
	require.ErrorIs(t, h.crons.accept(ctx, at(2, 59), at(3, 0)), ErrNotLeading)
	require.Empty(t, fired.seen())
}

func TestAnUnreadableHopCountIsReadAsTheCeiling(t *testing.T) {
	for _, raw := range []string{"not a number", "-1"} {
		require.Equal(t, maxHops,
			hopsOf(hopsIn(t.Context(), http.Header{hopHeader: []string{raw}})), raw)
	}

	// AND A TASKING THAT CARRIES NO COUNT HAS COME NOWHERE, which is the
	// leader's own turn and the local arm both.
	require.Equal(t, 0, hopsOf(hopsIn(t.Context(), http.Header{})))
}

func TestAWindowHandedToANodeThatRunsNoClockIsRefusedByName(t *testing.T) {
	h := starting(t)
	require.ErrorIs(t, h.Local().CronTask(t.Context(), at(2, 59), at(3, 0)), ErrNoClock)
}

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
		ready, _ := depthOf(t, held)
		return ready == 1
	}, 5*time.Second, 10*time.Millisecond, "a failed fire was dropped")

	fired.answer(200)
	h.turn(t, at(3, 1))
	require.Eventually(t, func() bool { return len(fired.seen()) == 2 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, fired.seen()[0].Headers[headerFiredAt], fired.seen()[1].Headers[headerFiredAt],
		"the redelivery is a run of the same period")
}

func TestASlotIsSpentOnceEvenWhenItsFireIsDeliveredTwice(t *testing.T) {
	h, fired := primed(t, nightly("* * * * *"), at(2, 59))
	fired.answer(500)

	h.turn(t, at(3, 0))
	require.Eventually(t, func() bool { return len(fired.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)

	// The nack is waited for: the invocation starts before the goroutine gives the item
	// back.
	held, err := h.crons.open(nightly("* * * * *"))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		ready, _ := depthOf(t, held)
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

func TestAFireThatExhaustsItsBudgetIsDeadLettered(t *testing.T) {
	s := nightly("0 3 * * *")
	h, fired := primed(t, s, at(2, 59))
	fired.answer(500)

	h.turn(t, at(3, 0))

	held, err := h.crons.open(s)
	require.NoError(t, err)

	// The delivery pass is driven directly: this exercises the attempt budget, and the
	// next slot is a day away.
	now := at(3, 0)
	for range queue.DefaultMaxAttempts {
		require.Eventually(t, func() bool {
			ready, claimed := depthOf(t, held)
			return ready+claimed == 0 || ready == 1
		}, 5*time.Second, 10*time.Millisecond)
		now = now.Add(fireRetryCeiling + time.Second)
		h.clock.set(now)
		h.crons.drain(s, held)
	}

	dead, err := held.Dead()
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		buried, _ := depthOf(t, dead)
		return buried == 1
	}, 5*time.Second, 10*time.Millisecond, "a fire that gave up vanished instead of being buried")
}

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
		_, claimed := depthOf(t, held)
		return claimed == 1
	}, 5*time.Second, 10*time.Millisecond, "the fire was not claimed before the wait")

	require.Eventually(t, func() bool { return len(fired.seen()) == 1 }, 5*time.Second, 10*time.Millisecond)
	require.Never(t, func() bool { return len(fired.seen()) > 1 }, time.Second, 50*time.Millisecond)
}

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

func TestACronThatStartsAWorkflowSendsFromTheDelivery(t *testing.T) {
	s := beginning(jobsID)
	h, _ := primed(t, s, at(2, 30))
	h.turn(t, at(3, 0))

	// THE RUN LANDS IN THE WORKFLOW'S OWN FIRST STEP'S QUEUE, under an id that
	// is the cron and the SLOT — so a fire this queue delivered twice is still
	// one run.
	declared, err := h.server.declared(t.Context(), address{project: projectID, environment: "prod", id: jobsID}, queueType)
	require.NoError(t, err)
	opened, err := h.server.work.open(declared)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		ready, _ := depthOf(t, opened)
		return ready == 1
	}, 5*time.Second, 10*time.Millisecond, "the run never reached the workflow's queue")

	item, err := opened.Claim()
	require.NoError(t, err)
	require.Equal(t, startRunID("nightly", at(3, 0)), item.ID)
}

func TestAStartThatCouldNotBeSentIsDeliveredAgain(t *testing.T) {
	// A handle naming nothing this project declares: the send is refused every
	// time, exactly as a group still forming would refuse it once.
	s := beginning("QmYwAPJzv5CZsnAzt8auVTL6aKYQYiKRcHTbLuxSXKZJgp")

	h, _ := primed(t, s, at(2, 30))
	h.turn(t, at(3, 0))

	opened, err := h.crons.open(s)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		ready, claimed := depthOf(t, opened)
		return ready == 1 && claimed == 0
	}, 5*time.Second, 10*time.Millisecond, "a start that was refused was acknowledged anyway")
}

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
		ready, claimed := depthOf(t, opened)
		return ready == 1 && claimed == 1
	}, 5*time.Second, 10*time.Millisecond, "one slot owed one item for two different things")

	// AND IT STAYS TWO. A single item for both would have taken the fire down
	// with the start's failure, or acknowledged the start along with the fire.
	require.Never(t, func() bool {
		ready, claimed := depthOf(t, opened)
		return ready != 1 || claimed != 1
	}, time.Second, 50*time.Millisecond, "the two items settled as one")
}

// idle is whether this schedule has no fire out with anybody, which is what
// "the previous run has ended" means to everything that reads it.
func idle(t *testing.T, h *harness, s Schedule) bool {
	t.Helper()

	q, err := h.crons.open(s)
	require.NoError(t, err)
	_, claimed := depthOf(t, q)
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

// workloadsOf is the workloads a test fires through: a listing and somewhere for the
// fires to land.
type workloadsOf struct {
	of    func(context.Context) []Schedule
	serve func(context.Context, Target, *invoke.Request) (*invoke.Response, error)
}

func (w workloadsOf) Workloads() (Workloads, bool)             { return w, true }
func (workloadsOf) Start(context.Context, service.Host) error  { return nil }
func (workloadsOf) Close() error                               { return nil }
func (w workloadsOf) Schedules(ctx context.Context) []Schedule { return w.of(ctx) }
func (w workloadsOf) Fire(ctx context.Context, at Target, req *invoke.Request) (*invoke.Response, error) {
	return w.serve(ctx, at, req)
}

// clockOf is a clock over those workloads, always beside the service.
func clockOf(of func(context.Context) []Schedule, serve func(context.Context, Target, *invoke.Request) (*invoke.Response, error)) option {
	return withWorkloads(workloadsOf{of: of, serve: serve})
}

// depthReader is a queue, as a test reads its depth.
type depthReader interface {
	Depth() (ready, claimed int, err error)
}

// depthOf is q's depth. A depth it cannot read fails t and reads as -1, -1, which
// no assertion takes for a depth; t.Errorf is safe from a condition Eventually polls.
func depthOf(t testing.TB, q depthReader) (ready, claimed int) {
	t.Helper()
	ready, claimed, err := q.Depth()
	if err != nil {
		t.Errorf("Depth: %v", err)
		return -1, -1
	}
	return ready, claimed
}
