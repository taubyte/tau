package stream_test

import (
	"context"
	"errors"
	"testing"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node/stream"
	gateway "github.com/taubyte/tau/services/gateway"
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

// publishing is a stream backing over a fresh router.
func publishing(t *testing.T, kind string) (*stream.Streams, *joiner) {
	t.Helper()

	gossip := &joiner{ps: gossiping(t)}
	s, err := stream.New(gossip, kind, "cloud")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s, gossip
}

func TestTheAdapterNeedsToKnowWhichKindItServes(t *testing.T) {
	gossip := &joiner{ps: gossiping(t)}

	for _, kind := range []string{"", "queue", "streams"} {
		_, err := stream.New(gossip, kind, "cloud")
		require.ErrorContains(t, err, "not a kind", "kind %q", kind)
	}

	for _, kind := range []string{stream.Stream, stream.Bus} {
		s, err := stream.New(gossip, kind, "cloud")
		require.NoError(t, err)
		require.NotNil(t, s)
	}
}

func TestNoCloudIsAllowed(t *testing.T) {
	gossip := &joiner{ps: gossiping(t)}

	bare, err := stream.New(gossip, stream.Stream, "")
	require.NoError(t, err)
	t.Cleanup(func() { bare.Close() })

	require.NoError(t, bare.Publish(t.Context(), "shop/prod/QmOrders", []byte("x")))
	require.Equal(t, []string{"/tau/stream//shop/prod/QmOrders"}, gossip.joined)
}

func TestTheTopicCarriesTheProjectAndDeployment(t *testing.T) {
	mine := stream.Topic(stream.Stream, "cloud", "shop/prod/QmOrders")
	theirs := stream.Topic(stream.Stream, "cloud", "other/prod/QmOrders")
	staging := stream.Topic(stream.Stream, "cloud", "shop/staging/QmOrders")

	require.NotEqual(t, mine, theirs, "two projects")
	require.NotEqual(t, mine, staging, "two deployments of one project")
	require.Equal(t, "/tau/stream/cloud/shop/prod/QmOrders", mine)
}

func TestTheTopicCarriesTheCloud(t *testing.T) {
	require.NotEqual(t,
		stream.Topic(stream.Stream, "one", "shop/prod/QmOrders"),
		stream.Topic(stream.Stream, "two", "shop/prod/QmOrders"))
}

func TestTheTopicIsNotTheLabel(t *testing.T) {
	require.NotContains(t, stream.Topic(stream.Stream, "cloud", "shop/prod/QmOrders"), "orders")
}

func TestTheKindIsInTheTopic(t *testing.T) {
	require.NotEqual(t,
		stream.Topic(stream.Stream, "cloud", "shop/prod/QmX"),
		stream.Topic(stream.Bus, "cloud", "shop/prod/QmX"))
}

func TestATopicIsJoinedOncePerHandle(t *testing.T) {
	s, gossip := publishing(t, stream.Stream)
	ctx := t.Context()

	require.NoError(t, s.Publish(ctx, "shop/prod/QmOrders", []byte("one")))
	require.NoError(t, s.Publish(ctx, "shop/prod/QmOrders", []byte("two")))
	require.Equal(t, []string{"/tau/stream/cloud/shop/prod/QmOrders"}, gossip.joined)

	require.NoError(t, s.Publish(ctx, "shop/prod/QmOther", []byte("three")))
	require.Len(t, gossip.joined, 2, "a second handle is a second topic")
}

func TestAStreamAndABusJoinDifferentTopicsForOneHandle(t *testing.T) {
	gossip := &joiner{ps: gossiping(t)}

	publisher, err := stream.New(gossip, stream.Stream, "cloud")
	require.NoError(t, err)
	t.Cleanup(func() { publisher.Close() })

	bus, err := stream.New(gossip, stream.Bus, "cloud")
	require.NoError(t, err)
	t.Cleanup(func() { bus.Close() })

	require.NoError(t, publisher.Publish(t.Context(), "shop/prod/QmX", []byte("a")))
	require.NoError(t, bus.Publish(t.Context(), "shop/prod/QmX", []byte("b")))

	require.Equal(t, []string{
		"/tau/stream/cloud/shop/prod/QmX",
		"/tau/bus/cloud/shop/prod/QmX",
	}, gossip.joined, "one handle, two topics")
}

func TestPublishingNeedsPubSub(t *testing.T) {
	bare, err := stream.New(nil, stream.Stream, "cloud")
	require.NoError(t, err, "a node with no gossip still composes")

	err = bare.Publish(t.Context(), "shop/prod/QmOrders", []byte("x"))
	require.ErrorIs(t, err, stream.ErrNoPubSub)
}

func TestAJoinThatFailsCarriesTheHandle(t *testing.T) {
	s, gossip := publishing(t, stream.Stream)
	gossip.err = errors.New("the router is shutting down")

	err := s.Publish(t.Context(), "shop/prod/QmOrders", []byte("x"))
	require.ErrorContains(t, err, "shutting down")
	require.ErrorContains(t, err, "shop/prod/QmOrders")
}

func TestClosingReleasesTheTopics(t *testing.T) {
	s, gossip := publishing(t, stream.Stream)

	require.NoError(t, s.Publish(t.Context(), "shop/prod/QmOrders", []byte("x")))
	require.NoError(t, s.Close())
	require.NoError(t, s.Close(), "Close is idempotent")

	require.NoError(t, s.Publish(t.Context(), "shop/prod/QmOrders", []byte("x")))
	require.Len(t, gossip.joined, 2)
}

func attaching(t *testing.T) (*stream.Streams, *stream.Buses, *joiner) {
	t.Helper()

	backing, gossip := publishing(t, stream.Bus)
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

	require.NoError(t, tier.Publish(ctx, "shop/prod/QmChat", []byte("hello")))

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
	require.NoError(t, tier.Publish(ctx, "shop/prod/QmChat", []byte("x")))

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
	require.NoError(t, tier.Publish(ctx, "shop/prod/QmChat", []byte("still here")))

	message, err := staying.Next(ctx)
	require.NoError(t, err)
	require.Equal(t, "still here", string(message))
	require.Len(t, gossip.joined, 1, "and the topic was not left")
}

func TestAnAttachmentDoesNotSeeAStream(t *testing.T) {
	_, buses, gossip := attaching(t)
	ctx := t.Context()

	at, err := buses.Attach(ctx, "shop/prod/QmX")
	require.NoError(t, err)
	t.Cleanup(func() { at.Close() })

	// The same router, the same handle, the other kind.
	published, err := stream.New(gossip, stream.Stream, "cloud")
	require.NoError(t, err)
	t.Cleanup(func() { published.Close() })
	require.NoError(t, published.Publish(ctx, "shop/prod/QmX", []byte("on the stream")))

	// Nothing arrives, so the assertion is a deadline that has to expire.
	short, stop := context.WithTimeout(ctx, 250*time.Millisecond)
	defer stop()
	_, err = at.Next(short)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestAttachingNeedsPubSub(t *testing.T) {
	bare, err := stream.New(nil, stream.Bus, "cloud")
	require.NoError(t, err)

	buses, err := stream.NewBuses(bare)
	require.NoError(t, err)

	_, err = buses.Attach(t.Context(), "shop/prod/QmChat")
	require.ErrorIs(t, err, stream.ErrNoPubSub)
}

func TestASubscriptionThatFailsIsReported(t *testing.T) {
	gossip := &joiner{ps: gossiping(t), closed: true}
	backing, err := stream.New(gossip, stream.Bus, "cloud")
	require.NoError(t, err)

	buses, err := stream.NewBuses(backing)
	require.NoError(t, err)

	_, err = buses.Attach(t.Context(), "shop/prod/QmChat")
	require.ErrorContains(t, err, "subscribing")
	require.ErrorContains(t, err, "shop/prod/QmChat")
}
