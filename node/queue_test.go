package node

import (
	"errors"
	"testing"
	"time"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/raft"
)

func sendGrant(handle string, settings map[string]string) invoke.Grant {
	return invoke.Grant{
		Kind: "queue", Label: "jobs", Handle: handle,
		Ops: []string{"send"}, Settings: settings,
	}
}

func queueing(t *testing.T) *queues {
	t.Helper()
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}})
	return &queues{node: n}
}

// TestAnItemIsPushed, over the same replicated queue the build service runs
// its jobs through.
func TestSendPushesAnItem(t *testing.T) {
	q := queueing(t)
	require.NoError(t, q.Send(t.Context(), sendGrant("shop/prod/QmJobs", nil), "order:1", []byte("work")))

	ready, claimed, ok := q.Depth("shop/prod/QmJobs")
	require.True(t, ok)
	require.Equal(t, 1, ready)
	require.Zero(t, claimed)
}

// TestTheSAMEIdIsONEItem, which is what makes a producer's retry safe.
func TestPushingOneIdTwiceIsOneItem(t *testing.T) {
	q := queueing(t)
	ctx := t.Context()
	grant := sendGrant("shop/prod/QmJobs", nil)

	require.NoError(t, q.Send(ctx, grant, "order:1", []byte("a")))
	require.NoError(t, q.Send(ctx, grant, "order:1", []byte("b")))

	ready, _, _ := q.Depth("shop/prod/QmJobs")
	require.Equal(t, 1, ready, "a retried send is not a second delivery")
}

// TestTwoDeploymentsAreTwoQueues, and two projects using the name `jobs` never
// meet — the handle carries both.
func TestQueuesAreScopedByHandle(t *testing.T) {
	q := queueing(t)
	ctx := t.Context()

	require.NoError(t, q.Send(ctx, sendGrant("shop/prod/QmJobs", nil), "1", []byte("prod")))
	require.NoError(t, q.Send(ctx, sendGrant("shop/staging/QmJobs", nil), "1", []byte("staging")))
	require.NoError(t, q.Send(ctx, sendGrant("other/prod/QmJobs", nil), "1", []byte("other")))

	for _, handle := range []string{"shop/prod/QmJobs", "shop/staging/QmJobs", "other/prod/QmJobs"} {
		ready, _, ok := q.Depth(handle)
		require.True(t, ok, handle)
		require.Equal(t, 1, ready, handle)
	}
}

// TestOneRaftClusterForEveryQueue, because a cluster per resource would be a
// leader election and a log per declared queue.
func TestEveryQueueSharesOneCluster(t *testing.T) {
	q := queueing(t)
	ctx := t.Context()

	for _, handle := range []string{"shop/prod/QmA", "shop/prod/QmB", "other/prod/QmA"} {
		require.NoError(t, q.Send(ctx, sendGrant(handle, nil), "1", []byte("x")))
	}

	require.Len(t, q.open, 3, "three queues")
	require.NotNil(t, q.group, "one group")
}

// ── what the resource declared ───────────────────────────────────────────────

// TestTheDeliveryBlockReachesTheQueue, so a visibility timeout an author wrote
// is the one in force.
func TestTheDeclaredDeliveryIsUsed(t *testing.T) {
	q := queueing(t)

	grant := sendGrant("shop/prod/QmJobs", map[string]string{
		"visibility_timeout": "45s",
		"delay":              "2s",
		"max_attempts":       "9",
		"retry/interval":     "5s",
		"retry/factor":       "2",
		"retry/ceiling":      "1m0s",
		"dead_letter/name":   "QmDead",
	})
	require.NoError(t, q.Send(t.Context(), grant, "1", []byte("x")))

	cfg := q.open["shop/prod/QmJobs"].Config()
	require.Equal(t, 45*time.Second, cfg.VisibilityTimeout)
	require.Equal(t, 2*time.Second, cfg.Delay)
	require.Equal(t, uint32(9), cfg.MaxAttempts)
	require.Equal(t, 5*time.Second, cfg.RetryInterval)
	require.Equal(t, 2.0, cfg.RetryFactor)
	require.Equal(t, time.Minute, cfg.RetryCeiling)

	// THE DEAD LETTER IS SCOPED LIKE THE QUEUE IT DRAINS, so it lands beside it
	// rather than in some other deployment.
	require.Equal(t, "shop/prod/QmDead", cfg.DeadLetter)
}

// TestAnUndeclaredDeliveryTakesTheDefaults, which pkg/queue owns rather than
// this file inventing them.
func TestNothingDeclaredIsTheDefaults(t *testing.T) {
	q := queueing(t)
	require.NoError(t, q.Send(t.Context(), sendGrant("shop/prod/QmJobs", nil), "1", []byte("x")))

	cfg := q.open["shop/prod/QmJobs"].Config()
	require.Positive(t, cfg.VisibilityTimeout)
	require.Positive(t, cfg.MaxAttempts)
	require.Contains(t, cfg.DeadLetter, "shop/prod/QmJobs", "its own name plus the suffix")
}

// TestADelayedItemIsNotReadyYet, which is the vocabulary's own `delay`.
func TestADelayHoldsAnItemBack(t *testing.T) {
	q := queueing(t)
	grant := sendGrant("shop/prod/QmLater", map[string]string{"delay": "1h0m0s"})

	require.NoError(t, q.Send(t.Context(), grant, "1", []byte("x")))

	ready, claimed, _ := q.Depth("shop/prod/QmLater")
	require.Equal(t, 1, ready, "it is in the queue")
	require.Zero(t, claimed)

	// And nothing may claim it yet.
	_, err := q.open["shop/prod/QmLater"].Claim()
	require.Error(t, err, "an item under its delay is not deliverable")
}

// ── what it refuses ──────────────────────────────────────────────────────────

// TestAQueueNeedsConsensus, and says so rather than panicking on the first
// send.
func TestAQueueNeedsRaft(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	n, err := New(Config{Services: []string{Worker, Stats}},
		WithHost(h), WithPubSub(newPubSub(t, h)), WithDatabases(&memFactory{}),
		WithRuntimes(&tier{name: "wasm"}))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })

	err = (&queues{node: n}).Send(t.Context(), sendGrant("shop/prod/QmJobs", nil), "1", nil)
	require.ErrorIs(t, err, ErrNoRaft)
}

// TestDepthOfAQueueNobodyOpened is not an error: it is a question about
// something this node is not holding.
func TestDepthOfAnUnopenedQueue(t *testing.T) {
	q := queueing(t)
	_, _, ok := q.Depth("shop/prod/QmNever")
	require.False(t, ok)
}

// TestClosingIsIdempotent, because the pool may close a node twice and a queue
// holds no state of its own — everything is in the store, which is what lets a
// process die mid-claim and the claim expire.
func TestClosingTheQueuesTwiceIsFine(t *testing.T) {
	q := queueing(t)
	require.NoError(t, q.Send(t.Context(), sendGrant("shop/prod/QmJobs", nil), "1", nil))

	require.NoError(t, q.Close())
	require.NoError(t, q.Close())
}

// TestTheTiersNotWritableArmComesFromRaftsNotLeader, so a guest is told to
// retry rather than to give up.
func TestAFollowerAnswersNotWritable(t *testing.T) {
	// A mock cluster is always the leader, so this asserts the TRANSLATION
	// rather than the election: raft.ErrNotLeader becomes the tier's arm.
	require.ErrorIs(t, wrapNotLeader(raft.ErrNotLeader), wasmtier.ErrNotWritable)

	// And anything else is left alone: an unreachable store is not a retry.
	other := errors.New("the swarm is unreachable")
	require.Equal(t, other, wrapNotLeader(other))
	require.NoError(t, wrapNotLeader(nil))
}
