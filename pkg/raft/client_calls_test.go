package raft

import (
	"context"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	ds "github.com/ipfs/go-datastore"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/stretchr/testify/require"
	taupeer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/rpc"
	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2/raftv2connect"
)

// connected links n to each of the nodes behind clusters.
func connected(t *testing.T, n taupeer.Node, clusters ...Cluster) {
	t.Helper()
	for _, cl := range clusters {
		host := cl.(*cluster).node.Peer()
		require.NoError(t, n.Peer().Connect(t.Context(), peer.AddrInfo{ID: host.ID(), Addrs: host.Addrs()}))
	}
}

// callerOf is a group of one on its own mock node, running on the store wrap
// makes of the node's own, and a client of the group on a second node: a
// caller from outside the group, the way a node running no member reaches it.
func callerOf(t *testing.T, namespace string, wrap func(ds.Batching) ds.Batching) (Cluster, *client) {
	t.Helper()
	member := newMockNode(t)
	caller := newMockNode(t)
	require.NoError(t, taupeer.LinkAllPeers())

	cl, err := New(storeNode{Node: member, store: wrap(member.Store())}, namespace,
		WithTimeouts(testTimeoutConfig()), WithForceBootstrap(), WithSnapshotRoot(t.TempDir()))
	require.NoError(t, err)
	t.Cleanup(func() { cl.Close() })
	waitLeader(t, cl, 5*time.Second)
	connected(t, caller, cl)

	cli, err := newInternalClient(caller, namespace, nil)
	require.NoError(t, err)
	t.Cleanup(func() { cli.Close() })
	return cl, cli.(*client)
}

func sameStore(store ds.Batching) ds.Batching { return store }

// TestClient_ClusterInfoIsTheMembersOwnView: ClusterInfo is how the healer
// learns another member's leader, term, log and membership, and each comes
// back as that member sees it.
func TestClient_ClusterInfoIsTheMembersOwnView(t *testing.T) {
	cl, cli := callerOf(t, "client-cluster-info", sameStore)
	require.NoError(t, cl.Set("k", []byte("v"), time.Second))

	info, err := cli.ClusterInfo(idOf(cl))
	require.NoError(t, err)
	require.Equal(t, idOf(cl).String(), info.LeaderID)
	require.Equal(t, idOf(cl).String(), info.NodeID)
	require.Equal(t, 1, info.MemberCount)

	stats := cl.(*cluster).raft.Stats()
	require.Equal(t, parseUint64(stats["term"]), info.Term)
	require.GreaterOrEqual(t, parseUint64(stats["last_log_index"]), info.LastIndex)
	require.NotZero(t, info.LastIndex, "the write above is in the log")
}

// TestClient_ACallWithNowhereToGoFails: a call addressed to nobody, to a peer
// id that is not one, or made on a closed client, fails at once rather than
// waiting out its timeout.
func TestClient_ACallWithNowhereToGoFails(t *testing.T) {
	cl, cli := callerOf(t, "client-nowhere", sameStore)

	require.ErrorContains(t, cli.Set("k", []byte("v"), time.Second), "no peers to send to")
	_, _, err := cli.Get("k", 0)
	require.ErrorContains(t, err, "no peers to send to")
	require.ErrorContains(t, cli.Delete("k", time.Second), "no peers to send to")
	_, err = cli.Keys("")
	require.ErrorContains(t, err, "no peers to send to")
	require.ErrorContains(t, cli.JoinVoter(t.Context(), idOf(cl), time.Second), "no peers to send to")

	_, err = cli.Keys("", peer.ID(""))
	require.ErrorContains(t, err, "keys failed")

	require.NoError(t, cli.Close())
	_, err = cli.Keys("", idOf(cl))
	require.ErrorContains(t, err, "client is closed")
	_, err = cli.ClusterInfo(idOf(cl))
	require.ErrorContains(t, err, "client is closed")

	_, err = NewClient(nil, "client-nowhere", nil)
	require.Error(t, err)
}

// TestClient_AnEmptyValueReadsBackPresent: proto does not tell an empty bytes
// field from an absent one, so the client restores what the found flag says:
// a key holding nothing is a key that is there.
func TestClient_AnEmptyValueReadsBackPresent(t *testing.T) {
	cl, cli := callerOf(t, "client-empty-value", sameStore)

	require.NoError(t, cli.Set("empty", nil, time.Second, idOf(cl)))
	value, found, err := cli.Get("empty", 0, idOf(cl))
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, value)
	require.Empty(t, value)

	value, found, err = cli.Get("never", 0, idOf(cl))
	require.NoError(t, err)
	require.False(t, found)
	require.Nil(t, value)
}

// TestClient_KeysMatchingNothingIsAnEmptyList: proto sends no list for an
// empty one, and the client answers with an empty list rather than none.
func TestClient_KeysMatchingNothingIsAnEmptyList(t *testing.T) {
	cl, cli := callerOf(t, "client-empty-keys", sameStore)
	require.NoError(t, cl.Set("held/1", []byte("v"), time.Second))

	keys, err := cli.Keys("nothing/", idOf(cl))
	require.NoError(t, err)
	require.NotNil(t, keys)
	require.Empty(t, keys)

	keys, err = cli.Keys("held/", idOf(cl))
	require.NoError(t, err)
	require.Equal(t, []string{"held/1"}, keys)
}

// TestClient_CallsToAPeerThatRunsNoMemberFail: a peer that does not serve the
// group's protocol answers no call of it, and each says it failed.
func TestClient_CallsToAPeerThatRunsNoMemberFail(t *testing.T) {
	cl, _ := callerOf(t, "client-served", sameStore)
	caller := newMockNode(t)
	require.NoError(t, taupeer.LinkAllPeers())
	connected(t, caller, cl)

	cliOf, err := newInternalClient(caller, "client-not-served", nil)
	require.NoError(t, err)
	defer cliOf.Close()
	cli := cliOf.(*client)
	target := idOf(cl)

	require.ErrorContains(t, cli.Set("k", []byte("v"), time.Second, target), "set failed")
	_, _, err = cli.Get("k", 0, target)
	require.ErrorContains(t, err, "get failed")
	require.ErrorContains(t, cli.Delete("k", time.Second, target), "delete failed")
	_, err = cli.Keys("", target)
	require.ErrorContains(t, err, "keys failed")
	require.ErrorContains(t, cli.JoinVoter(t.Context(), caller.ID(), time.Second, target), "join voter failed")
	_, _, err = cli.ExchangePeers(time.Now(), nil, target)
	require.Error(t, err)
	_, err = cli.ClusterInfo(target)
	require.ErrorContains(t, err, "clusterInfo failed")
	_, _, err = cli.ExportFSM(target)
	require.ErrorContains(t, err, "exportFSM failed")
	require.Error(t, cli.HealAck(target))
}

// TestClient_ExchangePeersWithNoneOfItsOwn: a caller that has seen no peers
// still learns the member's, and the member learns the caller from the call.
func TestClient_ExchangePeersWithNoneOfItsOwn(t *testing.T) {
	cl, cli := callerOf(t, "client-exchange", sameStore)

	theirStart, theirPeers, err := cli.ExchangePeers(time.Now(), nil, idOf(cl))
	require.NoError(t, err)
	require.False(t, theirStart.IsZero())
	require.Contains(t, theirPeers, idOf(cl).String())
	require.Contains(t, theirPeers, cli.node.ID().String(), "the member did not learn its caller")
}

// garbledExport answers ExportFSM with bytes that are not a state.
type garbledExport struct {
	raftv2connect.UnimplementedRaftServiceHandler
}

func (garbledExport) ExportFSM(context.Context, *connect.Request[raftv2.ExportFSMRequest]) (*connect.Response[raftv2.ExportFSMResponse], error) {
	return connect.NewResponse(&raftv2.ExportFSMResponse{FsmState: []byte{0xff, 0xfe}, Clock: 3}), nil
}

// TestClient_AStateThatWillNotDecodeIsAnError: the healer merges what
// ExportFSM returns, so a state that cannot be read is an error and never an
// empty one, which would merge as nothing at all.
func TestClient_AStateThatWillNotDecodeIsAnError(t *testing.T) {
	const namespace = "client-garbled-export"
	server := newMockNode(t)
	caller := newMockNode(t)
	require.NoError(t, taupeer.LinkAllPeers())
	require.NoError(t, caller.Peer().Connect(t.Context(), peer.AddrInfo{ID: server.ID(), Addrs: server.Peer().Addrs()}))

	route, handler := raftv2connect.NewRaftServiceHandler(garbledExport{})
	mux := http.NewServeMux()
	mux.Handle(route, handler)
	srv, err := rpc.Listen(t.Context(), server.Peer(), protocol.ID(Protocol(namespace)), mux)
	require.NoError(t, err)
	defer srv.Close()

	cliOf, err := newInternalClient(caller, namespace, nil)
	require.NoError(t, err)
	defer cliOf.Close()

	state, clock, err := cliOf.ExportFSM(server.ID())
	require.ErrorContains(t, err, "unmarshaling FSM state")
	require.Nil(t, state)
	require.Zero(t, clock)
}

// TestStream_AWriteTheLeadersStoreRefusesComesBackRefused: a write carried
// over the wire fails with the store's reason, not as an acknowledgement.
func TestStream_AWriteTheLeadersStoreRefusesComesBackRefused(t *testing.T) {
	store := newFaultStore()
	cl, cli := callerOf(t, "stream-refused-write", func(ds.Batching) ds.Batching { return store })
	store.failPut("/raft/stream-refused-write/data/k", errNoSpace)

	require.ErrorContains(t, cli.Set("k", []byte("v"), time.Second, idOf(cl)), errNoSpace.Error())
	require.ErrorContains(t, cli.Delete("k", time.Second, idOf(cl)), errNoSpace.Error())

	_, found, err := cl.TryGet("k")
	require.NoError(t, err)
	require.False(t, found)
}

// TestStream_AnExportTheLeaderCannotReadIsRefused: the side that loses a split
// is told to wipe itself once its export is merged, so an export cut short by
// the store has to fail rather than hand over part of the state.
func TestStream_AnExportTheLeaderCannotReadIsRefused(t *testing.T) {
	store := newFaultStore()
	cl, cli := callerOf(t, "stream-refused-export", func(ds.Batching) ds.Batching { return store })
	require.NoError(t, cl.Set("k", []byte("v"), time.Second))
	store.failScan("/raft/stream-refused-export/data", errDisk)

	state, _, err := cli.ExportFSM(idOf(cl))
	require.ErrorContains(t, err, errDisk.Error())
	require.Nil(t, state)
}

// TestStream_AConsistentReadFromAMemberThatCannotBarrierIsRefused: a read that
// asks for a barrier is served only by a member that can raise one; a member
// that cannot answers ErrNotLeader rather than its own, possibly stale, state.
func TestStream_AConsistentReadFromAMemberThatCannotBarrierIsRefused(t *testing.T) {
	caller := newMockNode(t)
	a, b := joinedPair(t, "stream-barrier-refused", sameStore)
	connected(t, caller, a, b)
	require.NoError(t, onLeader(t, []Cluster{a, b}, func(l Cluster) error {
		return l.Set("k", []byte("v"), time.Second)
	}))

	removed, left := removeTheLeader(t, a, b)

	cliOf, err := newInternalClient(caller, "stream-barrier-refused", nil)
	require.NoError(t, err)
	defer cliOf.Close()
	cli := cliOf.(*client)

	_, _, err = cli.Get("k", int64(time.Second), idOf(removed))
	require.ErrorIs(t, err, ErrNotLeader)

	value, found, err := cli.Get("k", int64(time.Second), idOf(left))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("v"), value)
}

// TestStream_JoinVoterNamingNobodyJoinsTheCaller: a join that names no peer is
// the caller asking for itself, and the group adds the caller, not some other
// member, so asking again from inside the group changes nothing.
func TestStream_JoinVoterNamingNobodyJoinsTheCaller(t *testing.T) {
	a, b := joinedPair(t, "stream-join-caller", sameStore)

	fromB, err := newInternalClient(b.(*cluster).node, "stream-join-caller", nil)
	require.NoError(t, err)
	defer fromB.Close()
	require.NoError(t, fromB.JoinVoter(t.Context(), peer.ID(""), 5*time.Second, idOf(a)))

	members, err := a.Members()
	require.NoError(t, err)
	ids := make([]peer.ID, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.ID)
	}
	require.ElementsMatch(t, []peer.ID{idOf(a), idOf(b)}, ids)
}

// TestStream_JoinVoterNamingNoPeerIsRefused: a join names a peer id, or
// arrives over a stream whose caller it can name instead; one that does
// neither is refused before anything is asked of the group.
func TestStream_JoinVoterNamingNoPeerIsRefused(t *testing.T) {
	cl, ss := leaderService(t)

	_, err := ss.JoinVoter(context.Background(), connect.NewRequest(&raftv2.JoinVoterRequest{Peer: "not-a-peer-id"}))
	require.Error(t, err)
	_, err = ss.JoinVoter(context.Background(), connect.NewRequest(&raftv2.JoinVoterRequest{}))
	require.ErrorContains(t, err, "missing peer id")

	members, err := cl.Members()
	require.NoError(t, err)
	require.Len(t, members, 1)
}

// TestStream_ExportFSMAskedOfEitherMemberIsTheGroupsState: the healer asks a
// group for its state through whichever member it reaches, and a follower
// forwards the call to its leader.
func TestStream_ExportFSMAskedOfEitherMemberIsTheGroupsState(t *testing.T) {
	caller := newMockNode(t)
	a, b := joinedPair(t, "stream-export-either", sameStore)
	connected(t, caller, a, b)
	require.NoError(t, onLeader(t, []Cluster{a, b}, func(l Cluster) error {
		return l.Set("k", []byte("v"), time.Second)
	}))

	cliOf, err := newInternalClient(caller, "stream-export-either", nil)
	require.NoError(t, err)
	defer cliOf.Close()

	for _, member := range []Cluster{a, b} {
		require.Eventually(t, func() bool {
			state, _, err := cliOf.ExportFSM(idOf(member))
			return err == nil && string(state["k"].Value) == "v"
		}, leaderWait, 20*time.Millisecond, "asking %s for the group's state", idOf(member).ShortString())
	}
}

// TestStream_AJoinAskedOfAFollowerIsForwardedToItsLeader: a peer asking to
// join may reach any member, and one that follows hands the request to its
// leader, which lets the peer in.
func TestStream_AJoinAskedOfAFollowerIsForwardedToItsLeader(t *testing.T) {
	const namespace = "stream-join-forwarded"
	caller := newMockNode(t)
	a, b := joinedPair(t, namespace, sameStore)
	connected(t, caller, a, b)
	cli, err := newInternalClient(caller, namespace, nil)
	require.NoError(t, err)
	defer cli.Close()

	leader := leaderOf(t, a, b)
	follower := a
	if leader == a {
		follower = b
	}
	joiner := strangerID(t)
	require.NoError(t, cli.JoinVoter(t.Context(), joiner, 5*time.Second, idOf(follower)))
	members, err := leader.Members()
	require.NoError(t, err)
	ids := make([]peer.ID, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.ID)
	}
	require.ElementsMatch(t, []peer.ID{idOf(a), idOf(b), joiner}, ids)
}

// TestStream_AJoinAskedOfAFollowerBetweenLeadersWaitsForTheNext: a follower
// whose leader is gone knows no leader until the group elects the next, and a
// join asked of it meanwhile waits for that leader instead of being refused.
// Three of four voters elect without the fourth, and commit a fifth.
func TestStream_AJoinAskedOfAFollowerBetweenLeadersWaitsForTheNext(t *testing.T) {
	const namespace = "stream-join-between-leaders"
	caller := newMockNode(t)
	group := joinedGroup(t, namespace, sameStore, 4)
	connected(t, caller, group...)
	cli, err := newInternalClient(caller, namespace, nil)
	require.NoError(t, err)
	defer cli.Close()

	gone := leaderOf(t, group...)
	var survivors []Cluster
	for _, cl := range group {
		if cl != gone {
			survivors = append(survivors, cl)
		}
	}
	require.NoError(t, gone.Close())
	var follower Cluster
	require.Eventually(t, func() bool {
		for _, cl := range survivors {
			if _, err := cl.Leader(); err != nil {
				follower = cl
				return true
			}
		}
		return false
	}, leaderWait, time.Millisecond, "every survivor kept naming the closed leader")

	joiner := strangerID(t)
	require.NoError(t, cli.JoinVoter(t.Context(), joiner, leaderWait, idOf(follower)))
	members, err := leaderOf(t, survivors...).Members()
	require.NoError(t, err)
	ids := make([]peer.ID, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.ID)
	}
	require.Contains(t, ids, joiner)
}
