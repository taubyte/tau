package node

import (
	"testing"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node/stream"
)

// What is left here is the wiring: node/stream holds the topics and its own
// tests hold the rules, and what only this package can say is which gossip and
// which cloud a backing was built over, and that a bus has one backing.

// TestBUSTOPICISTHECONTRACTWHATEVERATTACHES. Whatever comes to attach a browser
// has to listen on exactly what a publish reaches.
func TestTheNodeExposesBothTopics(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker, Discovery}})

	require.Equal(t, stream.Topic(stream.Bus, n.cfg.Cloud, "h"), n.BusTopic("h"))
	require.Equal(t, stream.Topic(stream.Stream, n.cfg.Cloud, "h"), n.StreamTopic("h"))
	require.NotEqual(t, n.BusTopic("h"), n.StreamTopic("h"))
}

// TestONEBUSBACKINGFORTHENODE, shared by the wasm tier's publish and the
// gateway's attach. Two would be two joins of one topic — a mesh each, with the
// browser on the one the workload is not.
func TestOneBusBackingIsSharedByBothConsumers(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker, Discovery, Gateway}})

	first, err := n.busStreams()
	require.NoError(t, err)
	again, err := n.busStreams()
	require.NoError(t, err)

	require.Same(t, first, again)
	require.Same(t, n.bus, first, "and it is the one the composition kept")
}

// TestANODEWITHNOPUBSUBSTILLCOMPOSES, and its publisher refuses by name.
//
// THE NIL IS THE POINT. opts.pubsub is an interface, so a backing built over an
// absent one has to arrive as a nil the adapter can test — which is what makes
// this a wiring test rather than a repeat of the adapter's own.
func TestABackingOverNoPubSubRefusesByName(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	n, err := New(Config{Services: []string{Worker}},
		WithHost(newHost(t, mn)), WithDatabases(&memFactory{}), WithRuntimes(&tier{name: "wasm"}))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })

	publisher, err := n.streamBacking(stream.Stream)
	require.NoError(t, err)

	err = publisher.Publish(t.Context(), "shop/prod/QmOrders", []byte("x"))
	require.ErrorIs(t, err, stream.ErrNoPubSub)
}
