package node

import (
	"strings"
	"testing"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"
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
