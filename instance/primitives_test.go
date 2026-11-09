package instance

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	pkgregister "github.com/taubyte/tau/pkg/register"
	primitivesService "github.com/taubyte/tau/services/primitives"
)

// A node runs the primitives service when its config says, and its worker reaches one
// on another node. What the worker sends and appends is services/worker/hosted's.

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
	require.NoError(t, n.Primitives().WaitForLeader(t.Context()))
	return n
}

func TestANodeThatRunsPrimitivesOpensTheService(t *testing.T) {
	n := holdingSync(t, Config{Services: []string{Register, Worker, Stats, Primitives}})
	require.NotNil(t, n.Primitives(), "the service list named it")

	other, _, _ := makeNode(t, Config{Services: []string{Register, Stats}})
	require.Nil(t, other.Primitives(), "the service list did not")
}

func TestTheDefaultShapeRunsPrimitives(t *testing.T) {
	n, _, _ := makeNode(t, Config{})
	require.Contains(t, n.Services(), Primitives)
	require.NotNil(t, n.Primitives())
}

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
	require.NoError(t, holder.Primitives().WaitForLeader(t.Context()))

	finds(t, compute, primitivesService.Protocol)

	jobs := handleOf(primJobsID)
	require.NoError(t, holder.Primitives().Local().Send(t.Context(), jobs, "order-1", []byte("ship it")))

	require.Eventually(t, func() bool {
		ready, _, ok := compute.QueueDepth(jobs.Handle)
		return ok && ready == 1
	}, 10*time.Second, 50*time.Millisecond, "the worker never read the queue on the other node")
}

func TestAWindowHandedToAnotherNodeReachesItsPrimitivesService(t *testing.T) {
	// THE HOLDER IS THE ONLY PRIMITIVES NODE, so it leads its group alone: a window
	// it took would otherwise be forwarded to a leader that does run a clock.
	nodes := fleet(t,
		Config{Services: []string{Register, Worker, Stats}},
		Config{Services: []string{Stats, Primitives}},
	)
	clock, holder := nodes[0], nodes[1]
	require.NoError(t, holder.Primitives().WaitForLeader(t.Context()))

	// DIALLED DIRECTLY rather than through the clock's reach, because the
	// reach's whole job is to decide WHO gets the window and this test is about
	// what happens once it has been handed over.
	client, err := primitivesService.NewClient(clock.Host(), holder.Host().ID())
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
