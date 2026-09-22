package primitives

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
)

// TestAQueueDeliversAnItemToExactlyOneConsumer, which is the whole of what a
// queue promises and the one thing a stream deliberately does not: a claim is
// exclusive, so a second consumer asking while the first holds the item gets
// nothing rather than a copy of the work.
func TestAQueueDeliversAnItemToExactlyOneConsumer(t *testing.T) {
	tier := starting(t).Local()
	ctx := t.Context()
	jobs := grantFor(jobsID)

	require.NoError(t, tier.Send(ctx, jobs, "job-1", []byte("resize")))

	first, err := tier.Claim(ctx, jobs, 10)
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.Equal(t, "job-1", first[0].ID)
	require.Equal(t, []byte("resize"), first[0].Data)
	require.EqualValues(t, 1, first[0].Attempt, "a first delivery counts from one")
	require.NotEmpty(t, first[0].Token, "a delivery with no token could not be settled")

	second, err := tier.Claim(ctx, jobs, 10)
	require.NoError(t, err)
	require.Empty(t, second, "a claimed item must be invisible to everybody else")

	// NOTHING DELIVERABLE IS AN EMPTY ANSWER AND NOT A REFUSAL, because a
	// consumer acts on it by asking again.
	depth, err := tier.Depth(ctx, jobs)
	require.NoError(t, err)
	require.EqualValues(t, 0, depth.Ready)
	require.EqualValues(t, 1, depth.Claimed)

	require.NoError(t, tier.Settle(ctx, jobs, first[0], true))

	depth, err = tier.Depth(ctx, jobs)
	require.NoError(t, err)
	require.EqualValues(t, 0, depth.Ready+depth.Claimed+depth.Dead,
		"an acknowledged item is gone, not moved")
}

// TestARepeatedSendIsNotASecondCopyOfTheWork. The id is the caller's, which is
// what lets a producer that cannot tell whether its last send landed repeat it.
func TestARepeatedSendIsNotASecondCopyOfTheWork(t *testing.T) {
	tier := starting(t).Local()
	ctx := t.Context()
	jobs := grantFor(jobsID)

	require.NoError(t, tier.Send(ctx, jobs, "job-1", []byte("resize")))
	require.NoError(t, tier.Send(ctx, jobs, "job-1", []byte("resize")))

	depth, err := tier.Depth(ctx, jobs)
	require.NoError(t, err)
	require.EqualValues(t, 1, depth.Ready)
}

// TestASendWithNoIdIsRefused. A queue cannot invent an identity: without one,
// two sends of the same work are two items and nothing can say otherwise.
func TestASendWithNoIdIsRefused(t *testing.T) {
	tier := starting(t).Local()

	require.ErrorIs(t, tier.Send(t.Context(), grantFor(jobsID), "  ", []byte("work")), ErrNoItemID)
}

// TestAnItemLargerThanTheCeilingIsRefused, which node/queue.go never did: a
// guest's bytes went straight to Push, where they live INLINE in a state machine
// every voter holds in memory.
func TestAnItemLargerThanTheCeilingIsRefused(t *testing.T) {
	tier := starting(t).Local()
	ctx := t.Context()

	err := tier.Send(ctx, grantFor(jobsID), "big", make([]byte, MaxItem+1))
	require.ErrorIs(t, err, wasmtier.ErrRecordTooLarge)

	depth, err := tier.Depth(ctx, grantFor(jobsID))
	require.NoError(t, err)
	require.EqualValues(t, 0, depth.Ready, "a refused send must leave nothing behind")
}

// TestASendOffTheLeaderIsRefusedAndNamesWhoMayWrite. pkg/queue refuses a write
// off the leader and this service does not forward it — so the caller is told to
// retry, and told where.
func TestASendOffTheLeaderIsRefusedAndNamesWhoMayWrite(t *testing.T) {
	h := starting(t)
	ctx := t.Context()

	h.cluster.stepDown()

	err := h.Local().Send(ctx, grantFor(jobsID), "job-1", []byte("resize"))
	require.ErrorIs(t, err, wasmtier.ErrNotWritable)

	var elsewhere NotLeading
	require.ErrorAs(t, err, &elsewhere)
	require.NotEmpty(t, elsewhere.Leader)
}

// TestAClaimOffTheLeaderConsumesNothing. A follower that handed an item out
// would be a second claimer of work the leader still believes is ready.
func TestAClaimOffTheLeaderConsumesNothing(t *testing.T) {
	h := starting(t)
	ctx := t.Context()
	jobs := grantFor(jobsID)

	require.NoError(t, h.Local().Send(ctx, jobs, "job-1", []byte("resize")))
	h.cluster.stepDown()

	_, err := h.Local().Claim(ctx, jobs, 1)
	require.ErrorIs(t, err, wasmtier.ErrNotWritable)
}

// TestASettleWhoseClaimIsGoneIsRefusedAndNotAppliedSilently. The token is the
// exclusion: once the item has been handed to somebody else, the previous
// holder's work was NOT applied and must not be treated as if it were.
func TestASettleWhoseClaimIsGoneIsRefusedAndNotAppliedSilently(t *testing.T) {
	h := starting(t)
	tier := h.Local()
	ctx := t.Context()
	jobs := grantFor(jobsID)

	require.NoError(t, tier.Send(ctx, jobs, "job-1", []byte("resize")))
	first, err := tier.Claim(ctx, jobs, 1)
	require.NoError(t, err)

	// The first consumer stalls past its visibility timeout and somebody else
	// picks the item up.
	h.clock.advance(2 * time.Minute)
	second, err := tier.Claim(ctx, jobs, 1)
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.NotEqual(t, first[0].Token, second[0].Token, "a redelivery is a new delivery")

	require.ErrorIs(t, tier.Settle(ctx, jobs, first[0], true), ErrStaleDelivery)

	depth, err := tier.Depth(ctx, jobs)
	require.NoError(t, err)
	require.EqualValues(t, 1, depth.Claimed, "the stale ack must not have discarded live work")

	require.NoError(t, tier.Settle(ctx, jobs, second[0], true))
}

// TestTwoQueuesDoNotSeeEachOther. Every queue's keys hang off Spec.Prefix, so
// two queues in one project share a namespace and never an item.
func TestTwoQueuesDoNotSeeEachOther(t *testing.T) {
	tier := starting(t).Local()
	ctx := t.Context()

	require.NoError(t, tier.Send(ctx, grantFor(jobsID), "job-1", []byte("resize")))

	depth, err := tier.Depth(ctx, grantFor(deadID))
	require.NoError(t, err)
	require.EqualValues(t, 0, depth.Ready, "another queue's item is not this queue's")

	claimed, err := tier.Claim(ctx, grantFor(deadID), 10)
	require.NoError(t, err)
	require.Empty(t, claimed)
}
