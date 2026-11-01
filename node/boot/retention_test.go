package boot

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
	fleetdoc "github.com/taubyte/tau/pkg/fleet"
)

func TestTheFleetDocumentIsReadFromTheNodesRegister(t *testing.T) {
	var running atomic.Pointer[node.Node]
	src := fleetDocument(&running)
	_, _, err := src(t.Context())
	require.ErrorIs(t, err, errNoFleetSource)

	f := admit(t, File{
		Node:   node.Config{Services: []string{node.Register}},
		Root:   t.TempDir(),
		Listen: []string{"/ip4/127.0.0.1/tcp/0"},
	})
	r, err := Start(t.Context(), f)
	require.NoError(t, err)
	defer r.Close()
	running.Store(r.Node)

	doc, published, err := src(t.Context())
	require.NoError(t, err)
	require.False(t, published)
	require.Nil(t, doc)

	want, err := fleetdoc.Encode(fleetdoc.Fleet{Retention: fleetdoc.Duration(72 * time.Hour)})
	require.NoError(t, err)
	_, err = r.Node.Register().Local().PublishFleet(t.Context(), 1, want)
	require.NoError(t, err)

	doc, published, err = src(t.Context())
	require.NoError(t, err)
	require.True(t, published)
	got, err := fleetdoc.Decode(doc)
	require.NoError(t, err)
	require.Equal(t, 72*time.Hour, time.Duration(got.Retention))
}

func TestANodeWithNoRegisterHasNoFleetSource(t *testing.T) {
	f := admit(t, File{
		Node:   node.Config{Services: []string{node.Discovery}},
		Root:   t.TempDir(),
		Listen: []string{"/ip4/127.0.0.1/tcp/0"},
	})
	r, err := Start(t.Context(), f)
	require.NoError(t, err)
	defer r.Close()
	var running atomic.Pointer[node.Node]
	running.Store(r.Node)
	_, _, err = fleetDocument(&running)(t.Context())
	require.ErrorIs(t, err, errNoFleetSource)
}
