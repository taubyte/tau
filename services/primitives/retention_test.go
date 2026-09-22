package primitives

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// retention_test is the lifecycle: what a primitive stops keeping, and when.
//
// EVERY CASE HERE RUNS THE PASS WITHOUT TOUCHING THE RESOURCE FIRST, because
// that is the whole defect it fixes. node/log.go swept from Append and pkg/queue
// enforces from Claim, so both were correct for a resource somebody was using
// and did nothing at all for one that had gone quiet — which is exactly when an
// age budget starts to matter.

// TestRetentionEndsRecordsOnALogNobodyIsAppendingTo.
func TestRetentionEndsRecordsOnALogNobodyIsAppendingTo(t *testing.T) {
	h := starting(t)
	tier := h.Local()
	ctx := t.Context()
	traces := grantFor(tracesID)

	for _, line := range []string{"one", "two", "three"} {
		_, err := tier.Append(ctx, traces, []byte(line))
		require.NoError(t, err)
	}

	// `traces` keeps an hour. Nothing appends again, which under the old rule
	// meant nothing ever expired.
	h.clock.advance(2 * time.Hour)
	h.cycle(ctx)

	read, err := tier.Read(ctx, traces, "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"three"}, payloads(read),
		"a quiet log must still lose what its retention no longer keeps")
}

// TestTheNewestRecordSurvivesEveryBudget. A log whose ceiling cannot hold one
// record is a misconfiguration, and answering it by forgetting everything loses
// the write a caller was told succeeded.
func TestTheNewestRecordSurvivesEveryBudget(t *testing.T) {
	h := starting(t)
	tier := h.Local()
	ctx := t.Context()
	traces := grantFor(tracesID)

	_, err := tier.Append(ctx, traces, []byte("the only one"))
	require.NoError(t, err)

	h.clock.advance(100 * time.Hour)
	h.cycle(ctx)

	read, err := tier.Read(ctx, traces, "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"the only one"}, payloads(read))
}

// TestALogThatDeclaredNoRetentionKeepsEverything, which is the vocabulary's own
// sentence — absent means forever — and the one thing this service does not get
// to improve on.
func TestALogThatDeclaredNoRetentionKeepsEverything(t *testing.T) {
	h := starting(t)
	tier := h.Local()
	ctx := t.Context()
	audit := grantFor(auditID)

	for _, line := range []string{"one", "two", "three"} {
		_, err := tier.Append(ctx, audit, []byte(line))
		require.NoError(t, err)
	}

	h.clock.advance(10000 * time.Hour)
	h.cycle(ctx)

	read, err := tier.Read(ctx, audit, "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"one", "two", "three"}, payloads(read))
}

// TestRetentionRunsOnlyOnTheNodeThatMayWrite. Every transition below is a write,
// and a write commits only on the leader — so the pass needs no election of its
// own, and a follower running it changes nothing.
func TestRetentionRunsOnlyOnTheNodeThatMayWrite(t *testing.T) {
	h := starting(t)
	tier := h.Local()
	ctx := t.Context()
	traces := grantFor(tracesID)

	for _, line := range []string{"one", "two", "three"} {
		_, err := tier.Append(ctx, traces, []byte(line))
		require.NoError(t, err)
	}

	h.clock.advance(2 * time.Hour)
	h.cluster.stepDown()
	h.cycle(ctx)

	read, err := tier.Read(ctx, traces, "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"one", "two", "three"}, payloads(read),
		"a follower must not have truncated a log the leader still owns")
}

// TestAnItemThatGivesUpReachesTheDeadLetterWithNobodyClaiming.
//
// pkg/queue buries an exhausted item at the top of Claim, so a queue whose
// consumers have stopped is exactly the queue whose failures never get there.
func TestAnItemThatGivesUpReachesTheDeadLetterWithNobodyClaiming(t *testing.T) {
	h := starting(t)
	tier := h.Local()
	ctx := t.Context()
	fragile := grantFor(deadID)

	require.NoError(t, tier.Send(ctx, fragile, "job-1", []byte("doomed")))
	claimed, err := tier.Claim(ctx, fragile, 1)
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	// The consumer dies holding it. `fragile` allows one attempt, so once the
	// claim expires the item has nowhere left to go but the dead letter — and
	// nothing claims again.
	h.clock.advance(time.Minute)
	h.cycle(ctx)

	depth, err := tier.Depth(ctx, fragile)
	require.NoError(t, err)
	require.EqualValues(t, 0, depth.Ready+depth.Claimed)
	require.EqualValues(t, 1, depth.Dead, "an item that gave up must be findable")
}

// TestTheDeadLetterIsBounded, which is the half that did not exist at all: a
// buried item was in a keyspace no declaration names, so no consumer could ever
// be declared for it, and it sat in every voter's heap forever.
func TestTheDeadLetterIsBounded(t *testing.T) {
	h := starting(t, WithDeadLetterRetention(24*time.Hour, 0))
	tier := h.Local()
	ctx := t.Context()
	fragile := grantFor(deadID)

	require.NoError(t, tier.Send(ctx, fragile, "job-1", []byte("doomed")))
	_, err := tier.Claim(ctx, fragile, 1)
	require.NoError(t, err)

	h.clock.advance(time.Minute)
	h.cycle(ctx)

	depth, err := tier.Depth(ctx, fragile)
	require.NoError(t, err)
	require.EqualValues(t, 1, depth.Dead)

	// A DAY LATER NOTHING HAS DRAINED IT, because nothing ever can.
	h.clock.advance(25 * time.Hour)
	h.cycle(ctx)

	depth, err = tier.Depth(ctx, fragile)
	require.NoError(t, err)
	require.EqualValues(t, 0, depth.Dead, "a dead letter nobody can drain must still end")
}

// TestALiveItemIsNotExpiredWithTheDeadOnes. Expiry walks the dead letter and
// nothing else: the queue's own items are the workload's, however old.
func TestALiveItemIsNotExpiredWithTheDeadOnes(t *testing.T) {
	h := starting(t, WithDeadLetterRetention(time.Hour, 0))
	tier := h.Local()
	ctx := t.Context()
	jobs := grantFor(jobsID)

	require.NoError(t, tier.Send(ctx, jobs, "job-1", []byte("slow")))

	h.clock.advance(100 * time.Hour)
	h.cycle(ctx)

	depth, err := tier.Depth(ctx, jobs)
	require.NoError(t, err)
	require.EqualValues(t, 1, depth.Ready, "retention on the dead letter must not touch live work")
}

// TestARefusedDropLeavesTheLogReadable.
//
// Retention writes the largest entry this service ever asks raft for — every
// position it is dropping, in one batch — so it is the likeliest write to be
// refused, by a lost election or by out-running applyTimeout. What must not
// happen then is what deleting the bytes first would do: every SURVIVING record
// sitting behind a position whose bytes are gone, a read stopping at the first
// of them, and the log answering empty on every node for as long as it exists.
func TestARefusedDropLeavesTheLogReadable(t *testing.T) {
	cluster := &brittle{fakeCluster: newCluster()}
	h := startingOn(t, cluster)
	tier := h.Local()
	ctx := t.Context()
	traces := grantFor(tracesID)

	for _, line := range []string{"one", "two", "three"} {
		_, err := tier.Append(ctx, traces, []byte(line))
		require.NoError(t, err)
	}
	h.clock.advance(2 * time.Hour)
	_, err := tier.Append(ctx, traces, []byte("four"))
	require.NoError(t, err)

	// The pass decides to end the first three and loses the entry that would
	// have retired their positions.
	cluster.refuseWrites(true)
	h.cycle(ctx)
	cluster.refuseWrites(false)

	read, err := tier.Read(ctx, traces, "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"one", "two", "three", "four"}, payloads(read),
		"a retention that did not commit must not have ended anything")

	// AND THE NEXT PASS IS THE WHOLE OF THE RECOVERY. The service runs the same
	// pass over the same replicated state, which is what leader-only buys it.
	h.cycle(ctx)

	read, err = tier.Read(ctx, traces, "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"four"}, payloads(read))
}

// TestEveryPlaceTheLifecyclePassWalksResolvesBackToItsDeclaration.
//
// The pass reads PLACES out of the keyspace and turns each one back into the
// declaration that says what to keep. A place that does not resolve is not an
// error anybody ever sees: retainLog and retainQueue return at their first arm,
// so that log keeps everything, that queue's expired claims are never swept and
// its dead letter grows with nothing said — the exact failure this service
// exists to end, reintroduced for whatever cannot be looked up.
//
// IT IS A GUARD AND NOT A REPRODUCTION, deliberately. An instance is refused at
// the door now, so no call can write a place the pass cannot resolve and there
// is nothing left to reproduce; what this holds is the invariant the next thing
// that folds something into a name would break.
func TestEveryPlaceTheLifecyclePassWalksResolvesBackToItsDeclaration(t *testing.T) {
	h := starting(t)
	tier := h.Local()
	ctx := t.Context()

	_, err := tier.Append(ctx, grantFor(tracesID), []byte("a record"))
	require.NoError(t, err)
	require.NoError(t, tier.Send(ctx, grantFor(jobsID), "job-1", []byte("work")))

	cluster, err := h.consensus()
	require.NoError(t, err)

	places := append(logsIn(cluster), queuesIn(cluster)...)
	require.Len(t, places, 2, "the pass must see the log and the queue it was just given")
	for _, spec := range places {
		_, err := h.declared(ctx, spec.at(), spec.Type)
		require.NoError(t, err, "%s is a place the pass walks and cannot resolve", spec.Prefix())
	}
}
