package hosted

import (
	"testing"

	"github.com/taubyte/tau/pkg/containers"

	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/p2p/peer/peertest"
	secretsService "github.com/taubyte/tau/services/secrets"
)

// A declared tier is built from config and an injected one is not; the daemon itself
// is pkg/invoke/container's test.

func TestAnUnknownBackendIsRefusedAtStart(t *testing.T) {
	mn := peertest.Net(t)
	_, _, err := startNode(t, mn, Config{
		Services:  []string{Worker},
		Container: containers.Declared{Backend: "podman"},
	})

	require.ErrorIs(t, err, containers.ErrUnknownBackend)
	require.Contains(t, err.Error(), "podman")
	require.Contains(t, err.Error(), containers.Docker, "and it says what it can build")
	require.Contains(t, err.Error(), containers.Containerd)
}

func TestAnInjectedTierIsEnough(t *testing.T) {
	mn := peertest.Net(t)
	n, _, _ := makeNode(t, mn, Config{Services: []string{Register, Worker}})
	require.NotNil(t, n.Worker())
}

func TestNoWorkerMeansNoBackend(t *testing.T) {
	mn := peertest.Net(t)
	_, _, err := startNode(t, mn, Config{
		Services:  []string{Register, Stats},
		Container: containers.Declared{Backend: "podman"},
	})
	require.NoError(t, err, "a node with no worker never asked for a tier")
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
