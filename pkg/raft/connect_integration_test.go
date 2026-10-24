//go:build raft_integration

package raft

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"github.com/libp2p/go-libp2p"
	peercore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/protocol/identify"
	libp2ptls "github.com/libp2p/go-libp2p/p2p/security/tls"
	tcp "github.com/libp2p/go-libp2p/p2p/transport/tcp"
	taupeer "github.com/taubyte/tau/p2p/peer"
	"gotest.tools/v3/assert"
)

// connectWait bounds how long a node takes to serve what a test waits on and
// to be connected, the bound the tests gave a connection before.
const connectWait = 10 * time.Second

func addrOf(node taupeer.Node) peercore.AddrInfo {
	return peercore.AddrInfo{ID: node.ID(), Addrs: node.Peer().Addrs()}
}

// listsAll reports whether listed holds every protocol of want.
func listsAll(listed, want []protocol.ID) bool {
	for _, p := range want {
		if !slices.Contains(listed, p) {
			return false
		}
	}
	return true
}

// identifiesAll returns once an identify of node lists every protocol node
// serves. Identify answers from a snapshot of them that its own goroutine
// refreshes after each change, so a protocol node has just begun to serve can
// be missing from an identify run at that moment.
func identifiesAll(ctx context.Context, node taupeer.Node) error {
	probe, err := libp2p.New(libp2p.NoListenAddrs, libp2p.NoTransports, libp2p.Transport(tcp.NewTCPTransport),
		libp2p.Security(libp2ptls.ID, libp2ptls.New))
	if err != nil {
		return err
	}
	defer probe.Close()

	deadline := time.Now().Add(connectWait)
	for {
		served := node.Peer().Mux().Protocols()
		if probe.Connect(ctx, addrOf(node)) == nil {
			listed, err := probe.Peerstore().GetProtocols(node.ID())
			if err == nil && listsAll(listed, served) {
				return nil
			}
			probe.Network().ClosePeer(node.ID())
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no identify of %s listed what it serves within %v", node.ID(), connectWait)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// link connects node to each of members once an identify of every one of them
// lists everything it serves, so the identify each side runs on a connection
// tells it what the other serves. That list is the only way a member's
// bootstrap finds the others. A side that begins to serve a group after the
// two connected is known to serve it only once identify's push arrives: one
// best-effort attempt, lost under load, and a member that has not heard it
// within its bootstrap timeout founds a group of its own.
func link(ctx context.Context, node taupeer.Node, members ...taupeer.Node) error {
	for _, n := range append([]taupeer.Node{node}, members...) {
		if err := identifiesAll(ctx, n); err != nil {
			return err
		}
	}
	return connectAll(ctx, node, members...)
}

// connectAll connects node to each of members at once.
func connectAll(ctx context.Context, node taupeer.Node, members ...taupeer.Node) error {
	errs := make([]error, len(members))
	var wg sync.WaitGroup
	for i, member := range members {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := node.Peer().Connect(ctx, addrOf(member)); err != nil {
				errs[i] = fmt.Errorf("connecting %s to %s: %w", node.ID(), member.ID(), err)
			}
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

// linkTo is link failing the test: a node about to join a group, or to call
// one, connects to members already serving it.
func linkTo(t *testing.T, node taupeer.Node, members ...taupeer.Node) {
	t.Helper()
	assert.NilError(t, link(t.Context(), node, members...))
}

// serves returns once node's mux lists the group's protocols.
func serves(ctx context.Context, node taupeer.Node, namespace string) error {
	group := []protocol.ID{protocol.ID(Protocol(namespace)), protocol.ID(TransportProtocol(namespace))}
	deadline := time.Now().Add(connectWait)
	for !listsAll(node.Peer().Mux().Protocols(), group) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not serve %s within %v", node.ID(), namespace, connectWait)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

// linkAsTheyServe links each of nodes, once it serves the group, to the nodes
// before it, whose identifies were seen to list all they serve when they were
// linked in turn.
func linkAsTheyServe(ctx context.Context, namespace string, nodes []taupeer.Node) error {
	for i, node := range nodes {
		if err := serves(ctx, node, namespace); err != nil {
			return err
		}
		if err := identifiesAll(ctx, node); err != nil {
			return err
		}
		if err := connectAll(ctx, node, nodes[:i]...); err != nil {
			return err
		}
	}
	return nil
}

// startTogether starts a member of namespace on each of nodes at once, the one
// on nodes[i] after delays[i], and links each node, once it serves the group,
// to the nodes started before it. Every member is then still waiting out its
// bootstrap timeout, so they form the group by agreeing on its founders. Each
// member is closed when the test ends.
func startTogether(t *testing.T, namespace string, nodes []taupeer.Node, delays []time.Duration, opts ...Option) []Cluster {
	t.Helper()
	clusters := make([]Cluster, len(nodes))
	errs := make([]error, len(nodes))
	var started sync.WaitGroup
	for i, node := range nodes {
		started.Add(1)
		go func() {
			defer started.Done()
			time.Sleep(delays[i])
			clusters[i], errs[i] = New(node, namespace, opts...)
		}()
	}

	linked := linkAsTheyServe(t.Context(), namespace, nodes)
	started.Wait()
	for _, cl := range clusters {
		if cl != nil {
			t.Cleanup(func() { cl.Close() })
		}
	}

	assert.NilError(t, linked)
	for i, err := range errs {
		assert.NilError(t, err, "failed to create member %d", i)
	}
	return clusters
}

// pushlessNode is a test node that refuses identify's push, so all it knows a
// peer serves is what the identify run on connecting told it.
func pushlessNode(t *testing.T) taupeer.Node {
	node := newTestNode(t)
	node.Peer().RemoveStreamHandler(identify.IDPush)
	return node
}

// TestCluster_AGroupFormsWithNoIdentifyPush_Integration: a member's bootstrap
// finds the others only through the protocols its peerstore lists for its
// connected peers. With no push, a node connected before a peer served the
// group never learns that it does, and founds a group of its own. Connected
// once the peer serves it, whether joining a leader or starting alongside the
// others, it joins.
func TestCluster_AGroupFormsWithNoIdentifyPush_Integration(t *testing.T) {
	const namespace = "pushless"
	leading := func(t *testing.T, node taupeer.Node) Cluster {
		cl, err := New(node, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
		assert.NilError(t, err, "failed to create the leader")
		t.Cleanup(func() { cl.Close() })
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		assert.NilError(t, cl.WaitForLeader(ctx), "the leader failed to elect itself")
		return cl
	}
	joining := func(t *testing.T, node taupeer.Node) Cluster {
		cl, err := New(node, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
		assert.NilError(t, err, "failed to create the joiner")
		t.Cleanup(func() { cl.Close() })
		return cl
	}

	t.Run("connected before the group exists, a node founds its own", func(t *testing.T) {
		leaderNode, joinerNode := pushlessNode(t), pushlessNode(t)
		assert.NilError(t, joinerNode.Peer().Connect(t.Context(), addrOf(leaderNode)))

		leader := leading(t, leaderNode)
		joiner := joining(t, joinerNode)

		assert.Assert(t, !supportsRaftProtocol(joinerNode.Peer(), leaderNode.ID(), protocol.ID(TransportProtocol(namespace))),
			"the joiner learned with no push that the leader serves the group")
		for _, cl := range []Cluster{leader, joiner} {
			members, err := cl.Members()
			assert.NilError(t, err)
			assert.Equal(t, len(members), 1, "%s is not in a group of its own", idOf(cl))
			assert.Equal(t, members[0].ID, idOf(cl))
		}
	})

	t.Run("connected once the group is served, a node joins it", func(t *testing.T) {
		leaderNode, joinerNode := pushlessNode(t), pushlessNode(t)
		leader := leading(t, leaderNode)
		linkTo(t, joinerNode, leaderNode)

		joiner := joining(t, joinerNode)
		assert.NilError(t, waitForMember(t, leader, joinerNode.ID(), raft.Voter, 20*time.Second), "the joiner never joined")
		leaderOf(t, leader, joiner)
	})

	t.Run("started together and linked while they bootstrap, nodes form one group", func(t *testing.T) {
		nodes := []taupeer.Node{pushlessNode(t), pushlessNode(t), pushlessNode(t)}
		clusters := startTogether(t, namespace, nodes, make([]time.Duration, len(nodes)),
			WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(3*time.Second))

		leaderOf(t, clusters...)
		for _, cl := range clusters {
			for _, node := range nodes {
				assert.NilError(t, waitForMember(t, cl, node.ID(), raft.Voter, 20*time.Second),
					"%s does not hold %s as a voter", idOf(cl), node.ID())
			}
		}
	})
}
