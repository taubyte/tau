//go:build linux && containerd_integration

package container

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/containers/backends/containerd"
	"github.com/taubyte/tau/pkg/containers/core"
)

// The containerd half of backend_reality_test.go's question, and it had its own
// answer: containerd takes host networking and used to refuse any port mapping
// beside it, so the tier's config was rejected here for a different reason than
// docker rejected it for. Both are fixed, and both are only provable against a
// real daemon.
//
//	go test -tags "linux containerd_integration" ./pkg/invoke/container/
func TestTheConfigThisTierBuildsIsAcceptedByContainerd(t *testing.T) {
	backend, err := containerd.New(core.ContainerdConfig{})
	require.NoError(t, err, "no containerd to ask")

	r := &Runtime{}
	cfg := r.config(Image{Ref: "docker.io/library/busybox:latest"}, nil, 18081, "", nil)

	require.NotNil(t, cfg.Network)
	t.Logf("tier asks for: mode=%q restrict-egress=%v port-mappings=%d",
		cfg.Network.Mode, cfg.Network.RestrictEgress, len(cfg.Network.PortMappings))

	image := backend.Image(cfg.Image)
	if !image.Exists(context.Background()) {
		require.NoError(t, image.Pull(context.Background()), "pulling the test image")
	}

	id, err := backend.Create(context.Background(), cfg)
	if err == nil {
		t.Cleanup(func() { backend.Remove(context.Background(), id) })
	}
	require.NoError(t, err, "containerd refused the configuration this tier always sends")
}
