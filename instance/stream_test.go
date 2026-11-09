package instance

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	gateway "github.com/taubyte/tau/services/gateway"
	workerHosted "github.com/taubyte/tau/services/worker/hosted"
	"github.com/taubyte/tau/services/worker/stream"
)

// Only the wiring: which gossip and cloud the bus was built over, and that the node
// has one, which a workload's publish and a browser's attach both reach.

func TestTheNodeExposesTheBusTopic(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker, Stats}})
	require.Equal(t, stream.Topic(n.cfg.Cloud, "h"), n.BusTopic("h"))
}

func TestOneBusIsGivenToBothConsumers(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker, Stats, Gateway}})
	require.NotNil(t, n.bus)

	host := &added{node: n}
	held, ok := service.Get[workerHosted.Bus](host)
	require.True(t, ok, "the worker is given the node's bus")
	require.Same(t, n.bus, held.Bus())
	_, ok = service.Get[gateway.Buses](host)
	require.True(t, ok, "and the gateway attaches browsers to it")
}
