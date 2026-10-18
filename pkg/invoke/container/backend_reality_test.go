//go:build docker_integration

package container

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/containers/backends/docker"
	"github.com/taubyte/tau/pkg/containers/core"
)

// backend_reality_test asks the one question every other test in this package
// assumes the answer to: DOES THE CONFIG THIS TIER BUILDS CREATE A CONTAINER ON
// A REAL DAEMON?
//
// Everything else here runs against fakeBackend, which accepts whatever it is
// handed. A fake cannot refuse a combination a daemon refuses, so a tier that
// asks for something impossible looks perfectly healthy all the way to
// production — which is exactly the shape of failure this file exists to catch.
//
// It is tagged because it needs a daemon. Run it where one is:
//
//	go test -tags docker_integration ./pkg/invoke/container/
func TestTheConfigThisTierBuildsIsAcceptedByDocker(t *testing.T) {
	backend, err := docker.New(core.DockerConfig{})
	require.NoError(t, err, "no docker daemon to ask")

	// EXACTLY WHAT THE TIER SENDS, built by the tier's own function rather than
	// restated here — a copy would drift and then agree with itself.
	r := &Runtime{}
	cfg := r.config(Image{Ref: "docker.io/library/busybox:latest"}, nil, 18080, "", nil)

	require.NotNil(t, cfg.Network)
	t.Logf("tier asks for: mode=%q restrict-egress=%v port-mappings=%d",
		cfg.Network.Mode, cfg.Network.RestrictEgress, len(cfg.Network.PortMappings))

	// The image is the test's own business; what is under test is the network
	// configuration, and a missing image would refuse the create for a reason
	// that has nothing to do with it.
	image := backend.Image(cfg.Image)
	if !image.Exists(context.Background()) {
		require.NoError(t, image.Pull(context.Background()), "pulling the test image")
	}

	id, err := backend.Create(context.Background(), cfg)
	if err == nil {
		t.Cleanup(func() { backend.Remove(context.Background(), id) })
	}
	require.NoError(t, err, "the daemon refused the configuration this tier always sends")
}
