package hosted

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	taupeer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/peer/peertest"
	"github.com/taubyte/tau/pkg/invoke"
	pkgregister "github.com/taubyte/tau/pkg/register"
	primitivesService "github.com/taubyte/tau/services/primitives"
	"github.com/taubyte/tau/services/worker"
)

// A send and an append reach the primitives service and land in its namespace. That
// a guest's call arrives here at all is a dream test's.

const (
	primJobsID  = "QmVLDAzEjEbfBPuoyLPWnPMhKp8ceDXwGdmMKY5W3nZWfG"
	primAuditID = "QmZ4gCbmYaFZ4dWJmvNBQKvNAWMYtHqQTHRDprHmxPh6Zk"
)

// withPrimitives is a project declaring one of each type; the service reads their
// settings from it.
func withPrimitives() []byte {
	return []byte(`{
		"object": {
			"id": "` + projectID + `",
			"name": "shop",
			"sync": {
				"` + primJobsID + `": {
					"name": "jobs", "type": "queue",
					"queue": {"consensus": "raft", "visibility_timeout": 60000000000}
				},
				"` + primAuditID + `": {
					"name": "audit", "type": "log",
					"log": {"consensus": "raft"}
				}
			}
		},
		"indexes": {
			"projects/` + projectID + `/sync/jobs/links": [
				"branches/main/projects/` + projectID + `/sync/` + primJobsID + `"],
			"projects/` + projectID + `/sync/audit/links": [
				"branches/main/projects/` + projectID + `/sync/` + primAuditID + `"]
		}
	}`)
}

// handleOf is what a grant carries: the deployment and the resource id, and
// nothing else.
func handleOf(id string) invoke.Grant {
	return invoke.Grant{Kind: "sync", Label: id, Handle: "shop/prod/" + id}
}

// holdingSync is a node that runs the service, with the project published and the
// group formed — the two things every send below rests on.
func holdingSync(t *testing.T, mn *taupeer.Mocknet, cfg Config) *node {
	t.Helper()

	n, _, _ := makeNode(t, mn, cfg)

	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: withPrimitives(),
	})
	require.NoError(t, err)

	// THE GROUP FORMS OFF THE REQUEST PATH, so a test that called straight into
	// it would be testing ErrForming. The mock is instant; waiting for it is
	// what makes the assertions below deterministic rather than fast.
	require.NoError(t, n.Primitives().WaitForLeader(t.Context()))
	return n
}

func TestASendThroughTheNodeLandsInThePrimitivesNamespace(t *testing.T) {
	mn := peertest.Net(t)
	n := holdingSync(t, mn, Config{Services: []string{Register, Worker, Stats, Primitives}})
	jobs := handleOf(primJobsID)

	require.NoError(t, n.syncs.Send(t.Context(), jobs, "order-1", []byte("ship it")))

	depth, err := n.Primitives().Local().Depth(t.Context(), jobs)
	require.NoError(t, err)
	require.Equal(t, uint64(1), depth.Ready, "the service does not hold the item")
}

func TestQueueDepthAnswersFromTheService(t *testing.T) {
	mn := peertest.Net(t)
	n := holdingSync(t, mn, Config{Services: []string{Register, Worker, Stats, Primitives}})
	jobs := handleOf(primJobsID)

	require.NoError(t, n.syncs.Send(t.Context(), jobs, "order-1", []byte("ship it")))

	ready, claimed, ok := n.QueueDepth(jobs.Handle)
	require.True(t, ok)
	require.Equal(t, 1, ready)
	require.Zero(t, claimed)
}

func TestASendToAQueueNobodyDeclaredIsRefused(t *testing.T) {
	mn := peertest.Net(t)
	n := holdingSync(t, mn, Config{Services: []string{Register, Worker, Stats, Primitives}})

	err := n.syncs.Send(t.Context(), handleOf("QmNothingDeclaresThis"), "1", []byte("x"))
	require.ErrorIs(t, err, primitivesService.ErrNotDeclared)
}

func TestALogAppendedThroughTheNodeIsReadBackFromTheService(t *testing.T) {
	mn := peertest.Net(t)
	n := holdingSync(t, mn, Config{Services: []string{Register, Worker, Stats, Primitives}})
	audit := handleOf(primAuditID)

	cursor, err := n.syncs.Append(t.Context(), audit, []byte("first"))
	require.NoError(t, err)
	require.NotEmpty(t, cursor)

	_, err = n.syncs.Append(t.Context(), audit, []byte("second"))
	require.NoError(t, err)

	read, err := n.Primitives().Local().Read(t.Context(), audit, "", 10)
	require.NoError(t, err)
	require.Len(t, read, 2)
	require.Equal(t, "first", string(read[0].Data))
	require.Equal(t, "second", string(read[1].Data))
}

func TestTheClockFiresThroughTheWorkerBesideIt(t *testing.T) {
	mn := peertest.Net(t)
	n := holdingSync(t, mn, Config{Services: []string{Register, Worker, Primitives}})
	w, ok := service.Local[primitivesService.Workloads](n.host.as(Primitives), worker.Protocol)
	require.True(t, ok, "a worker offers itself to the clock beside it")
	require.Empty(t, w.Schedules(t.Context()), "nothing deployed, nothing scheduled")

	without, _, _ := makeNode(t, mn, Config{Services: []string{Register, Primitives}})
	_, ok = service.Local[primitivesService.Workloads](without.host.as(Primitives), worker.Protocol)
	require.False(t, ok, "a node with no worker runs no clock")
	require.Empty(t, workloads{without.Service}.Schedules(t.Context()))
	_, err := workloads{without.Service}.Fire(t.Context(), primitivesService.Target{Project: "shop", Environment: "prod", Kind: "function", Name: "checkout"}, &invoke.Request{Method: "POST", Path: "/"})
	require.ErrorIs(t, err, ErrNoPrimitives, "and fires nothing")
}

func TestAFireRunsOnTheWorkerItCanReach(t *testing.T) {
	mn := peertest.Net(t)
	at := primitivesService.Target{Project: "shop", Environment: "prod", Kind: "function", Name: "checkout"}
	req := &invoke.Request{Method: "POST", Path: "/"}

	none, _, _ := makeNode(t, mn, Config{Services: []string{Register}})
	_, err := none.fire(t.Context(), at, req)
	require.ErrorIs(t, err, ErrNoPrimitives)

	answers := &answering{name: "wasm", status: 200}
	c := dialingNode(t, mn, answers)
	res, err := c.node.fire(t.Context(), at, req)
	require.NoError(t, err)
	require.Equal(t, 200, res.Status)
	require.Len(t, answers.seen(), 1, "this node's own worker ran it")
}

func TestAFireIsPlacedWhereThereIsDiscovery(t *testing.T) {
	mn := peertest.Net(t)
	answers := &answering{name: "wasm", status: 200}
	n, _ := consuming(t, mn, answers, compiled())
	require.NotNil(t, n.workers)

	res, err := n.fire(t.Context(), primitivesService.Target{Project: "shop", Environment: "prod", Kind: "function", Name: "checkout"},
		&invoke.Request{Method: "POST", Path: "/"})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status)
	require.Len(t, answers.seen(), 1)
}
