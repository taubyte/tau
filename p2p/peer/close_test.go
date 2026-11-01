package peer

import (
	"errors"
	"os"
	"testing"

	"github.com/ipfs/go-datastore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/keypair"
)

var (
	errStoreClose   = errors.New("store refused to close")
	errPeeringStop  = errors.New("peering refused to stop")
	localListenAddr = []string{"/ip4/127.0.0.1/tcp/0"}
)

// storeThatFailsToClose closes the store it wraps and reports a failure anyway.
type storeThatFailsToClose struct {
	datastore.Batching
	closed bool
}

func (s *storeThatFailsToClose) Close() error {
	s.closed = true
	s.Batching.Close()
	return errStoreClose
}

type peeringThatFailsToStop struct {
	PeeringService
}

func (p peeringThatFailsToStop) Stop() error {
	p.PeeringService.Stop()
	return errPeeringStop
}

// Made without a repository, the peer makes one in TMPDIR.
func ephemeral(t *testing.T) *node {
	t.Helper()
	n, err := New(t.Context(), nil, keypair.NewRaw(), nil, localListenAddr, nil, true, false)
	require.NoError(t, err)
	p := n.(*node)
	require.True(t, p.ephemeral_repo_path)
	require.DirExists(t, p.repo_path)
	return p
}

func TestCloseRemovesTheTemporaryRepositoryWhenTheStoreFailsToClose(t *testing.T) {
	p := ephemeral(t)
	store := &storeThatFailsToClose{Batching: p.store}
	p.store = store

	p.Close()

	assert.True(t, store.closed)
	assert.NoDirExists(t, p.repo_path)
}

func TestCleanupGoesOnPastAFailingStepAndReportsEveryFailure(t *testing.T) {
	p := ephemeral(t)
	store := &storeThatFailsToClose{Batching: p.store}
	p.store = store
	p.peering = peeringThatFailsToStop{p.peering}

	err := p.cleanup()

	require.ErrorIs(t, err, errPeeringStop)
	require.ErrorIs(t, err, errStoreClose)
	assert.True(t, store.closed, "a failed step must not stop the store from closing")
	assert.NoDirExists(t, p.repo_path)
	assert.Error(t, p.Context().Err())
}

func TestAPeerThatFailsToStartLeavesNoRepositoryBehind(t *testing.T) {
	for _, c := range []struct {
		name   string
		key    []byte
		listen []string
	}{
		{"a key that cannot be read", []byte("not a key"), localListenAddr},
		{"an address that cannot be listened on", keypair.NewRaw(), []string{"not a multiaddr"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)

			_, err := New(t.Context(), nil, c.key, nil, c.listen, nil, true, false)
			require.Error(t, err)

			left, err := os.ReadDir(tmp)
			require.NoError(t, err)
			assert.Empty(t, left)
		})
	}
}
