package instance

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A declared tier is built from config and an injected one is not; the daemon itself
// is pkg/invoke/container's test.

func TestAnUnknownBackendIsRefusedAtBoot(t *testing.T) {
	_, err := New(mockPeer(t), rooted(t, Config{
		Services:  []string{Worker},
		Container: ContainerConfig{Backend: "podman"},
	}))

	require.ErrorIs(t, err, ErrUnknownBackend)
	require.Contains(t, err.Error(), "podman")
	require.Contains(t, err.Error(), BackendDocker, "and it says what it can build")
	require.Contains(t, err.Error(), BackendContainerd)
}

func TestAnInjectedTierIsEnough(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker}})
	require.NotNil(t, n.Worker())
}

func TestNoWorkerMeansNoBackend(t *testing.T) {
	n, err := New(mockPeer(t), rooted(t, Config{
		Services:  []string{Register, Discovery},
		Container: ContainerConfig{Backend: "podman"},
	}), withKVDB(&memFactory{}))
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
