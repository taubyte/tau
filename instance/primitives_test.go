package instance

import (
	"testing"
	"time"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/raft"
	pkgregister "github.com/taubyte/tau/pkg/register"
	primitivesService "github.com/taubyte/tau/services/primitives"
)

// The cutover: a send and an append reach the service, locally or over the swarm,
// and land in its namespace. That a guest's call arrives here at all is a dream test's.

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
func holdingSync(t *testing.T, cfg Config) *Instance {
	t.Helper()

	n, _, _ := makeNode(t, cfg)
	require.NoError(t, n.Start(t.Context()))

	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: withPrimitives(),
	})
	require.NoError(t, err)

	// THE GROUP FORMS OFF THE REQUEST PATH, so a test that called straight into
	// it would be testing ErrForming. The mock is instant; waiting for it is
	// what makes the assertions below deterministic rather than fast.
	<-n.Primitives().Formed()
	return n
}

func TestANodeThatRunsPrimitivesOpensTheService(t *testing.T) {
	n := holdingSync(t, Config{Services: []string{Register, Worker, Discovery, Primitives}})
	require.NotNil(t, n.Primitives(), "the service list named it")

	other, _, _ := makeNode(t, Config{Services: []string{Register, Discovery}})
	require.Nil(t, other.Primitives(), "the service list did not")
}

func TestTheDefaultShapeRunsPrimitives(t *testing.T) {
	n, _, _ := makeNode(t, Config{})
	require.Contains(t, n.Services(), Primitives)
	require.NotNil(t, n.Primitives())
}

func TestAPrimitivesNodeWithNoConsensusIsRefusedAtConstruction(t *testing.T) {
	// BUILT WITHOUT THE HARNESS, which always hands a node a raft opener: what
	// is under test is the composition that has none.
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	_, err := New(Config{Services: []string{Primitives}},
		WithHost(h), WithDatabases(&memFactory{}))
	require.ErrorIs(t, err, ErrNoRaft)
}

func TestAPrimitivesNodeWithNoRecordStoreIsRefusedAtConstruction(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	_, err := New(Config{Services: []string{Primitives}}, WithHost(h),
		WithRaft(func(string) (raft.Cluster, error) { return raft.NewMockCluster(), nil }))
	require.ErrorIs(t, err, ErrNoCacheStore)
}

func TestASendThroughTheNodeLandsInThePrimitivesNamespace(t *testing.T) {
	n := holdingSync(t, Config{Services: []string{Register, Worker, Discovery, Primitives}})
	jobs := handleOf(primJobsID)

	require.NoError(t, n.syncs.Send(t.Context(), jobs, "order-1", []byte("ship it")))

	depth, err := n.Primitives().Local().Depth(t.Context(), jobs)
	require.NoError(t, err)
	require.Equal(t, uint64(1), depth.Ready, "the service does not hold the item")
}

func TestQueueDepthAnswersFromTheService(t *testing.T) {
	n := holdingSync(t, Config{Services: []string{Register, Worker, Discovery, Primitives}})
	jobs := handleOf(primJobsID)

	require.NoError(t, n.syncs.Send(t.Context(), jobs, "order-1", []byte("ship it")))

	ready, claimed, ok := n.QueueDepth(jobs.Handle)
	require.True(t, ok)
	require.Equal(t, 1, ready)
	require.Zero(t, claimed)
}

func TestASendToAQueueNobodyDeclaredIsRefused(t *testing.T) {
	n := holdingSync(t, Config{Services: []string{Register, Worker, Discovery, Primitives}})

	err := n.syncs.Send(t.Context(), handleOf("QmNothingDeclaresThis"), "1", []byte("x"))
	require.ErrorIs(t, err, primitivesService.ErrNotDeclared)
}

func TestALogAppendedThroughTheNodeIsReadBackFromTheService(t *testing.T) {
	n := holdingSync(t, Config{Services: []string{Register, Worker, Discovery, Primitives}})
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

func TestAWorkerReachesAPrimitivesServiceOnAnotherNode(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register, Discovery, Primitives}},
		Config{Services: []string{Worker, Discovery}},
	)
	holder, compute := nodes[0], nodes[1]

	_, err := holder.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: withPrimitives(),
	})
	require.NoError(t, err)
	<-holder.Primitives().Formed()

	sees(t, compute, Primitives)

	jobs := handleOf(primJobsID)
	require.NoError(t, compute.syncs.Send(t.Context(), jobs, "order-1", []byte("ship it")))

	depth, err := holder.Primitives().Local().Depth(t.Context(), jobs)
	require.NoError(t, err)
	require.Equal(t, uint64(1), depth.Ready, "the item never crossed")
}

func TestTheStreamToARemotePrimitivesServiceIsKept(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register, Discovery, Primitives}},
		Config{Services: []string{Worker, Discovery}},
	)
	compute := nodes[1]
	sees(t, compute, Primitives)

	first, err := compute.sends.Now(t.Context())
	require.NoError(t, err)
	second, err := compute.sends.Now(t.Context())
	require.NoError(t, err)
	require.Same(t, first, second)
}

func TestASendWithNoPrimitivesAnywhereIsRefused(t *testing.T) {
	nodes := fleet(t, Config{Services: []string{Worker, Discovery}})

	err := nodes[0].syncs.Send(t.Context(), handleOf(primJobsID), "1", []byte("x"))
	require.ErrorIs(t, err, ErrNoPrimitives)
}

func TestClosingANodeClosesThePrimitivesService(t *testing.T) {
	n := holdingSync(t, Config{Services: []string{Register, Worker, Discovery, Primitives}})

	require.NoError(t, n.Close())
	require.NoError(t, n.Close(), "closing twice is not a second teardown")

	err := n.syncs.Send(t.Context(), handleOf(primJobsID), "1", []byte("x"))
	require.ErrorIs(t, err, primitivesService.ErrClosed)
}

func TestATaskingForThisNodeSkipsTheWire(t *testing.T) {
	n := holdingSync(t, Config{Services: []string{Register, Worker, Primitives}})

	// No discovery, so there is no fleet to pick from — and the one primitives
	// service this node can reach is its own.
	require.NoError(t, n.taskCron(t.Context(), at(2, 59), at(3, 0)))

	_, dialled := n.clocks.At()
	require.False(t, dialled, "the clock dialled a service in this very process")
}

func TestAWindowHandedToAnotherNodeReachesItsPrimitivesService(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register, Worker, Discovery, Primitives}},
		Config{Services: []string{Discovery, Primitives}},
	)
	clock, holder := nodes[0], nodes[1]
	<-holder.Primitives().Formed()

	// DIALLED DIRECTLY rather than through the clock's reach, because the
	// reach's whole job is to decide WHO gets the window and this test is about
	// what happens once it has been handed over.
	client, err := primitivesService.NewClient(clock.opts.host, holder.opts.host.ID())
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	err = client.CronTask(t.Context(), at(2, 59), at(3, 0))
	require.ErrorContains(t, err, primitivesService.ErrNoClock.Error(),
		"the window either never crossed or was answered done by a node that runs no clock")
}

// at is a moment in UTC, for the window a leader hands out.
func at(hour, minute int) time.Time {
	return time.Date(2026, 9, 5, hour, minute, 0, 0, time.UTC)
}

func TestAFireRunsOnTheWorkerItCanReach(t *testing.T) {
	at := primitivesService.Target{Project: "shop", Environment: "prod", Kind: "function", Name: "checkout"}
	req := &invoke.Request{Method: "POST", Path: "/"}

	none, _, _ := makeNode(t, Config{Services: []string{Register}})
	_, err := none.fire(t.Context(), at, req)
	require.ErrorIs(t, err, ErrNoPrimitives)

	answers := &answering{name: "wasm", status: 200}
	c := dialingNode(t, answers)
	res, err := c.node.fire(t.Context(), at, req)
	require.NoError(t, err)
	require.Equal(t, 200, res.Status)
	require.Len(t, answers.seen(), 1, "this node's own worker ran it")
}

func TestAFireIsPlacedWhereThereIsDiscovery(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, _ := consuming(t, answers, compiled())
	require.NotNil(t, n.workers)

	res, err := n.fire(t.Context(), primitivesService.Target{Project: "shop", Environment: "prod", Kind: "function", Name: "checkout"},
		&invoke.Request{Method: "POST", Path: "/"})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status)
	require.Len(t, answers.seen(), 1)
}

func TestAMinuteNeedsAPrimitivesService(t *testing.T) {
	_, err := (&Instance{}).clockNow(t.Context())
	require.ErrorIs(t, err, ErrNoPrimitives)
	require.ErrorContains(t, err, "runs no service to take one")
}
