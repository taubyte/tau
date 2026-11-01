package primitives

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
)

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

func TestASendWithNoIdIsRefused(t *testing.T) {
	tier := starting(t).Local()

	require.ErrorIs(t, tier.Send(t.Context(), grantFor(jobsID), "  ", []byte("work")), ErrNoItemID)
}

func TestAnItemLargerThanTheCeilingIsRefused(t *testing.T) {
	tier := starting(t).Local()
	ctx := t.Context()

	err := tier.Send(ctx, grantFor(jobsID), "big", make([]byte, MaxItem+1))
	require.ErrorIs(t, err, wasmtier.ErrRecordTooLarge)

	depth, err := tier.Depth(ctx, grantFor(jobsID))
	require.NoError(t, err)
	require.EqualValues(t, 0, depth.Ready, "a refused send must leave nothing behind")
}

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

func TestAClaimOffTheLeaderConsumesNothing(t *testing.T) {
	h := starting(t)
	ctx := t.Context()
	jobs := grantFor(jobsID)

	require.NoError(t, h.Local().Send(ctx, jobs, "job-1", []byte("resize")))
	h.cluster.stepDown()

	_, err := h.Local().Claim(ctx, jobs, 1)
	require.ErrorIs(t, err, wasmtier.ErrNotWritable)
}

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
