package stream_test

import (
	"errors"
	"testing"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	gateway "github.com/taubyte/tau/services/gateway"
	"github.com/taubyte/tau/services/worker/stream"
)

// A real gossipsub over one mocknet peer: a *pubsub.Topic is concrete, and a double
// would make "joined once" assert against itself.

// gossiping is a router over one peer. Publishing delivers to this peer's own
// subscriptions, which is the whole of what an attachment needs.
func gossiping(t *testing.T) *pubsub.PubSub {
	t.Helper()

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	h, err := mn.GenPeer()
	require.NoError(t, err)

	ps, err := pubsub.NewGossipSub(t.Context(), h)
	require.NoError(t, err)
	return ps
}

// joiner is a Gossip double over a real router: it records every join, and can
// fail one or hand back a topic that is already closed.
type joiner struct {
	ps     *pubsub.PubSub
	joined []string

	err error

	// closed hands back a topic nothing can be done with, which is what a
	// subscription racing the node's own shutdown finds.
	closed bool
}

func (j *joiner) Join(topic string, opts ...pubsub.TopicOpt) (*pubsub.Topic, error) {
	j.joined = append(j.joined, topic)
	if j.err != nil {
		return nil, j.err
	}
	joined, err := j.ps.Join(topic, opts...)
	if err != nil {
		return nil, err
	}
	if j.closed {
		joined.Close()
	}
	return joined, nil
}

// publishing is a bus backing over a fresh router.
func publishing(t *testing.T) (*stream.Bus, *joiner) {
	t.Helper()

	gossip := &joiner{ps: gossiping(t)}
	s := stream.NewBus(gossip, "cloud")
	t.Cleanup(func() { s.Close() })
	return s, gossip
}

// on is the grant a guest publishes under; the bus reads only its handle.
func on(handle string) invoke.Grant { return invoke.Grant{Kind: "bus", Handle: handle} }

func TestNoCloudIsAllowed(t *testing.T) {
	gossip := &joiner{ps: gossiping(t)}

	bare := stream.NewBus(gossip, "")
	t.Cleanup(func() { bare.Close() })

	require.NoError(t, bare.Publish(t.Context(), on("shop/prod/QmOrders"), []byte("x")))
	require.Equal(t, []string{"/tau/bus//shop/prod/QmOrders"}, gossip.joined)
}

func TestTheTopicCarriesTheProjectAndDeployment(t *testing.T) {
	mine := stream.Topic("cloud", "shop/prod/QmOrders")
	theirs := stream.Topic("cloud", "other/prod/QmOrders")
	staging := stream.Topic("cloud", "shop/staging/QmOrders")

	require.NotEqual(t, mine, theirs, "two projects")
	require.NotEqual(t, mine, staging, "two deployments of one project")
	require.Equal(t, "/tau/bus/cloud/shop/prod/QmOrders", mine)
}

func TestTheTopicCarriesTheCloud(t *testing.T) {
	require.NotEqual(t,
		stream.Topic("one", "shop/prod/QmOrders"),
		stream.Topic("two", "shop/prod/QmOrders"))
}

func TestTheTopicIsNotTheLabel(t *testing.T) {
	require.NotContains(t, stream.Topic("cloud", "shop/prod/QmOrders"), "orders")
}

func TestATopicIsJoinedOncePerHandle(t *testing.T) {
	s, gossip := publishing(t)
	ctx := t.Context()

	require.NoError(t, s.Publish(ctx, on("shop/prod/QmOrders"), []byte("one")))
	require.NoError(t, s.Publish(ctx, on("shop/prod/QmOrders"), []byte("two")))
	require.Equal(t, []string{"/tau/bus/cloud/shop/prod/QmOrders"}, gossip.joined)

	require.NoError(t, s.Publish(ctx, on("shop/prod/QmOther"), []byte("three")))
	require.Len(t, gossip.joined, 2, "a second handle is a second topic")
}

func TestPublishingNeedsPubSub(t *testing.T) {
	bare := stream.NewBus(nil, "cloud")

	err := bare.Publish(t.Context(), on("shop/prod/QmOrders"), []byte("x"))
	require.ErrorIs(t, err, stream.ErrNoPubSub)
}

func TestAJoinThatFailsCarriesTheHandle(t *testing.T) {
	s, gossip := publishing(t)
	gossip.err = errors.New("the router is shutting down")

	err := s.Publish(t.Context(), on("shop/prod/QmOrders"), []byte("x"))
	require.ErrorContains(t, err, "shutting down")
	require.ErrorContains(t, err, "shop/prod/QmOrders")
}

func TestClosingReleasesTheTopics(t *testing.T) {
	s, gossip := publishing(t)

	require.NoError(t, s.Publish(t.Context(), on("shop/prod/QmOrders"), []byte("x")))
	require.NoError(t, s.Close())
	require.NoError(t, s.Close(), "Close is idempotent")

	require.NoError(t, s.Publish(t.Context(), on("shop/prod/QmOrders"), []byte("x")))
	require.Len(t, gossip.joined, 2)
}

func attaching(t *testing.T) (*stream.Bus, *stream.Buses, *joiner) {
	t.Helper()

	backing, gossip := publishing(t)
	buses, err := stream.NewBuses(backing)
	require.NoError(t, err)
	return backing, buses, gossip
}

func TestABusNeedsABackingToBeAViewOf(t *testing.T) {
	_, err := stream.NewBuses(nil)
	require.ErrorContains(t, err, "no backing")
}

func TestAnAttachmentGetsWhatTheTierPublishes(t *testing.T) {
	tier, buses, _ := attaching(t)
	ctx := t.Context()

	at, err := buses.Attach(ctx, "shop/prod/QmChat")
	require.NoError(t, err)
	t.Cleanup(func() { at.Close() })

	require.NoError(t, tier.Publish(ctx, on("shop/prod/QmChat"), []byte("hello")))

	message, err := at.Next(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", string(message))
}

func TestASecondAttachmentGetsItToo(t *testing.T) {
	_, buses, _ := attaching(t)
	ctx := t.Context()

	first, err := buses.Attach(ctx, "shop/prod/QmChat")
	require.NoError(t, err)
	t.Cleanup(func() { first.Close() })

	second, err := buses.Attach(ctx, "shop/prod/QmChat")
	require.NoError(t, err)
	t.Cleanup(func() { second.Close() })

	// The FIRST one publishes, so this also pins that a sender is not excluded.
	require.NoError(t, first.Publish(ctx, []byte("i am here")))

	for i, at := range []gateway.Attachment{first, second} {
		message, err := at.Next(ctx)
		require.NoError(t, err, "attachment %d", i)
		require.Equal(t, "i am here", string(message), "attachment %d", i)
	}
}

func TestAttachingDoesNotRejoinTheTopic(t *testing.T) {
	tier, buses, gossip := attaching(t)
	ctx := t.Context()

	for range 3 {
		at, err := buses.Attach(ctx, "shop/prod/QmChat")
		require.NoError(t, err)
		t.Cleanup(func() { at.Close() })
	}
	require.NoError(t, tier.Publish(ctx, on("shop/prod/QmChat"), []byte("x")))

	require.Len(t, gossip.joined, 1, "three attachments and a publish, one joined topic")
}

func TestClosingOneAttachmentLeavesTheOthers(t *testing.T) {
	tier, buses, gossip := attaching(t)
	ctx := t.Context()

	leaving, err := buses.Attach(ctx, "shop/prod/QmChat")
	require.NoError(t, err)
	staying, err := buses.Attach(ctx, "shop/prod/QmChat")
	require.NoError(t, err)
	t.Cleanup(func() { staying.Close() })

	require.NoError(t, leaving.Close())
	require.NoError(t, tier.Publish(ctx, on("shop/prod/QmChat"), []byte("still here")))

	message, err := staying.Next(ctx)
	require.NoError(t, err)
	require.Equal(t, "still here", string(message))
	require.Len(t, gossip.joined, 1, "and the topic was not left")
}

func TestAttachingNeedsPubSub(t *testing.T) {
	bare := stream.NewBus(nil, "cloud")

	buses, err := stream.NewBuses(bare)
	require.NoError(t, err)

	_, err = buses.Attach(t.Context(), "shop/prod/QmChat")
	require.ErrorIs(t, err, stream.ErrNoPubSub)
}

func TestASubscriptionThatFailsIsReported(t *testing.T) {
	gossip := &joiner{ps: gossiping(t), closed: true}
	backing := stream.NewBus(gossip, "cloud")

	buses, err := stream.NewBuses(backing)
	require.NoError(t, err)

	_, err = buses.Attach(t.Context(), "shop/prod/QmChat")
	require.ErrorContains(t, err, "subscribing")
	require.ErrorContains(t, err, "shop/prod/QmChat")
}
