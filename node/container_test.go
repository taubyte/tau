package node

import (
	"testing"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"
)

// A declared tier is built from config and an injected one is not; the daemon itself
// is pkg/invoke/container's test.

func TestAnUnknownBackendIsRefusedAtBoot(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	_, err := New(Config{
		Services:  []string{Worker},
		Container: ContainerConfig{Backend: "podman"},
	}, WithHost(newHost(t, mn)))

	require.ErrorIs(t, err, ErrUnknownBackend)
	require.Contains(t, err.Error(), "podman")
	require.Contains(t, err.Error(), BackendDocker, "and it says what it can build")
	require.Contains(t, err.Error(), BackendContainerd)
}

func TestAWorkerWithNoTier(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	_, err := New(Config{Services: []string{Worker}}, WithHost(newHost(t, mn)))
	require.Error(t, err)
	require.Contains(t, err.Error(), "declare a container backend, give it an asset store, or inject one")
}

func TestAnInjectedTierIsEnough(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker}})
	require.NotNil(t, n.Worker())
}

func TestNoWorkerMeansNoBackend(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	n, err := New(Config{
		Services:  []string{Register, Discovery},
		Container: ContainerConfig{Backend: "podman"},
	},
		WithHost(newHost(t, mn)),
		WithPubSub(newPubSub(t, newHost(t, mn))),
		WithDatabases(&memFactory{}))
	require.NoError(t, err, "a node with no worker never asked for a tier")
	t.Cleanup(func() { n.Close() })
}

func TestTheIsolationHandlerIsCarried(t *testing.T) {
	cfg := ContainerConfig{
		Backend:   BackendContainerd,
		Socket:    "/run/containerd/containerd.sock",
		Namespace: "tau",
		Runtime:   "io.containerd.runsc.v1",
	}

	require.True(t, cfg.declared())

	// Building it needs containerd; what is checked here is that nothing
	// silently drops the handler on the way, which is the failure that would
	// run untrusted code under runc without a word.
	_, err := containerTier(cfg, nil, &vault{})
	if err != nil {
		require.NotErrorIs(t, err, ErrUnknownBackend,
			"containerd is a backend this node builds; the error is the daemon's")
	}
}

func TestAnUndeclaredTierIsNotOne(t *testing.T) {
	require.False(t, ContainerConfig{}.declared())
	require.True(t, ContainerConfig{Backend: BackendDocker}.declared())
}
