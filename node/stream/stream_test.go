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

// stream_test is the reason this package exists.
//
// Every one of these ran a whole node when the adapter took *Node — a register,
// a worker, a kvdb factory and a component tier — to assert which topic name a
// handle goes on the wire as. One interface later what is left is a gossipsub
// over one mocknet peer, which is the only part that cannot be faked: a
// *pubsub.Topic is a concrete type nobody but a real router hands out, and a
// double for it would leave "the topic is joined once" asserting against itself.

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

// --- construction -----------------------------------------------------------

// TestTHEKINDISNOTOPTIONAL. It is what keeps a stream and a bus of one id apart
// on the wire, so a backing with none would deliver each other's messages to
// both.
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

// TestANODEWITHNOCLOUDISSTILLANODE: a development node names none, and every
// node on it leaves the same gap in the same place.
func TestNoCloudIsAllowed(t *testing.T) {
	gossip := &joiner{ps: gossiping(t)}

	bare, err := stream.New(gossip, stream.Stream, "")
	require.NoError(t, err)
	t.Cleanup(func() { bare.Close() })

	require.NoError(t, bare.Publish(t.Context(), "shop/prod/QmOrders", []byte("x")))
	require.Equal(t, []string{"/tau/stream//shop/prod/QmOrders"}, gossip.joined)
}

// --- the topic name ---------------------------------------------------------

// TestTWOPROJECTSUSINGONENAMENEVERMEET, which is the property the vocabulary
// states about a stream's name and this is where it is kept.
func TestTheTopicCarriesTheProjectAndDeployment(t *testing.T) {
	mine := stream.Topic(stream.Stream, "cloud", "shop/prod/QmOrders")
	theirs := stream.Topic(stream.Stream, "cloud", "other/prod/QmOrders")
	staging := stream.Topic(stream.Stream, "cloud", "shop/staging/QmOrders")

	require.NotEqual(t, mine, theirs, "two projects")
	require.NotEqual(t, mine, staging, "two deployments of one project")
	require.Equal(t, "/tau/stream/cloud/shop/prod/QmOrders", mine)
}

// TestTWOCLOUDSSHARINGAMACHINEDONOTDELIVEREACHOTHERSMESSAGES, which is the same
// reason discovery' own bus is scoped by cloud.
func TestTheTopicCarriesTheCloud(t *testing.T) {
	require.NotEqual(t,
		stream.Topic(stream.Stream, "one", "shop/prod/QmOrders"),
		stream.Topic(stream.Stream, "two", "shop/prod/QmOrders"))
}

// TestTHELABELISNOTONTHEWIRE. The handle carries the resolved id; an author's
// label is theirs and two projects may both use `orders`.
func TestTheTopicIsNotTheLabel(t *testing.T) {
	require.NotContains(t, stream.Topic(stream.Stream, "cloud", "shop/prod/QmOrders"), "orders")
}

// TestABUSANDASTREAMDONOTCROSS, even on one handle: two kinds may share an id
// space and a project with both would otherwise deliver each other's messages.
func TestTheKindIsInTheTopic(t *testing.T) {
	require.NotEqual(t,
		stream.Topic(stream.Stream, "cloud", "shop/prod/QmX"),
		stream.Topic(stream.Bus, "cloud", "shop/prod/QmX"))
}

// --- joining ----------------------------------------------------------------

// TestATOPICISJOINEDONCEANDKEPT. Joining is what starts the gossip machinery for
// a topic, so a publisher that rejoined per message would churn the mesh it
// depends on — the same reason services/discovery' bus joins once.
func TestATopicIsJoinedOncePerHandle(t *testing.T) {
	s, gossip := publishing(t, stream.Stream)
	ctx := t.Context()

	require.NoError(t, s.Publish(ctx, "shop/prod/QmOrders", []byte("one")))
	require.NoError(t, s.Publish(ctx, "shop/prod/QmOrders", []byte("two")))
	require.Equal(t, []string{"/tau/stream/cloud/shop/prod/QmOrders"}, gossip.joined)

	require.NoError(t, s.Publish(ctx, "shop/prod/QmOther", []byte("three")))
	require.Len(t, gossip.joined, 2, "a second handle is a second topic")
}

// TestONEBACKINGPERKIND, so a bus and a stream on one node reach different
// topics through different values — and the check that matters is the name that
// went to the router, not the map they are keyed in.
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

// TestANODEWITHNOPUBSUBCANNOTPUBLISH, and says so rather than panicking on the
// first message.
func TestPublishingNeedsPubSub(t *testing.T) {
	bare, err := stream.New(nil, stream.Stream, "cloud")
	require.NoError(t, err, "a node with no gossip still composes")

	err = bare.Publish(t.Context(), "shop/prod/QmOrders", []byte("x"))
	require.ErrorIs(t, err, stream.ErrNoPubSub)
}

// TestAJOINTHATFAILEDISNAMED, because the handle is the only thing an operator
// can look up in a config.
func TestAJoinThatFailsCarriesTheHandle(t *testing.T) {
	s, gossip := publishing(t, stream.Stream)
	gossip.err = errors.New("the router is shutting down")

	err := s.Publish(t.Context(), "shop/prod/QmOrders", []byte("x"))
	require.ErrorContains(t, err, "shutting down")
	require.ErrorContains(t, err, "shop/prod/QmOrders")
}

// TestTOPICSARERELEASEDWITHTHENODE, so a node that published does not leak the
// gossip machinery it started. A publish afterwards joins again, which is what
// says the topics were actually let go of.
func TestClosingReleasesTheTopics(t *testing.T) {
	s, gossip := publishing(t, stream.Stream)

	require.NoError(t, s.Publish(t.Context(), "shop/prod/QmOrders", []byte("x")))
	require.NoError(t, s.Close())
	require.NoError(t, s.Close(), "Close is idempotent")

	require.NoError(t, s.Publish(t.Context(), "shop/prod/QmOrders", []byte("x")))
	require.Len(t, gossip.joined, 2)
}

// --- attaching --------------------------------------------------------------

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

// TestANATTACHMENTGETSWHATTHETIERPUBLISHES. The gateway's browser and the wasm
// tier's `bus.publish` reach one topic, which is the whole reason a handle is
// composed once: a workload publishing to what the author wrote as one bus has
// to arrive where the browser is listening.
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

// TestEVERYTHINGATTACHEDGETSWHATISSENT, the sender included — which is the
// kind's own definition and the reason there is no envelope and no sender id.
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

// TestTHETOPICISJOINEDONCEHOWEVERMANYATTACH. A second join is a second mesh for
// one topic, and a browser would be on the one the workload is not.
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

// TestCLOSINGONEATTACHMENTLEAVESTHEOTHERS, because a subscription is per caller
// and the topic outlives all of them.
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

// TestABUSANDASTREAMDONOTMEET on the wire either, which the topic names say and
// this proves with a live subscription that receives nothing.
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

// TestATTACHINGNEEDSPUBSUB, and says so rather than handing back an attachment
// that never delivers.
func TestAttachingNeedsPubSub(t *testing.T) {
	bare, err := stream.New(nil, stream.Bus, "cloud")
	require.NoError(t, err)

	buses, err := stream.NewBuses(bare)
	require.NoError(t, err)

	_, err = buses.Attach(t.Context(), "shop/prod/QmChat")
	require.ErrorIs(t, err, stream.ErrNoPubSub)
}

// TestASUBSCRIPTIONTHATCOULDNOTBEOPENEDISNOTANATTACHMENT. The topic is joined
// and the subscription is the part that failed, which is a different failure from
// having no gossip at all and is reported as one.
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
