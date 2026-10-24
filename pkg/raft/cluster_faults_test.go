package raft

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
)

// strangerID is the id of a peer no group has heard of.
func strangerID(t *testing.T) peer.ID {
	t.Helper()
	_, pub, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	id, err := peer.IDFromPublicKey(pub)
	require.NoError(t, err)
	return id
}

// TestCluster_AWriteTheStoreRefusesIsRefused: a write raft committed but the
// state machine could not apply comes back with the store's reason from every
// write path, and Apply hands it over in the response.
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

// TestCluster_AMemberLeftAloneInAPairAcknowledgesNoWrite: a group of two
// commits nothing with one member gone. Every write the other is asked for,
// whether it still leads when asked or has already stepped down, fails as a
// leadership move, and none of it is ever applied. Membership changes that
// still need the gone member fail too: adding a third, directly or for a peer
// asking to join, or the one left removing itself.
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
			_, err := alone.(*cluster).streamService.JoinVoter(context.Background(),
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

// TestNew_RefusesWhatItCannotRun: New fails, and returns no cluster, for an
// option it cannot honour and for a configuration raft will not start with.
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
			cl, err := New(newMockNode(t), "new-refuses", WithForceBootstrap(), WithSnapshotRoot(t.TempDir()), tc.opt)
			require.ErrorContains(t, err, tc.says)
			require.Nil(t, cl)
		})
	}
}

// TestCluster_CloseReleasesAWaitForALeader: a member that removed itself hears
// from no leader again, so a caller waiting on it for one waits until it is
// closed, and is then told it is shut down.
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

// TestCluster_BatchAndMembershipRefuseWhatTheyCannotDo: a batch member is a
// set or a delete, so one naming neither refuses the whole batch on the
// leader; a member that cannot lead refuses deletes and batches as
// ErrNotLeader; and a closed member refuses batches and new voters as shut
// down.
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

	require.ErrorIs(t, removed.Delete("k", time.Second), ErrNotLeader)
	require.ErrorIs(t, removed.Batch([]BatchOp{{Delete: &DeleteCommand{Key: "k"}}}, time.Second), ErrNotLeader)

	require.NoError(t, removed.Close())
	require.ErrorIs(t, removed.Batch([]BatchOp{{Delete: &DeleteCommand{Key: "k"}}}, time.Second), ErrShutdown)
	require.ErrorIs(t, removed.AddVoter(idOf(left), time.Second), ErrShutdown)
}
