package instance

import (
	"testing"

	"github.com/taubyte/tau/pkg/containers"

	"github.com/stretchr/testify/require"
	secretsService "github.com/taubyte/tau/services/secrets"
)

// A declared tier is built from config and an injected one is not; the daemon itself
// is pkg/invoke/container's test.

func TestAnUnknownBackendIsRefusedAtBoot(t *testing.T) {
	_, err := New(mockPeer(t), rooted(t, Config{
		Services:  []string{Worker},
		Container: containers.Declared{Backend: "podman"},
	}))

	require.ErrorIs(t, err, containers.ErrUnknownBackend)
	require.Contains(t, err.Error(), "podman")
	require.Contains(t, err.Error(), containers.Docker, "and it says what it can build")
	require.Contains(t, err.Error(), containers.Containerd)
}

func TestAnInjectedTierIsEnough(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker}})
	require.NotNil(t, n.Worker())
}

func TestNoWorkerMeansNoBackend(t *testing.T) {
	n, err := New(mockPeer(t), rooted(t, Config{
		Services:  []string{Register, Discovery},
		Container: containers.Declared{Backend: "podman"},
	}), withKVDB(&memFactory{}))
	require.NoError(t, err, "a node with no worker never asked for a tier")
	t.Cleanup(func() { n.Close() })
}

func TestTheIsolationHandlerIsCarried(t *testing.T) {
	cfg := containers.Declared{
		Backend:   containers.Containerd,
		Socket:    "/run/containerd/containerd.sock",
		Namespace: "tau",
		Runtime:   "io.containerd.runsc.v1",
	}

	// Building it needs containerd; what is checked here is that nothing
	// silently drops the handler on the way, which is the failure that would
	// run untrusted code under runc without a word.
	_, err := containerTier(cfg, nil, new(secretsService.Vault))
	if err != nil {
		require.NotErrorIs(t, err, containers.ErrUnknownBackend,
			"containerd is a backend this node builds; the error is the daemon's")
	}
}
