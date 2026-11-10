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

func connected(t *testing.T, n taupeer.Node, clusters ...Cluster) {
	t.Helper()
	for _, cl := range clusters {
		host := cl.(*cluster).Host()
		require.NoError(t, n.Host().Connect(t.Context(), peer.AddrInfo{ID: host.ID(), Addrs: host.Addrs()}))
	}
}

// callerOf is a group of one on its own mock node, over the store wrap makes
// of the node's, and a client on a second node: a caller from outside the
// group.
func callerOf(t *testing.T, namespace string, wrap func(ds.Batching) ds.Batching) (Cluster, *client) {
	t.Helper()
	member := newMockNode(t)
	caller := newMockNode(t)
	require.NoError(t, taupeer.LinkAllPeers())

	cl, err := New(storeNode{Node: member, store: wrap(member.Store())}, namespace,
		WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err)
	t.Cleanup(func() { cl.Close() })
	waitLeader(t, cl, 5*time.Second)
	connected(t, caller, cl)

	cli, err := newInternalClient(caller, namespace, nil, quickRetry)
	require.NoError(t, err)
	t.Cleanup(func() { cli.Close() })
	return cl, cli.(*client)
}

func sameStore(store ds.Batching) ds.Batching { return store }

// formationOf is the formation cl's member is bound to, which a join asked of its
// group names.
func formationOf(cl Cluster) []byte { return cl.(*cluster).current().raft.Formation() }

func TestClient_ClusterInfoIsTheMembersOwnView(t *testing.T) {
	cl, cli := callerOf(t, "client-cluster-info", sameStore)
	require.NoError(t, cl.Set("k", []byte("v"), time.Second))

	info, err := cli.ClusterInfo(idOf(cl))
	require.NoError(t, err)
	require.Equal(t, idOf(cl).String(), info.LeaderID)
	require.Equal(t, idOf(cl).String(), info.NodeID)
	require.Equal(t, 1, info.MemberCount)

	g := cl.(*cluster).current().raft
	require.Equal(t, g.Term(), info.Term)
	require.GreaterOrEqual(t, g.LastIndex(), info.LastIndex)
	require.NotZero(t, info.LastIndex, "the write above is in the log")
}

func TestClient_ACallWithNowhereToGoFails(t *testing.T) {
	cl, cli := callerOf(t, "client-nowhere", sameStore)

	require.ErrorContains(t, cli.Set("k", []byte("v"), time.Second), "no peers to send to")
	_, _, err := cli.Get("k", 0)
	require.ErrorContains(t, err, "no peers to send to")
	require.ErrorContains(t, cli.Delete("k", time.Second), "no peers to send to")
	_, err = cli.Keys("")
	require.ErrorContains(t, err, "no peers to send to")
	require.ErrorContains(t, cli.JoinVoter(t.Context(), idOf(cl), time.Second, nil, 0), "no peers to send to")

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

func TestClient_CallsToAPeerThatRunsNoMemberFail(t *testing.T) {
	cl, _ := callerOf(t, "client-served", sameStore)
	caller := newMockNode(t)
	require.NoError(t, taupeer.LinkAllPeers())
	connected(t, caller, cl)

	cliOf, err := newInternalClient(caller, "client-not-served", nil, quickRetry)
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
	require.ErrorContains(t, cli.JoinVoter(t.Context(), caller.ID(), time.Second, nil, 0, target), "join voter failed")
	_, _, err = cli.ExchangePeers(time.Now(), nil, target)
	require.Error(t, err)
	_, err = cli.ClusterInfo(target)
	require.ErrorContains(t, err, "clusterInfo failed")
	_, err = cli.ExportFSM(target, 0)
	require.ErrorContains(t, err, "exportFSM failed")
	require.Error(t, cli.HealAck(target, receipt{}))
}

func TestClient_ExchangePeersWithNoneOfItsOwn(t *testing.T) {
	cl, cli := callerOf(t, "client-exchange", sameStore)

	theirStart, theirPeers, err := cli.ExchangePeers(time.Now(), nil, idOf(cl))
	require.NoError(t, err)
	require.False(t, theirStart.IsZero())
	require.Contains(t, theirPeers, idOf(cl).String())
	require.Contains(t, theirPeers, cli.ID().String(), "the member did not learn its caller")
}

// garbledExport answers ExportFSM with bytes that are not a protobuf message.
func garbledExport(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/proto")
	w.Write([]byte{0xff, 0xfe})
}

func TestClient_AStateThatWillNotDecodeIsAnError(t *testing.T) {
	const namespace = "client-garbled-export"
	server := newMockNode(t)
	caller := newMockNode(t)
	require.NoError(t, taupeer.LinkAllPeers())
	require.NoError(t, caller.Host().Connect(t.Context(), peer.AddrInfo{ID: server.ID(), Addrs: server.Host().Addrs()}))

	mux := http.NewServeMux()
	mux.HandleFunc(raftv2connect.RaftServiceExportFSMProcedure, garbledExport)
	srv, err := rpc.Listen(t.Context(), server.Host(), protocol.ID(Protocol(namespace)), mux)
	require.NoError(t, err)
	defer srv.Close()

	cliOf, err := newInternalClient(caller, namespace, nil, quickRetry)
	require.NoError(t, err)
	defer cliOf.Close()

	exp, err := cliOf.ExportFSM(server.ID(), 0)
	require.ErrorContains(t, err, "exportFSM failed")
	require.Nil(t, exp.state)
	require.Zero(t, exp.clock)
}

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

func TestStream_AnExportTheLeaderCannotReadIsRefused(t *testing.T) {
	store := newFaultStore()
	cl, cli := callerOf(t, "stream-refused-export", func(ds.Batching) ds.Batching { return store })
	require.NoError(t, cl.Set("k", []byte("v"), time.Second))
	store.failScan("/raft/stream-refused-export/data", errDisk)

	exp, err := cli.ExportFSM(idOf(cl), 0)
	require.ErrorContains(t, err, errDisk.Error())
	require.Nil(t, exp.state)
}

func TestStream_AConsistentReadFromAMemberThatCannotBarrierIsRefused(t *testing.T) {
	caller := newMockNode(t)
	a, b := joinedPair(t, "stream-barrier-refused", sameStore)
	connected(t, caller, a, b)
	require.NoError(t, onLeader(t, []Cluster{a, b}, func(l Cluster) error {
		return l.Set("k", []byte("v"), time.Second)
	}))

	removed, left := removeTheLeader(t, a, b)

	cliOf, err := newInternalClient(caller, "stream-barrier-refused", nil, quickRetry)
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

func TestStream_JoinVoterNamingNobodyJoinsTheCaller(t *testing.T) {
	a, b := joinedPair(t, "stream-join-caller", sameStore)

	fromB, err := newInternalClient(b.(*cluster).Node, "stream-join-caller", nil, quickRetry)
	require.NoError(t, err)
	defer fromB.Close()
	require.NoError(t, fromB.JoinVoter(t.Context(), peer.ID(""), 5*time.Second, formationOf(a), 0, idOf(a)))

	members, err := a.Members()
	require.NoError(t, err)
	ids := make([]peer.ID, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.ID)
	}
	require.ElementsMatch(t, []peer.ID{idOf(a), idOf(b)}, ids)
}

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

func TestStream_ExportFSMAskedOfEitherMemberIsTheGroupsState(t *testing.T) {
	caller := newMockNode(t)
	a, b := joinedPair(t, "stream-export-either", sameStore)
	connected(t, caller, a, b)
	require.NoError(t, onLeader(t, []Cluster{a, b}, func(l Cluster) error {
		return l.Set("k", []byte("v"), time.Second)
	}))

	cliOf, err := newInternalClient(caller, "stream-export-either", nil, quickRetry)
	require.NoError(t, err)
	defer cliOf.Close()

	for _, member := range []Cluster{a, b} {
		require.Eventually(t, func() bool {
			exp, err := cliOf.ExportFSM(idOf(member), 0)
			return err == nil && string(exp.state["k"].Value) == "v"
		}, leaderWait, 20*time.Millisecond, "asking %s for the group's state", idOf(member).ShortString())
	}
}

// AN ORPHAN'S EXPORT IS SERVED ONLY BY A MEMBER THAT KNOWS NO LEADER, never
// forwarded: the group taking it takes it outside the order two leaders merge by.
func TestStream_AnOrphansExportIsServedOnlyByAMemberThatKnowsNoLeader(t *testing.T) {
	caller := newMockNode(t)
	a, b := joinedPair(t, "stream-export-orphan", sameStore)
	connected(t, caller, a, b)
	require.NoError(t, onLeader(t, []Cluster{a, b}, func(l Cluster) error {
		return l.Set("k", []byte("v"), time.Second)
	}))

	cliOf, err := newInternalClient(caller, "stream-export-orphan", nil, quickRetry)
	require.NoError(t, err)
	defer cliOf.Close()

	for _, member := range []Cluster{a, b} {
		require.Eventually(t, func() bool { _, err := member.Leader(); return err == nil }, leaderWait, 20*time.Millisecond)
		_, err := cliOf.ExportOrphan(idOf(member))
		require.ErrorContains(t, err, ErrNotAnOrphan.Error(), "%s served an orphan's export", idOf(member).ShortString())
	}

	require.NoError(t, a.Close())
	require.Eventually(t, func() bool { _, err := b.Leader(); return err != nil }, leaderWait, 20*time.Millisecond)
	exp, err := cliOf.ExportOrphan(idOf(b))
	require.NoError(t, err)
	require.Equal(t, "v", string(exp.state["k"].Value))
	require.Equal(t, formationOf(b), exp.formation)
}

func TestStream_AJoinAskedOfAFollowerIsForwardedToItsLeader(t *testing.T) {
	const namespace = "stream-join-forwarded"
	caller := newMockNode(t)
	a, b := joinedPair(t, namespace, sameStore)
	connected(t, caller, a, b)
	cli, err := newInternalClient(caller, namespace, nil, quickRetry)
	require.NoError(t, err)
	defer cli.Close()

	leader := leaderOf(t, a, b)
	follower := a
	if leader == a {
		follower = b
	}
	joiner := strangerID(t)
	require.NoError(t, cli.JoinVoter(t.Context(), joiner, 5*time.Second, formationOf(follower), 0, idOf(follower)))
	members, err := leader.Members()
	require.NoError(t, err)
	ids := make([]peer.ID, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.ID)
	}
	require.ElementsMatch(t, []peer.ID{idOf(a), idOf(b), joiner}, ids)
}

func TestStream_AJoinAskedOfAFollowerBetweenLeadersWaitsForTheNext(t *testing.T) {
	const namespace = "stream-join-between-leaders"
	caller := newMockNode(t)
	group := joinedGroup(t, namespace, sameStore, 4)
	connected(t, caller, group...)
	cli, err := newInternalClient(caller, namespace, nil, quickRetry)
	require.NoError(t, err)
	defer cli.Close()

	// BETWEEN LEADERS FOR AS LONG AS THE TEST NEEDS: the leader and one more close,
	// and two of four cannot elect anyone, so the survivors stay leaderless until the
	// second comes back. A window that lasted only one election would race the poll.
	gone := leaderOf(t, group...)
	var back *cluster
	var survivors []Cluster
	for _, cl := range group {
		switch {
		case cl == gone:
		case back == nil:
			back = cl.(*cluster)
		default:
			survivors = append(survivors, cl)
		}
	}
	require.NoError(t, gone.Close())
	require.NoError(t, back.Close())
	var follower Cluster
	require.Eventually(t, func() bool {
		for _, cl := range survivors {
			if _, err := cl.Leader(); err != nil {
				follower = cl
				return true
			}
		}
		return false
	}, leaderWait, 10*time.Millisecond, "a survivor of a group that cannot elect still named a leader")

	joiner := strangerID(t)
	joined := make(chan error, 1)
	go func() {
		joined <- cli.JoinVoter(t.Context(), joiner, leaderWait, formationOf(follower), 0, idOf(follower))
	}()
	select {
	case err := <-joined:
		t.Fatalf("a join asked between leaders answered before there was one: %v", err)
	case <-time.After(500 * time.Millisecond):
	}

	again, err := New(storeNode{Node: back.Node, store: back.Node.Store()}, namespace,
		WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(2*time.Second))
	require.NoError(t, err)
	t.Cleanup(func() { again.Close() })
	require.NoError(t, <-joined, "the join never reached the next leader")

	members, err := leaderOf(t, append(survivors, again)...).Members()
	require.NoError(t, err)
	ids := make([]peer.ID, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.ID)
	}
	require.Contains(t, ids, joiner)
}

func TestStream_AJoinAskedOfAMemberLetInBeforeItsLogWaitsForTheLeader(t *testing.T) {
	const (
		namespace = "stream-join-before-log"
		firstLog  = "/raft/" + namespace + "/engine/log/00000000000000000001"
		vote      = "/raft/" + namespace + "/engine/hardstate"
		budget    = 300 * time.Millisecond
	)
	caller := newMockNode(t)
	group := joinedGroup(t, namespace, sameStore, 3)

	node := newMockNode(t)
	store := newFaultStore()
	store.failPut(firstLog, errNoSpace)
	store.failPut(vote, errNoSpace)
	require.NoError(t, taupeer.LinkAllPeers())
	founder := group[0].(*cluster).Host()
	require.NoError(t, node.Host().Connect(t.Context(), peer.AddrInfo{ID: founder.ID(), Addrs: founder.Addrs()}))
	late, err := New(storeNode{Node: node, store: store}, namespace,
		WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(2*time.Second))
	require.NoError(t, err)
	t.Cleanup(func() { late.Close() })
	require.Eventually(t, late.(*cluster).current().joined.Load, leaderWait, 10*time.Millisecond, "the group never let the late member in")
	members, err := late.Members()
	require.NoError(t, err)
	require.Empty(t, members, "the late member holds a configuration")

	connected(t, caller, group...)
	connected(t, caller, late)
	cli, err := newInternalClient(caller, namespace, nil, quickRetry)
	require.NoError(t, err)
	defer cli.Close()

	gone := leaderOf(t, group...)
	survivors := []Cluster{late}
	for _, cl := range group {
		if cl != gone {
			survivors = append(survivors, cl)
		}
	}
	require.NoError(t, gone.Close())
	require.Eventually(t, func() bool {
		_, err := late.Leader()
		return err != nil
	}, leaderWait, time.Millisecond, "the late member kept naming the closed leader")

	began := time.Now()
	require.ErrorIs(t, cli.JoinVoter(t.Context(), strangerID(t), budget, formationOf(late), 0, idOf(late)), ErrNoLeader)
	require.GreaterOrEqual(t, time.Since(began), budget, "a member the group let in refused a join at once")

	store.failPut(firstLog, nil)
	store.failPut(vote, nil)
	joiner := strangerID(t)
	require.NoError(t, cli.JoinVoter(t.Context(), joiner, leaderWait, formationOf(late), 0, idOf(late)))
	members, err = leaderOf(t, survivors...).Members()
	require.NoError(t, err)
	ids := make([]peer.ID, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.ID)
	}
	require.Contains(t, ids, joiner)
}

func TestStream_AJoinAskedOfAMemberAYieldTookOutOfItsGroupIsRefusedAtOnce(t *testing.T) {
	const (
		namespace = "stream-join-after-yield"
		budget    = time.Second
	)
	caller := newMockNode(t)
	a, b := joinedPair(t, namespace, sameStore)
	require.True(t, b.(*cluster).current().joined.Load(), "the group never let b in")
	require.NoError(t, a.Close())

	connected(t, caller, b)
	cli, err := newInternalClient(caller, namespace, nil, quickRetry)
	require.NoError(t, err)
	defer cli.Close()

	yielded := make(chan struct{})
	go func() {
		defer close(yielded)
		b.(*cluster).current().healer.Load().yieldAndRejoin(t.Context(), "", covering(b.(*cluster).current()))
	}()
	require.Eventually(t, func() bool {
		info, err := cli.ClusterInfo(idOf(b))
		return err == nil && info.MemberCount == 0
	}, leaderWait, 10*time.Millisecond, "b never came back in no group")

	began := time.Now()
	require.ErrorIs(t, cli.JoinVoter(t.Context(), strangerID(t), budget, formationOf(a), 0, idOf(b)), ErrNoLeader)
	require.Less(t, time.Since(began), budget, "a member in no group waited out a join's budget")
	<-yielded
}

func TestStream_AJoinWhoseLeaderStopsLeadingIsAddedByTheNext(t *testing.T) {
	const namespace = "stream-join-leader-steps-down"
	// No member loses its leader on its own: the test moves the lead.
	steady := TimeoutConfig{
		HeartbeatTimeout:   time.Second,
		ElectionTimeout:    time.Second,
		CommitTimeout:      50 * time.Millisecond,
		LeaderLeaseTimeout: 500 * time.Millisecond,
		SnapshotInterval:   time.Minute,
		SnapshotThreshold:  1000,
	}
	caller := newMockNode(t)
	var stores []*faultStore
	group := joinedGroup(t, namespace, func(ds.Batching) ds.Batching {
		store := newFaultStore()
		stores = append(stores, store)
		return store
	}, 3, WithTimeouts(steady))
	connected(t, caller, group...)
	cli, err := newInternalClient(caller, namespace, nil, quickRetry)
	require.NoError(t, err)
	defer cli.Close()

	leader := leaderOf(t, group...)
	var next Cluster
	for i, cl := range group {
		switch {
		case cl == leader:
		case next == nil:
			next = cl
		default:
			stores[i].failBatches(errNoSpace)
		}
	}

	joiner := strangerID(t)
	joined := make(chan error, 1)
	go func() { joined <- cli.JoinVoter(t.Context(), joiner, leaderWait, formationOf(leader), 0, idOf(leader)) }()
	require.Eventually(t, func() bool {
		members, err := leader.Members()
		return err == nil && len(members) == len(group)+1
	}, leaderWait, 5*time.Millisecond, "the leader never took the voter's change")

	require.NoError(t, leader.(*cluster).current().raft.TransferLeadership(idOf(next)))
	for _, store := range stores {
		store.failBatches(nil)
	}
	require.NoError(t, <-joined)
	members, err := leaderOf(t, group...).Members()
	require.NoError(t, err)
	ids := make([]peer.ID, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.ID)
	}
	require.Contains(t, ids, joiner)
}

func TestStream_AJoinForwardedToALeaderThatClosedReachesTheNext(t *testing.T) {
	const namespace = "stream-join-past-a-closed-leader"
	// A member names its leader until an election timeout passes without it, two
	// seconds here: long enough to ask it a join while it names one that closed.
	steady := TimeoutConfig{
		HeartbeatTimeout:   2 * time.Second,
		ElectionTimeout:    2 * time.Second,
		CommitTimeout:      50 * time.Millisecond,
		LeaderLeaseTimeout: time.Second,
		SnapshotInterval:   time.Minute,
		SnapshotThreshold:  1000,
	}
	caller := newMockNode(t)
	group := joinedGroup(t, namespace, sameStore, 4, WithTimeouts(steady))
	connected(t, caller, group...)
	cli, err := newInternalClient(caller, namespace, nil, quickRetry)
	require.NoError(t, err)
	defer cli.Close()

	gone := leaderOf(t, group...)
	var survivors []Cluster
	for _, cl := range group {
		if cl != gone {
			survivors = append(survivors, cl)
		}
	}
	asked := survivors[0]
	require.NoError(t, gone.Close())
	named, err := asked.Leader()
	require.NoError(t, err)
	require.Equal(t, idOf(gone), named, "the member stopped naming the closed leader before it was asked")

	joiner := strangerID(t)
	require.NoError(t, cli.JoinVoter(t.Context(), joiner, leaderWait, formationOf(asked), 0, idOf(asked)))
	members, err := leaderOf(t, survivors...).Members()
	require.NoError(t, err)
	ids := make([]peer.ID, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.ID)
	}
	require.Contains(t, ids, joiner)
}

func TestStream_AForwardFromTheLeaderItselfIsRefused(t *testing.T) {
	cl, _ := callerOf(t, "forward-from-the-leader", sameStore)
	s := &raftStreamService{cluster: cl.(*cluster)}
	_, err := forwardToLeader(t.Context(), s, "set", func(context.Context, raftv2connect.RaftServiceClient) (*connect.Response[raftv2.SetResponse], error) {
		t.Error("a leader forwarded to itself")
		return nil, nil
	})
	require.ErrorIs(t, err, ErrNotLeader)
}

// A FORWARD IN FLIGHT WHEN ITS LEADER CHANGES MAY HAVE LANDED, and says so rather
// than be sent again to the next.
func TestStream_AForwardInFlightWhenTheLeaderChangesIsLeadershipLost(t *testing.T) {
	// No member loses its leader on its own: the test moves the lead.
	steady := TimeoutConfig{
		HeartbeatTimeout:   time.Second,
		ElectionTimeout:    time.Second,
		CommitTimeout:      50 * time.Millisecond,
		LeaderLeaseTimeout: 500 * time.Millisecond,
		SnapshotInterval:   time.Minute,
		SnapshotThreshold:  1000,
	}
	group := joinedGroup(t, "forward-in-flight", sameStore, 2, WithTimeouts(steady))
	leader := leaderOf(t, group...)
	follower := group[0]
	if follower == leader {
		follower = group[1]
	}
	g := follower.(*cluster).current().raft
	observers := func() int {
		g.mu.Lock()
		defer g.mu.Unlock()
		return len(g.observers)
	}
	before := observers()

	s := &raftStreamService{cluster: follower.(*cluster)}
	sent := make(chan struct{})
	answered := make(chan error, 1)
	go func() {
		_, err := forwardToLeader(t.Context(), s, "set", func(ctx context.Context, _ raftv2connect.RaftServiceClient) (*connect.Response[raftv2.SetResponse], error) {
			close(sent)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		answered <- err
	}()
	select {
	case <-sent:
	case err := <-answered:
		t.Fatalf("the forward answered before it was sent: %v", err)
	}
	require.NoError(t, leader.(*cluster).current().raft.TransferLeadership(idOf(follower)))
	require.ErrorIs(t, <-answered, ErrLeadershipLost)
	require.Equal(t, before, observers(), "the forward kept watching the leader")
}

// A JOIN FROM A MEMBER OF ANOTHER FORMATION IS REFUSED: added, it would be a voter
// that takes none of the group's messages.
func TestStream_AJoinFromAnotherFormationIsRefused(t *testing.T) {
	cl, cli := callerOf(t, "join-elsewhere", sameStore)
	err := cli.JoinVoter(t.Context(), strangerID(t), time.Second, newFormation(), 0, idOf(cl))
	require.ErrorContains(t, err, ErrJoinerElsewhere.Error())
	members, err := cl.Members()
	require.NoError(t, err)
	require.Len(t, members, 1, "a joiner of another formation was added")
}

// quickRetry is how a test's own client waits for a member not answering yet: briefly,
// since most of these calls are meant to be refused.
var quickRetry = rpc.Retry{First: 5 * time.Millisecond, Max: 20 * time.Millisecond, For: 100 * time.Millisecond}

func TestClient_AMemberNotAnsweringYetIsAskedAgain(t *testing.T) {
	const namespace = "client-late-member"
	node, caller := newMockNode(t), newMockNode(t)
	require.NoError(t, taupeer.LinkAllPeers())
	require.NoError(t, caller.Host().Connect(t.Context(), peer.AddrInfo{ID: node.ID(), Addrs: node.Host().Addrs()}))

	cliOf, err := newInternalClient(caller, namespace, nil, rpc.Retry{First: 10 * time.Millisecond, Max: 50 * time.Millisecond, For: 10 * time.Second})
	require.NoError(t, err)
	defer cliOf.Close()

	started := make(chan Cluster, 1)
	time.AfterFunc(200*time.Millisecond, func() {
		member, err := New(storeNode{Node: node, store: node.Store()}, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
		if err == nil {
			started <- member
		}
		close(started)
	})
	_, err = cliOf.(*client).ClusterInfo(node.ID())
	require.NoError(t, err, "a call to a member that began answering a moment later was not asked again")
	if member, ok := <-started; ok {
		member.Close()
	}

	_, err = newInternalClient(caller, namespace, nil, quickRetry)
	require.NoError(t, err)
	require.Error(t, WithClientRetry(rpc.Retry{First: -time.Second})(&cluster{}))
	require.NoError(t, WithClientRetry(rpc.Retry{})(&cluster{}))
}
