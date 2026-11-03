package primitives

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/register"
)

// Every case runs the pass without touching the resource first: quiet resources are
// the ones that need it.

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

func TestTheDeadLetterIsBounded(t *testing.T) {
	h := starting(t, withDeadLetterRetention(24*time.Hour, 0))
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

func TestALiveItemIsNotExpiredWithTheDeadOnes(t *testing.T) {
	h := starting(t, withDeadLetterRetention(time.Hour, 0))
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

// refusingRegister is a register this node cannot read.
type refusingRegister struct{}

func (refusingRegister) Resolve(context.Context, string, string) (register.Pointer, error) {
	return register.Pointer{}, errors.New("the register is unreachable")
}

func (refusingRegister) Object(context.Context, register.ObjectID) ([]byte, error) {
	return nil, errors.New("the register is unreachable")
}

// A REGISTER THIS NODE CANNOT READ, or one that no longer declares the resource,
// truncates nothing: the first is a fault the next pass retries, the second reclaim's.
func TestRetentionTruncatesNothingItCannotResolve(t *testing.T) {
	h := starting(t)
	ctx := t.Context()
	traces := grantFor(tracesID)
	for _, line := range []string{"one", "two", "three"} {
		_, err := h.Local().Append(ctx, traces, []byte(line))
		require.NoError(t, err)
	}
	require.NoError(t, h.Local().Send(ctx, grantFor(jobsID), "job-1", []byte("work")))

	nothing, err := json.Marshal(map[string]any{"object": map[string]any{"id": projectID, "name": "shop"}})
	require.NoError(t, err)
	for _, reg := range []register.Registry{refusingRegister{}, &published{revision: 2, object: nothing}} {
		other := startingOn(t, h.cluster, withRegister(reg), withRecords(h.stores.open), withClock(h.clock.now))
		h.clock.advance(2 * time.Hour)
		other.cycle(ctx)
	}

	read, err := h.Local().Read(ctx, traces, "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"one", "two", "three"}, payloads(read))
	depth, err := h.Local().Depth(ctx, grantFor(jobsID))
	require.NoError(t, err)
	require.Equal(t, uint64(1), depth.Ready)
}

// A FOLLOWER'S TURN RETAINS NOTHING, a pass whose caller has gone stops, and a store
// that cannot answer is reported rather than read as a log inside its budget.
func TestRetentionRunsOnlyWhereItMayAndWhatItCanRead(t *testing.T) {
	ctx := t.Context()
	traces := grantFor(tracesID)
	appended := func(t *testing.T) *harness {
		h := starting(t)
		for _, line := range []string{"one", "two", "three"} {
			_, err := h.Local().Append(ctx, traces, []byte(line))
			require.NoError(t, err)
		}
		require.NoError(t, h.Local().Send(ctx, grantFor(jobsID), "job-1", []byte("work")))
		h.clock.advance(2 * time.Hour)
		return h
	}
	kept := func(t *testing.T, h *harness) {
		t.Helper()
		read, err := h.Local().Read(ctx, traces, "", 0)
		require.NoError(t, err)
		require.Equal(t, []string{"one", "two", "three"}, payloads(read))
	}

	follower := appended(t)
	follower.cluster.stepDown()
	follower.cycle(ctx)
	kept(t, follower)

	gone := appended(t)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	gone.retain(cancelled, gone.cluster)
	kept(t, gone)

	sick := appended(t)
	sick.stores.sicken(true)
	sick.cycle(ctx)
	sick.stores.sicken(false)
	kept(t, sick)
}
