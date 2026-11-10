package protobook_test

import (
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/protobook"
)

const served = protocol.ID("/tau/test/protobook/v1")

func newHost(t *testing.T) host.Host {
	t.Helper()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	t.Cleanup(func() { h.Close() })
	return h
}

func connect(t *testing.T, from, to host.Host) {
	t.Helper()
	require.NoError(t, from.Connect(t.Context(), peer.AddrInfo{ID: to.ID(), Addrs: to.Addrs()}))
}

func kept(t *testing.T, h host.Host) {
	t.Helper()
	k, err := protobook.Keep(t.Context(), h)
	require.NoError(t, err)
	t.Cleanup(k.Close)
}

func serve(h host.Host) {
	h.SetStreamHandler(served, func(s network.Stream) { s.Close() })
}

func book(h host.Host, about peer.ID) []protocol.ID {
	protos, err := h.Peerstore().GetProtocols(about)
	if err != nil {
		return nil
	}
	return protos
}

func lists(h host.Host, about peer.ID) bool { return slices.Contains(book(h, about), served) }

func without(protos []protocol.ID, id protocol.ID) []protocol.ID {
	return slices.DeleteFunc(slices.Clone(protos), func(p protocol.ID) bool { return p == id })
}

// identifiedOnce waits until identify has told from what about serves.
func identifiedOnce(t *testing.T, from host.Host, about peer.ID) {
	t.Helper()
	require.Eventually(t, func() bool { return len(book(from, about)) > 0 }, 5*time.Second, 10*time.Millisecond,
		"identify never told the client what the server serves")
}

// told emits on from what identify emits once it has applied a message from about
// that named protos.
func told(t *testing.T, from host.Host, about peer.ID, protos []protocol.ID) {
	t.Helper()
	conns := from.Network().ConnsToPeer(about)
	require.NotEmpty(t, conns)
	em, err := from.EventBus().Emitter(new(event.EvtPeerIdentificationCompleted))
	require.NoError(t, err)
	defer em.Close()
	require.NoError(t, em.Emit(event.EvtPeerIdentificationCompleted{Peer: about, Conn: conns[0], Protocols: protos}))
}

// announced collects every update h announces about served.
type announced struct {
	added, removed atomic.Int32
}

func watch(t *testing.T, h host.Host) *announced {
	t.Helper()
	sub, err := h.EventBus().Subscribe(new(event.EvtPeerProtocolsUpdated))
	require.NoError(t, err)
	t.Cleanup(func() { sub.Close() })
	a := &announced{}
	go func() {
		for e := range sub.Out() {
			u := e.(event.EvtPeerProtocolsUpdated)
			if slices.Contains(u.Added, served) {
				a.added.Add(1)
			}
			if slices.Contains(u.Removed, served) {
				a.removed.Add(1)
			}
		}
	}()
	return a
}

// counting makes h count every test protocol a peer proposes to it, answering none
// of them itself.
func counting(h host.Host) *atomic.Int32 {
	var n atomic.Int32
	h.Mux().AddHandlerWithFunc("/tau/test/protobook/counts", func(p protocol.ID) bool {
		if strings.HasPrefix(string(p), "/tau/test/") {
			n.Add(1)
		}
		return false
	}, nil)
	return &n
}

// TWO MESSAGES FROM ONE PEER ARE APPLIED AT ONCE and the one that lands last stays, so
// a peer that began to serve can be left listed as it was before.
func TestAnOlderMessageThatLandedLastIsCorrected(t *testing.T) {
	server, client := newHost(t), newHost(t)
	kept(t, client)
	updates := watch(t, client)
	connect(t, client, server)

	serve(server)
	require.Eventually(t, func() bool { return lists(client, server.ID()) }, 5*time.Second, 10*time.Millisecond)

	older := without(book(client, server.ID()), served)
	require.NoError(t, client.Peerstore().SetProtocols(server.ID(), older...))
	told(t, client, server.ID(), older)

	require.Eventually(t, func() bool { return lists(client, server.ID()) }, 5*time.Second, 10*time.Millisecond,
		"an older message that landed last was left in the book")
	require.Eventually(t, func() bool { return updates.added.Load() == 1 }, 5*time.Second, 10*time.Millisecond,
		"the correction was not announced")
}

// THE NEWER MESSAGE CAN BE WRITTEN OVER before it is told, so it is told naming what
// the book leaves out.
func TestANewerMessageWrittenOverBeforeItWasToldIsCorrected(t *testing.T) {
	server, client := newHost(t), newHost(t)
	serve(server)
	connect(t, client, server)
	require.Eventually(t, func() bool { return lists(client, server.ID()) }, 5*time.Second, 10*time.Millisecond)
	kept(t, client)

	newer := book(client, server.ID())
	require.NoError(t, client.Peerstore().SetProtocols(server.ID(), without(newer, served)...))
	told(t, client, server.ID(), newer)

	require.Eventually(t, func() bool { return lists(client, server.ID()) }, 5*time.Second, 10*time.Millisecond,
		"a newer message written over was left out of the book")
}

// AN OLDER MESSAGE CAN NAME WHAT THE PEER HAS SINCE STOPPED SERVING, and land last.
func TestAProtocolTheBookKeepsThatThePeerDoesNotServeIsRemoved(t *testing.T) {
	server, client := newHost(t), newHost(t)
	connect(t, client, server)
	identifiedOnce(t, client, server.ID())
	kept(t, client)
	updates := watch(t, client)

	newer := book(client, server.ID())
	require.NoError(t, client.Peerstore().SetProtocols(server.ID(), append(slices.Clone(newer), served)...))
	told(t, client, server.ID(), newer)

	require.Eventually(t, func() bool { return !lists(client, server.ID()) }, 5*time.Second, 10*time.Millisecond,
		"the book kept a protocol the peer does not serve")
	require.Eventually(t, func() bool { return updates.removed.Load() == 1 }, 5*time.Second, 10*time.Millisecond,
		"the correction was not announced")
}

func TestAPeerThatStopsServingStaysOutOfTheBook(t *testing.T) {
	server, client := newHost(t), newHost(t)
	kept(t, client)
	updates := watch(t, client)
	serve(server)
	connect(t, client, server)
	require.Eventually(t, func() bool { return lists(client, server.ID()) }, 5*time.Second, 10*time.Millisecond)

	server.RemoveStreamHandler(served)

	require.Eventually(t, func() bool { return !lists(client, server.ID()) }, 5*time.Second, 10*time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	assert.False(t, lists(client, server.ID()), "asking put back a protocol the peer stopped serving")
	assert.Zero(t, updates.added.Load())
}

func TestAMessageTheBookAgreesWithAsksNothing(t *testing.T) {
	server, client := newHost(t), newHost(t)
	asked := counting(server)
	kept(t, client)
	connect(t, client, server)
	identifiedOnce(t, client, server.ID())

	serve(server)
	require.Eventually(t, func() bool { return lists(client, server.ID()) }, 5*time.Second, 10*time.Millisecond)

	time.Sleep(200 * time.Millisecond)
	assert.Zero(t, asked.Load(), "a peer whose messages the book agrees with was asked")
}

func TestADisputeIsAskedOnce(t *testing.T) {
	server, client := newHost(t), newHost(t)
	asked := counting(server)
	connect(t, client, server)
	identifiedOnce(t, client, server.ID())
	kept(t, client)

	said := append(book(client, server.ID()), served)
	told(t, client, server.ID(), said)

	require.Eventually(t, func() bool { return asked.Load() == 1 }, 5*time.Second, 10*time.Millisecond,
		"the peer was never asked")
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, int32(1), asked.Load(), "a peer that answered was asked again")
	assert.False(t, lists(client, server.ID()))
}
