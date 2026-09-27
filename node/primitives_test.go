package node

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

// primitives_test is the cutover itself: the `sync` kind left the worker's own
// process, so what has to be true is that a send and an append reach the
// SERVICE — on this node when it runs one, and over the swarm when it does not.
//
// WHAT A DREAM TEST PROVES AND THIS ONE CANNOT is that a guest calling
// `taubyte:queue/producer` arrives here at all; that needs a compiled component
// and a real tier. What THIS proves is the half a dream test cannot isolate:
// which namespace the item landed in, and which arm — this node's own service,
// or one across the swarm — answered for it.

const (
	primJobsID  = "QmVLDAzEjEbfBPuoyLPWnPMhKp8ceDXwGdmMKY5W3nZWfG"
	primAuditID = "QmZ4gCbmYaFZ4dWJmvNBQKvNAWMYtHqQTHRDprHmxPh6Zk"
)

// withPrimitives is a project declaring one of each of the kind's two types.
//
// THEY ARE DECLARED BECAUSE THE SERVICE READS THEM. A caller carries a
// deployment and a resource id and nothing else; the visibility timeout, the
// retry budget and the retention are read out of the compiled project, which is
// the whole reason a worker can no longer describe a queue into being something
// it is not.
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
func holdingSync(t *testing.T, cfg Config) *Node {
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

// TestANodeThatRunsPrimitivesOpensIt, and one that does not opens none — which
// is the composition's own claim and the thing an operator's service list is
// checked against.
func TestANodeThatRunsPrimitivesOpensTheService(t *testing.T) {
	n := holdingSync(t, Config{Services: []string{Register, Worker, Stats, Primitives}})
	require.NotNil(t, n.Primitives(), "the service list named it")

	other, _, _ := makeNode(t, Config{Services: []string{Register, Stats}})
	require.Nil(t, other.Primitives(), "the service list did not")
}

// TestPRIMITIVESISINTHEDEFAULTSHAPE. All is what a single-node development
// cloud starts, and after this cutover a worker without a primitives service
// has no queues and no logs at all.
func TestTheDefaultShapeRunsPrimitives(t *testing.T) {
	n, _, _ := makeNode(t, Config{})
	require.Contains(t, n.Services(), Primitives)
	require.NotNil(t, n.Primitives())
}

// TestANodeCOMPOSEDWITHNOGROUPISREFUSEDWHEREITISBUILT rather than answering
// "unavailable" about every queue forever. Both of the kind's types are
// decisions about who writes next.
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

// TestANodeCOMPOSEDWITHNORECORDSTOREISTOO, because a log's records live in the
// replicated store beside every other kind's bytes.
func TestAPrimitivesNodeWithNoRecordStoreIsRefusedAtConstruction(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	_, err := New(Config{Services: []string{Primitives}}, WithHost(h),
		WithRaft(func(string) (raft.Cluster, error) { return raft.NewMockCluster(), nil }))
	require.ErrorIs(t, err, ErrNoCacheStore)
}

// TestASENDLANDSINTHESERVICESNAMESPACE — the cutover, stated as the one thing
// that would be invisible if it were wrong.
//
// AN ITEM WRITTEN TO ONE GROUP AND DRAINED FROM ANOTHER GOES QUIET RATHER THAN
// RED. This used to assert both sides — that the service held the item and that
// node/queue.go's group did not — and the second half is gone with the file: a
// worker node now opens no raft group of its own at all, so there is no other
// namespace an item could land in.
func TestASendThroughTheNodeLandsInThePrimitivesNamespace(t *testing.T) {
	n := holdingSync(t, Config{Services: []string{Register, Worker, Stats, Primitives}})
	jobs := handleOf(primJobsID)

	require.NoError(t, n.syncs.Send(t.Context(), jobs, "order-1", []byte("ship it")))

	depth, err := n.Primitives().Local().Depth(t.Context(), jobs)
	require.NoError(t, err)
	require.Equal(t, uint64(1), depth.Ready, "the service does not hold the item")
}

// TestQueueDepthREADSTHESERVICE, which is what keeps every assertion written
// against it honest: it used to read this node's own group by a bare handle,
// and after the cutover that group is empty.
func TestQueueDepthAnswersFromTheService(t *testing.T) {
	n := holdingSync(t, Config{Services: []string{Register, Worker, Stats, Primitives}})
	jobs := handleOf(primJobsID)

	require.NoError(t, n.syncs.Send(t.Context(), jobs, "order-1", []byte("ship it")))

	ready, claimed, ok := n.QueueDepth(jobs.Handle)
	require.True(t, ok)
	require.Equal(t, 1, ready)
	require.Zero(t, claimed)
}

// TestARESOURCETHISDEPLOYMENTDOESNOTDECLAREISNOTSILENTLYANEMPTYONE. node/queue.go
// read the whole config off the grant, so a handle naming nothing at all opened
// a queue with pkg/queue's defaults and a send into it looked like it worked.
func TestASendToAQueueNobodyDeclaredIsRefused(t *testing.T) {
	n := holdingSync(t, Config{Services: []string{Register, Worker, Stats, Primitives}})

	err := n.syncs.Send(t.Context(), handleOf("QmNothingDeclaresThis"), "1", []byte("x"))
	require.ErrorIs(t, err, primitivesService.ErrNotDeclared)
}

// TestANAPPENDGOESTHROUGHTHESERVICETOO, which is the other half of the kind and
// the one node/log.go used to answer out of the worker's own process.
func TestALogAppendedThroughTheNodeIsReadBackFromTheService(t *testing.T) {
	n := holdingSync(t, Config{Services: []string{Register, Worker, Stats, Primitives}})
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

// TestAWORKERFINDSAPRIMITIVESSERVICEITDOESNOTRUN — the whole point of a
// composition that reports what it runs, and the arm node/database.go's kvBacking
// still does not have.
//
// IT IS A REAL HOP: a real gossipsub carries the view, the item crosses a p2p
// stream, and the node that answers is the one holding the namespace.
func TestAWorkerReachesAPrimitivesServiceOnAnotherNode(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register, Stats, Primitives}},
		Config{Services: []string{Worker, Stats}},
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

// TestTHESTREAMTOAREMOTEPRIMITIVESSERVICEISKEPT, so a send on the request path
// is not a dial on the request path.
func TestTheStreamToARemotePrimitivesServiceIsKept(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register, Stats, Primitives}},
		Config{Services: []string{Worker, Stats}},
	)
	compute := nodes[1]
	sees(t, compute, Primitives)

	first, err := compute.sends.Now(t.Context())
	require.NoError(t, err)
	second, err := compute.sends.Now(t.Context())
	require.NoError(t, err)
	require.Same(t, first, second)
}

// TestANODETHATCANREACHNOSERVICESAYSSO rather than accepting the send and
// dropping it, which is the failure a local fallback would have hidden: the
// item would land in a group nothing drains and every assertion would pass.
func TestASendWithNoPrimitivesAnywhereIsRefused(t *testing.T) {
	nodes := fleet(t, Config{Services: []string{Worker, Stats}})

	err := nodes[0].syncs.Send(t.Context(), handleOf(primJobsID), "1", []byte("x"))
	require.ErrorIs(t, err, ErrNoPrimitives)
}

// TestCLOSINGANODECLOSESTHESERVICE, and the service's own Close waits out a
// group still forming rather than leaving a goroutine writing into a store that
// has gone.
func TestClosingANodeClosesThePrimitivesService(t *testing.T) {
	n := holdingSync(t, Config{Services: []string{Register, Worker, Stats, Primitives}})

	require.NoError(t, n.Close())
	require.NoError(t, n.Close(), "closing twice is not a second teardown")

	err := n.syncs.Send(t.Context(), handleOf(primJobsID), "1", []byte("x"))
	require.ErrorIs(t, err, primitivesService.ErrClosed)
}

// TestATASKINGFORTHISNODESKIPSTHEWIRE. A node that holds the namespace and
// dialled itself to reach it would add a stream and a serialization to a call
// already in the right process — syncBacking's rule, and the clock keeps it.
func TestATaskingForThisNodeSkipsTheWire(t *testing.T) {
	n := holdingSync(t, Config{Services: []string{Register, Worker, Primitives}})

	// No stats, so there is no fleet to pick from — and the one primitives
	// service this node can reach is its own.
	require.NoError(t, n.taskCron(t.Context(), at(2, 59), at(3, 0)))

	_, dialled := n.clocks.At()
	require.False(t, dialled, "the clock dialled a service in this very process")
}

// TestAWINDOWREACHESANOTHERNODESSERVICEOVERTHESWARM, and a node that cannot
// generate says so by name rather than answering an empty done.
//
// IT IS A REAL HOP: the window crosses a p2p stream as protobuf, and the node
// that answers is the one that was handed it. What that node has is the
// namespace and no compute, which is the designed remote arm — so it has no
// deployments to enumerate crons from and refuses, which is what leaves the
// leader free to task somebody else next minute.
func TestAWindowHandedToAnotherNodeReachesItsPrimitivesService(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register, Worker, Stats, Primitives}},
		Config{Services: []string{Stats, Primitives}},
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
