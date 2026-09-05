package node

import (
	"context"
	"strings"
	"testing"
	"time"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	gateway "github.com/taubyte/tau/services/v2/gateway"
)

func publishing(t *testing.T) *streams {
	t.Helper()
	n, _, _ := makeNode(t, Config{Services: []string{Worker, Stats}})
	return &streams{node: n}
}

// TestATopicIsJoinedOnceAndKept. Joining is what starts the gossip machinery
// for a topic, so a publisher that rejoined per message would churn the mesh it
// depends on — the same reason services/stats' bus joins once.
func TestATopicIsJoinedOncePerHandle(t *testing.T) {
	s := publishing(t)
	ctx := t.Context()

	require.NoError(t, s.Publish(ctx, "shop/prod/QmOrders", []byte("one")))
	require.NoError(t, s.Publish(ctx, "shop/prod/QmOrders", []byte("two")))
	require.Len(t, s.topics, 1)

	require.NoError(t, s.Publish(ctx, "shop/prod/QmOther", []byte("three")))
	require.Len(t, s.topics, 2, "a second handle is a second topic")
}

// TestTwoProjectsUsingOneNameNeverMeet, which is the property the vocabulary
// states about a stream's name and this is where it is kept.
func TestTheTopicCarriesTheProjectAndDeployment(t *testing.T) {
	mine := streamTopic("cloud", "shop/prod/QmOrders")
	theirs := streamTopic("cloud", "other/prod/QmOrders")
	staging := streamTopic("cloud", "shop/staging/QmOrders")

	require.NotEqual(t, mine, theirs, "two projects")
	require.NotEqual(t, mine, staging, "two deployments of one project")
	require.Contains(t, mine, "shop/prod/QmOrders")
}

// TestTwoCloudsSharingAMachineDoNotDeliverEachOthersMessages, which is the
// same reason stats' own bus is scoped by cloud.
func TestTheTopicCarriesTheCloud(t *testing.T) {
	require.NotEqual(t,
		streamTopic("one", "shop/prod/QmOrders"),
		streamTopic("two", "shop/prod/QmOrders"))
}

// TestTheLabelIsNotOnTheWire. The handle carries the resolved id; an author's
// label is theirs and two projects may both use `orders`.
func TestTheTopicIsNotTheLabel(t *testing.T) {
	require.NotContains(t, streamTopic("cloud", "shop/prod/QmOrders"), "orders\"")
	require.True(t, strings.HasPrefix(streamTopic("cloud", "h"), "/tau/stream/"))
}

// TestANodeWithNoPubSubCannotPublish, and says so rather than panicking on the
// first message.
func TestPublishingNeedsPubSub(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	n, err := New(Config{Services: []string{Worker}},
		WithHost(h), WithDatabases(&memFactory{}), WithRuntimes(&tier{name: "wasm"}))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })

	err = (&streams{node: n}).Publish(t.Context(), "shop/prod/QmOrders", []byte("x"))
	require.ErrorIs(t, err, ErrNoPubSubForStreams)
}

// TestTopicsAreReleasedWithTheNode, so a node that published does not leak the
// gossip machinery it started.
func TestClosingReleasesTheTopics(t *testing.T) {
	s := publishing(t)
	require.NoError(t, s.Publish(t.Context(), "shop/prod/QmOrders", []byte("x")))
	require.Len(t, s.topics, 1)

	require.NoError(t, s.Close())
	require.Empty(t, s.topics)
	require.NoError(t, s.Close(), "Close is idempotent")
}

// ── the bus ──────────────────────────────────────────────────────────────────

// TestABusAndAStreamDoNotCROSS, even on one handle: two kinds may share an id
// space and a project with both would otherwise deliver each other's messages.
func TestTheKindIsInTheTopic(t *testing.T) {
	require.NotEqual(t,
		topicFor("stream", "cloud", "shop/prod/QmX"),
		topicFor("bus", "cloud", "shop/prod/QmX"))

	require.Contains(t, topicFor("bus", "cloud", "h"), "/tau/bus/")
	require.Contains(t, topicFor("stream", "cloud", "h"), "/tau/stream/")
}

// TestBusTopicIsTheContractWhateverAttaches. Whatever comes to attach a browser
// has to listen on exactly what a publish reaches.
func TestTheNodeExposesBothTopics(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker, Stats}})

	require.Equal(t, topicFor("bus", n.cfg.Cloud, "h"), n.BusTopic("h"))
	require.Equal(t, topicFor("stream", n.cfg.Cloud, "h"), n.StreamTopic("h"))
	require.NotEqual(t, n.BusTopic("h"), n.StreamTopic("h"))
}

// TestOneBackingPerKind, so a bus and a stream on one node join different
// topics through different values.
func TestAStreamAndABusAreSeparateBackings(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker, Stats}})
	ctx := t.Context()

	stream := &streams{node: n, kind: "stream"}
	bus := &streams{node: n, kind: "bus"}

	require.NoError(t, stream.Publish(ctx, "shop/prod/QmX", []byte("a")))
	require.NoError(t, bus.Publish(ctx, "shop/prod/QmX", []byte("b")))

	require.Len(t, stream.topics, 1)
	require.Len(t, bus.topics, 1)

	// BOTH ARE KEYED BY HANDLE, so the maps look alike — what has to differ is
	// the gossipsub topic each joined, which is the thing on the wire.
	require.NotEqual(t,
		stream.topics["shop/prod/QmX"].String(),
		bus.topics["shop/prod/QmX"].String(),
		"one handle, two topics")
}

// ── attaching ────────────────────────────────────────────────────────────────

// TestAnAttachmentGetsWhatTheTierPublishes. The gateway's browser and the wasm
// tier's `bus.publish` reach one topic, which is the whole reason
// project.Handle is one function: a workload publishing to what the author
// wrote as one bus has to arrive where the browser is listening.
func TestAnAttachmentGetsWhatTheTierPublishes(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker, Stats}})
	ctx := t.Context()

	tier := n.busStreams()
	at, err := (&buses{streams: tier}).Attach(ctx, "shop/prod/QmChat")
	require.NoError(t, err)
	t.Cleanup(func() { at.Close() })

	require.NoError(t, tier.Publish(ctx, "shop/prod/QmChat", []byte("hello")))

	message, err := at.Next(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", string(message))
}

// TestEverythingAttachedGetsWhatIsSent, the sender included — which is the
// kind's own definition and the reason there is no envelope and no sender id.
func TestASecondAttachmentGetsItToo(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker, Stats}})
	ctx := t.Context()

	backing := &buses{streams: n.busStreams()}
	first, err := backing.Attach(ctx, "shop/prod/QmChat")
	require.NoError(t, err)
	t.Cleanup(func() { first.Close() })

	second, err := backing.Attach(ctx, "shop/prod/QmChat")
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

// TestTheTopicIsJoinedOnceHoweverManyAttach. A second join is a second mesh for
// one topic, and a browser would be on the one the workload is not.
func TestAttachingDoesNotRejoinTheTopic(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker, Stats}})
	ctx := t.Context()

	tier := n.busStreams()
	backing := &buses{streams: tier}
	for range 3 {
		at, err := backing.Attach(ctx, "shop/prod/QmChat")
		require.NoError(t, err)
		t.Cleanup(func() { at.Close() })
	}
	require.NoError(t, tier.Publish(ctx, "shop/prod/QmChat", []byte("x")))

	require.Len(t, tier.topics, 1, "three attachments and a publish, one joined topic")
}

// TestABusAndAStreamDoNotMEET on the wire either, which the topic names say and
// this proves with two live subscriptions.
func TestAnAttachmentDoesNotSeeAStream(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker, Stats}})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	at, err := (&buses{streams: n.busStreams()}).Attach(ctx, "shop/prod/QmX")
	require.NoError(t, err)
	t.Cleanup(func() { at.Close() })

	published := &streams{node: n, kind: "stream"}
	t.Cleanup(func() { published.Close() })
	require.NoError(t, published.Publish(ctx, "shop/prod/QmX", []byte("on the stream")))

	// Nothing arrives, so the assertion is a deadline that has to expire.
	short, stop := context.WithTimeout(ctx, 250*time.Millisecond)
	defer stop()
	_, err = at.Next(short)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestClosingOneAttachmentLeavesTheOthers, because a subscription is per caller
// and the topic outlives all of them.
func TestClosingOneAttachmentLeavesTheOthers(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker, Stats}})
	ctx := t.Context()

	tier := n.busStreams()
	backing := &buses{streams: tier}
	leaving, err := backing.Attach(ctx, "shop/prod/QmChat")
	require.NoError(t, err)
	staying, err := backing.Attach(ctx, "shop/prod/QmChat")
	require.NoError(t, err)
	t.Cleanup(func() { staying.Close() })

	require.NoError(t, leaving.Close())
	require.NoError(t, tier.Publish(ctx, "shop/prod/QmChat", []byte("still here")))

	message, err := staying.Next(ctx)
	require.NoError(t, err)
	require.Equal(t, "still here", string(message))
	require.Len(t, tier.topics, 1, "and the topic was not left")
}

// TestAttachingNeedsPubSub, and says so rather than handing back an attachment
// that never delivers.
func TestAttachingNeedsPubSub(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	n, err := New(Config{Services: []string{Worker}},
		WithHost(newHost(t, mn)), WithDatabases(&memFactory{}), WithRuntimes(&tier{name: "wasm"}))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })

	_, err = (&buses{streams: &streams{node: n, kind: "bus"}}).Attach(t.Context(), "shop/prod/QmChat")
	require.ErrorIs(t, err, ErrNoPubSubForStreams)
}
