package mock_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/peer/peertest"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/services/cm/mock"
	"github.com/taubyte/tau/services/cm/settings"
)

type node struct {
	peer.Node
	*service.Served
}

func (n node) Peer() peer.Node          { return n.Node }
func (node) Running() []service.Running { return nil }
func (node) Config(any) error           { return nil }
func (node) Root() string               { return "" }
func (node) Cloud() string              { return "mock-test" }
func (node) Region() string             { return "" }
func (node) Now() time.Time             { return time.Now() }

func TestTheMockAnswersWhatItWasToldAndChangesWhenTold(t *testing.T) {
	cm := mock.New(settings.Defaults())
	got, err := cm.Settings(t.Context())
	require.NoError(t, err)
	require.Equal(t, settings.Defaults(), got)

	changed := settings.Defaults()
	changed.Replication = 5
	cm.Set(changed)
	got, err = cm.Settings(t.Context())
	require.NoError(t, err)
	require.Equal(t, 5, got.Replication)

	cm.Fail(errors.New("gone"))
	_, err = cm.Settings(t.Context())
	require.ErrorContains(t, err, "gone")
	cm.Fail(nil)
	_, err = cm.Settings(t.Context())
	require.NoError(t, err)
}

func TestTheMockListensOnCmsProtocolUntilClosed(t *testing.T) {
	mn := peertest.Net(t)
	p := mn.Mock(t.Context())
	t.Cleanup(p.Close)
	cm := mock.New(settings.Defaults())
	require.NoError(t, cm.Close(), "closing one never started")
	n := node{Node: p, Served: service.NewServed(p.ID(), nil, rpc.Retry{For: 50 * time.Millisecond})}
	require.NoError(t, cm.Start(t.Context(), n))
	require.Contains(t, p.Host().Mux().Protocols(), settings.Protocol)
	served, ok := n.Local(settings.Protocol)
	require.True(t, ok, "the node it started on does not serve cm's protocol with it")
	require.Same(t, cm, served)
	require.NoError(t, cm.Close())
}
