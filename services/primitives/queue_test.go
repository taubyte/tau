package primitives

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
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

// A QUEUE NEVER GROWS WITHOUT BOUND: a send past its declared size is refused, on
// the tier and the wire alike, and work nobody claims within its age moves to the
// dead letter on the lifecycle pass.
func TestAQueueIsBoundedBySizeAndAge(t *testing.T) {
	ctx := t.Context()
	h := starting(t)
	tier := h.Local()
	bounded := grantFor(boundedID)

	require.NoError(t, tier.Send(ctx, bounded, "1", []byte("12345")))
	err := tier.Send(ctx, bounded, "2", []byte("12345"))
	require.ErrorIs(t, err, ErrQueueFull)
	require.ErrorContains(t, err, "declared size")
	depth, err := tier.Depth(ctx, bounded)
	require.NoError(t, err)
	require.Equal(t, uint64(1), depth.Ready, "a refused send adds nothing")

	client, _ := wiredWith(t)
	require.NoError(t, client.Send(ctx, bounded, "1", []byte("12345")))
	err = client.Send(ctx, bounded, "2", []byte("12345"))
	require.Error(t, err)
	require.ErrorContains(t, err, "declared size", "the wire carries the reason")

	// PAST ITS AGE, unclaimed, it is dead-lettered rather than forgotten.
	h.clock.advance(2 * time.Hour)
	h.cycle(ctx)
	depth, err = tier.Depth(ctx, bounded)
	require.NoError(t, err)
	require.Zero(t, depth.Ready)
	require.Equal(t, uint64(1), depth.Dead)

	// And the room it held is free again.
	require.NoError(t, tier.Send(ctx, bounded, "3", []byte("12345678")))
}

// A WRITE OFF THE LEADER IS MADE ON THE NODE ITS REFUSAL NAMES, once: a guest on any
// node holding the namespace sends, appends, claims and settles through the node it
// found.
func TestAWriteOffTheLeaderIsMadeOnTheNodeItNames(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	writer := startingIn(t, mn, newCluster())
	follows := newCluster()
	follower := startingIn(t, mn, follows)
	follows.follow(writer.host.ID())
	require.NoError(t, mn.LinkAll())
	require.NoError(t, mn.ConnectAllButSelf())
	ctx := t.Context()
	jobs, audit := grantFor(jobsID), grantFor(auditID)

	require.NoError(t, follower.Local().Send(ctx, jobs, "job-1", []byte("resize")))
	depth, err := writer.Local().Depth(ctx, jobs)
	require.NoError(t, err)
	require.EqualValues(t, 1, depth.Ready, "the send landed on the writer")

	claimed, err := follower.Local().Claim(ctx, jobs, 1)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.NoError(t, follower.Local().Settle(ctx, jobs, claimed[0], true))
	depth, err = writer.Local().Depth(ctx, jobs)
	require.NoError(t, err)
	require.EqualValues(t, 0, depth.Ready+depth.Claimed, "the claim and its settle were the writer's")

	cursor, err := follower.Local().Append(ctx, audit, []byte("crosses"))
	require.NoError(t, err)
	require.NotEmpty(t, cursor)
	read, err := writer.Local().Read(ctx, audit, "", 10)
	require.NoError(t, err)
	require.Len(t, read, 1)
	require.Equal(t, cursor, read[0].Cursor)

	// OVER THE WIRE TOO, the follower sending it on once more.
	client, err := NewClient(writer.host, follower.host.ID())
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	require.NoError(t, client.Send(ctx, jobs, "job-2", []byte("crop")))
	depth, err = writer.Local().Depth(ctx, jobs)
	require.NoError(t, err)
	require.EqualValues(t, 1, depth.Ready)

	// AND A WRITE THAT HAS COME AS FAR AS IT MAY is refused where it lands.
	far := hopsIn(ctx, http.Header{hopHeader: []string{strconv.Itoa(maxHops)}})
	err = follower.Local().Send(far, jobs, "job-3", []byte("crop"))
	require.ErrorIs(t, err, wasmtier.ErrNotWritable)
	var named NotLeading
	require.ErrorAs(t, err, &named)
	require.Equal(t, writer.host.ID().String(), named.Leader)
}

// A LEADER THAT CANNOT BE REACHED LEAVES THE REFUSAL AS IT WAS, naming who may write,
// so the caller retries rather than reading a broken cloud.
func TestAWriteWhoseLeaderCannotBeReachedKeepsTheRefusal(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	absent, err := mocknet.New().GenPeer()
	require.NoError(t, err)
	follows := newCluster()
	follower := startingIn(t, mn, follows)
	follows.follow(absent.ID())

	err = follower.Local().Send(t.Context(), grantFor(jobsID), "job-1", []byte("resize"))
	require.ErrorIs(t, err, wasmtier.ErrNotWritable)
	var named NotLeading
	require.ErrorAs(t, err, &named)
	require.Equal(t, absent.ID().String(), named.Leader)
}
