package raft

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/hashicorp/raft"
	ds "github.com/ipfs/go-datastore"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/stretchr/testify/require"
	taupeer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/rpc"
	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2/raftv2connect"
)

func strangerID(t *testing.T) peer.ID {
	t.Helper()
	_, pub, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	id, err := peer.IDFromPublicKey(pub)
	require.NoError(t, err)
	return id
}

// The state machine holds the first write while ten more commit behind it, and
// the member is closed: raft drops the futures of logs it committed but had not
// applied.
func TestCluster_AWriteInFlightWhenTheRaftShutsDownIsAnswered(t *testing.T) {
	const (
		namespace = "write-across-shutdown"
		queued    = 10
	)
	node := newMockNode(t)
	held := newHeldStore(node.Store(), ds.NewKey(path.Join(RaftStoragePrefix, namespace, "data", "held")))
	defer held.letGo()
	c, err := New(storeNode{Node: node, store: held}, namespace,
		WithTimeouts(testTimeoutConfig()), WithForceBootstrap(), WithSnapshotRoot(t.TempDir()))
	require.NoError(t, err)
	waitLeader(t, c, 5*time.Second)

	r := c.(*cluster).current().raft
	answered := make(chan error, queued+1)
	go func() { answered <- c.Set("held", []byte("v"), time.Second) }()
	<-held.entered
	for i := range queued {
		commit := r.CommitIndex()
		go func() { answered <- c.Set(fmt.Sprint("queued-", i), []byte("v"), time.Second) }()
		require.Eventually(t, func() bool { return r.CommitIndex() > commit }, leaderWait, time.Millisecond, "write %d never committed", i)
	}

	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()
	require.Eventually(t, func() bool { return r.State() == raft.Shutdown }, leaderWait, time.Millisecond, "Close never shut the raft down")
	held.letGo()
	require.NoError(t, <-closed)

	deadline := time.After(leaderWait)
	for i := range queued + 1 {
		select {
		case <-answered:
		case <-deadline:
			t.Fatalf("%d of %d writes in flight at shutdown were never answered", queued+1-i, queued+1)
		}
	}
}

func TestCluster_AWriteTheStoreRefusesIsRefused(t *testing.T) {
	store := newFaultStore()
	cl, err := New(storeNode{Node: newMockNode(t), store: store}, "cluster-refused-write",
		WithTimeouts(testTimeoutConfig()), WithForceBootstrap(), WithSnapshotRoot(t.TempDir()))
	require.NoError(t, err)
	defer cl.Close()
	waitLeader(t, cl, 5*time.Second)
	store.failPut("/raft/cluster-refused-write/data/k", errNoSpace)

	require.ErrorIs(t, cl.Set("k", []byte("v"), time.Second), errNoSpace)
	require.ErrorIs(t, cl.Delete("k", time.Second), errNoSpace)
	require.ErrorIs(t, cl.Batch([]BatchOp{
		{Set: &SetCommand{Key: "other", Value: []byte("v")}},
		{Set: &SetCommand{Key: "k", Value: []byte("v")}},
	}, time.Second), errNoSpace)

	cmd, err := encodeSetCommand("k", []byte("v"))
	require.NoError(t, err)
	resp, err := cl.Apply(cmd, time.Second)
	require.NoError(t, err, "Apply reports the state machine's answer, not a failure to replicate")
	require.ErrorIs(t, resp.Error, errNoSpace)

	for _, key := range []string{"k", "other"} {
		_, found, err := cl.TryGet(key)
		require.NoError(t, err)
		require.False(t, found, "%q was written by a refused write", key)
	}
}

func TestCluster_AMemberLeftAloneInAPairAcknowledgesNoWrite(t *testing.T) {
	a, b := joinedPair(t, "cluster-left-alone", sameStore)

	alone := leaderOf(t, a, b)
	gone := a
	if alone == a {
		gone = b
	}
	require.NoError(t, gone.Close())

	cmd, err := encodeSetCommand("k", []byte("applied"))
	require.NoError(t, err)
	stranger, joiner := strangerID(t), strangerID(t)

	writes := map[string]func() error{
		"set":    func() error { return alone.Set("k", []byte("set"), 5*time.Second) },
		"delete": func() error { return alone.Delete("k", 5*time.Second) },
		"batch": func() error {
			return alone.Batch([]BatchOp{{Set: &SetCommand{Key: "k", Value: []byte("batch")}}}, 5*time.Second)
		},
		"apply": func() error {
			_, err := alone.Apply(cmd, 5*time.Second)
			return err
		},
		"barrier": func() error { return alone.Barrier(5 * time.Second) },
	}
	changes := map[string]func() error{
		"add voter":     func() error { return alone.AddVoter(stranger, 5*time.Second) },
		"remove server": func() error { return alone.RemoveServer(idOf(alone), 5*time.Second) },
		"join voter": func() error {
			_, err := alone.(*cluster).current().streamService.JoinVoter(context.Background(),
				connect.NewRequest(&raftv2.JoinVoterRequest{Peer: joiner.String(), TimeoutMs: 5000}))
			return err
		},
	}

	type answer struct {
		name       string
		membership bool
		err        error
	}
	answers := make(chan answer, len(writes)+len(changes))
	for name, write := range writes {
		go func() { answers <- answer{name: name, err: write()} }()
	}
	for name, change := range changes {
		go func() { answers <- answer{name: name, membership: true, err: change()} }()
	}
	for range len(writes) + len(changes) {
		got := <-answers
		require.Error(t, got.err, "%s was acknowledged by a member that cannot commit", got.name)
		if !got.membership {
			require.True(t, leadershipMoved(got.err), "%s failed as %v", got.name, got.err)
		}
	}

	require.False(t, alone.IsLeader())
	_, found, err := alone.TryGet("k")
	require.NoError(t, err)
	require.False(t, found, "a write nobody acknowledged was applied")
}

func TestNew_RefusesWhatItCannotRun(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(notADir, []byte("x"), 0o644))

	badTimeouts := testTimeoutConfig()
	badTimeouts.LeaderLeaseTimeout = 2 * badTimeouts.HeartbeatTimeout

	for _, tc := range []struct {
		name string
		opt  Option
		says string
	}{
		{"an empty snapshot dir", WithSnapshotDir(""), "snapshot dir cannot be empty"},
		{"an empty snapshot root", WithSnapshotRoot(""), "snapshot root cannot be empty"},
		{"a key shorter than AES-256 takes", WithEncryptionKey(make([]byte, 16)), "encryption key must be"},
		{"a key longer than AES-256 takes", WithEncryptionKey(make([]byte, 48)), "invalid key size 48"},
		{"a snapshot dir that is a file", WithSnapshotDir(notADir), "failed to create snapshot store"},
		{"a lease longer than the heartbeat", WithTimeouts(badTimeouts), "failed to create raft"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := newMockNode(t)
			cl, err := New(node, "new-refuses", WithForceBootstrap(), WithSnapshotRoot(t.TempDir()), tc.opt)
			require.ErrorContains(t, err, tc.says)
			require.Nil(t, cl)
			require.Empty(t, groupProtocols(node, "new-refuses"), "the refused New left its transport behind")
		})
	}
}

func groupProtocols(node taupeer.Node, namespace string) []protocol.ID {
	var served []protocol.ID
	for _, id := range node.Peer().Mux().Protocols() {
		if id == protocol.ID(Protocol(namespace)) || id == protocol.ID(TransportProtocol(namespace)) {
			served = append(served, id)
		}
	}
	return served
}

type writesSeen struct {
	ds.Batching
	writes atomic.Int32
}

func (s *writesSeen) Put(ctx context.Context, key ds.Key, value []byte) error {
	s.writes.Add(1)
	return s.Batching.Put(ctx, key, value)
}

func (s *writesSeen) Delete(ctx context.Context, key ds.Key) error {
	s.writes.Add(1)
	return s.Batching.Delete(ctx, key)
}

func (s *writesSeen) Batch(ctx context.Context) (ds.Batch, error) {
	s.writes.Add(1)
	return s.Batching.Batch(ctx)
}

// gostream.Listen replaces a handler already on its protocol, so the transport
// refuses the protocol before anything is built, and the group keeps it.
func TestNew_ANamespaceTheNodeAlreadyRunsIsRefused(t *testing.T) {
	const namespace = "namespace-already-run"
	// A leadership transfer has one election timeout to finish in.
	timeouts := TimeoutConfig{
		HeartbeatTimeout:   time.Second,
		ElectionTimeout:    time.Second,
		CommitTimeout:      50 * time.Millisecond,
		LeaderLeaseTimeout: 500 * time.Millisecond,
		SnapshotInterval:   time.Minute,
		SnapshotThreshold:  1000,
	}
	nodeA, nodeB := newMockNode(t), newMockNode(t)
	require.NoError(t, taupeer.LinkAllPeers())

	a, err := New(nodeA, namespace, WithTimeouts(timeouts), WithForceBootstrap(), WithSnapshotRoot(t.TempDir()))
	require.NoError(t, err)
	defer a.Close()
	waitLeader(t, a, 10*time.Second)

	store := &writesSeen{Batching: nodeA.Store()}
	second, err := New(storeNode{Node: nodeA, store: store}, namespace,
		WithTimeouts(timeouts), WithForceBootstrap(), WithSnapshotRoot(t.TempDir()))
	require.ErrorIs(t, err, rpc.ErrProtocolTaken)
	require.Nil(t, second)
	require.Zero(t, store.writes.Load(), "a second raft ran on the group's store")

	require.True(t, a.IsLeader())
	require.NoError(t, a.Set("before", []byte("v"), time.Second))
	value, found := a.Get("before")
	require.True(t, found)
	require.Equal(t, []byte("v"), value)

	require.NoError(t, nodeB.Peer().Connect(t.Context(), peer.AddrInfo{ID: nodeA.ID(), Addrs: nodeA.Peer().Addrs()}))
	b, err := New(nodeB, namespace, WithTimeouts(timeouts), WithBootstrapTimeout(5*time.Second), WithSnapshotRoot(t.TempDir()))
	require.NoError(t, err)
	defer b.Close()
	// a commits b's joining before b has heard of it, and a b that has not is
	// sent its turn to lead with nobody to ask for a vote.
	require.Eventually(t, func() bool {
		ofA, errA := a.Members()
		ofB, errB := b.Members()
		return errA == nil && errB == nil && len(ofA) == 2 && len(ofB) == 2
	}, 10*time.Second, 20*time.Millisecond, "b never joined a")

	require.NoError(t, a.TransferLeadership())
	require.Eventually(t, b.IsLeader, 10*time.Second, 20*time.Millisecond, "b never reached the group's transport for a's vote")
	require.NoError(t, b.Set("after", []byte("v"), time.Second))
	require.Eventually(t, func() bool {
		value, found := a.Get("after")
		return found && string(value) == "v"
	}, 10*time.Second, 20*time.Millisecond, "a write b leads never reached a")
}

func TestNew_AFailedNewLeavesNothingRunning(t *testing.T) {
	t.Run("a stream protocol another service holds", func(t *testing.T) {
		const namespace = "new-stream-protocol-taken"
		node := newMockNode(t)
		route, handler := raftv2connect.NewRaftServiceHandler(raftv2connect.UnimplementedRaftServiceHandler{})
		mux := http.NewServeMux()
		mux.Handle(route, handler)
		other, err := rpc.Listen(t.Context(), node.Peer(), protocol.ID(Protocol(namespace)), mux)
		require.NoError(t, err)

		cl, err := New(node, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap(), WithSnapshotRoot(t.TempDir()))
		require.ErrorIs(t, err, rpc.ErrProtocolTaken)
		require.Nil(t, cl)
		require.Equal(t, []protocol.ID{protocol.ID(Protocol(namespace))}, groupProtocols(node, namespace),
			"the refused New left its transport behind")

		require.NoError(t, other.Close())
		cl, err = New(node, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap(), WithSnapshotRoot(t.TempDir()))
		require.NoError(t, err)
		defer cl.Close()
		waitLeader(t, cl, 5*time.Second)
	})

	t.Run("a bootstrap the store refuses", func(t *testing.T) {
		const namespace = "new-bootstrap-refused"
		store := newFaultStore()
		store.failPut(fmt.Sprintf("%s/%020d", path.Join(RaftStoragePrefix, namespace, "log"), 1), errNoSpace)
		node := newMockNode(t)

		cl, err := New(storeNode{Node: node, store: store}, namespace,
			WithTimeouts(testTimeoutConfig()), WithForceBootstrap(), WithSnapshotRoot(t.TempDir()))
		require.ErrorContains(t, err, errNoSpace.Error())
		require.Nil(t, cl)
		require.Empty(t, groupProtocols(node, namespace), "the refused New left its stream service or transport behind")
	})
}

func TestCluster_CloseReleasesAWaitForALeader(t *testing.T) {
	a, b := joinedPair(t, "close-releases-wait", sameStore)
	removed, _ := removeTheLeader(t, a, b)
	require.Eventually(t, func() bool {
		_, err := removed.Leader()
		return errors.Is(err, ErrNoLeader)
	}, leaderWait, 10*time.Millisecond, "the removed member kept naming a leader")

	waited := make(chan error, 1)
	go func() { waited <- removed.WaitForLeader(context.Background()) }()
	select {
	case err := <-waited:
		t.Fatalf("a member with no leader to wait for stopped waiting: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	require.NoError(t, removed.Close())
	select {
	case err := <-waited:
		require.ErrorIs(t, err, ErrShutdown)
	case <-time.After(leaderWait):
		t.Fatal("Close did not release the wait")
	}
}

func TestCluster_BatchAndMembershipRefuseWhatTheyCannotDo(t *testing.T) {
	a, b := joinedPair(t, "cluster-refusals", sameStore)
	removed, left := removeTheLeader(t, a, b)

	require.ErrorIs(t, left.Batch([]BatchOp{
		{Set: &SetCommand{Key: "k", Value: []byte("v")}},
		{},
	}, time.Second), ErrInvalidCommand)
	_, found, err := left.TryGet("k")
	require.NoError(t, err)
	require.False(t, found, "a refused batch wrote part of itself")

	require.ErrorIs(t, removed.Set("k", []byte("v"), time.Second), ErrNotLeader)
	require.ErrorIs(t, removed.Delete("k", time.Second), ErrNotLeader)
	require.ErrorIs(t, removed.Batch([]BatchOp{{Delete: &DeleteCommand{Key: "k"}}}, time.Second), ErrNotLeader)

	require.NoError(t, removed.Close())
	require.ErrorIs(t, removed.Batch([]BatchOp{{Delete: &DeleteCommand{Key: "k"}}}, time.Second), ErrShutdown)
	require.ErrorIs(t, removed.AddVoter(idOf(left), time.Second), ErrShutdown)
}

// A leader alone commits a second voter on its own say, then needs a voter
// nobody can reach: the third voter's change stays uncommitted, raft holds the
// next change back, and the leader steps down at its next lease check. A change
// asked for meanwhile passes the check, waits in raft and is refused once the
// member follows.
func TestCluster_AMembershipChangeThatLosesTheLeadIsNotLeader(t *testing.T) {
	lease := time.Second
	cl, err := New(newMockNode(t), "membership-loses-the-lead", WithTimeouts(TimeoutConfig{
		HeartbeatTimeout:   lease,
		ElectionTimeout:    lease,
		CommitTimeout:      50 * time.Millisecond,
		LeaderLeaseTimeout: lease,
		SnapshotInterval:   time.Minute,
		SnapshotThreshold:  1000,
	}), WithForceBootstrap(), WithSnapshotRoot(t.TempDir()))
	require.NoError(t, err)
	defer cl.Close()
	waitLeader(t, cl, 10*time.Second)

	unreachable, third, joiner := strangerID(t), strangerID(t), strangerID(t)
	require.NoError(t, cl.AddVoter(unreachable, 10*time.Second))
	uncommitted := make(chan error, 1)
	go func() { uncommitted <- cl.AddVoter(third, 10*time.Second) }()
	require.Eventually(t, func() bool {
		members, err := cl.Members()
		return err == nil && len(members) == 3
	}, 5*time.Second, 5*time.Millisecond, "the change adding the third voter never reached the configuration")
	require.True(t, cl.IsLeader(), "the lead was lost before the changes were asked for")

	changes := map[string]func() error{
		"add voter":     func() error { return cl.AddVoter(joiner, 10*time.Second) },
		"remove server": func() error { return cl.RemoveServer(unreachable, 10*time.Second) },
	}
	type answer struct {
		name string
		err  error
	}
	answers := make(chan answer, len(changes))
	for name, change := range changes {
		go func() { answers <- answer{name: name, err: change()} }()
	}
	for range changes {
		got := <-answers
		require.ErrorIs(t, got.err, ErrNotLeader, "%s", got.name)
	}
	require.Error(t, <-uncommitted, "a change that never committed was acknowledged")

	require.ErrorIs(t, cl.TransferLeadership(), ErrNotLeader)
}
