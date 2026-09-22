//go:build dreaming

package dream_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	nodeDream "github.com/taubyte/tau/node/dream"
)

// queue_test is background work, end to end and with nothing faked: a guest
// pushes an item, the platform claims it off a raft-replicated queue with no
// prompting, and the same guest is invoked again with the payload.
//
// EVERY OTHER TEST OF THIS PATH FAKES ITS NEIGHBOUR — the dispatcher's fakes a
// tier, the tier's fakes a queue, the queue's fakes raft. What none of them can
// show is that a workload declaring `receive` in a compiled project is a
// workload something actually comes and invokes.
//
// AND THE PROJECT IS AUTHORED RATHER THAN TRANSCRIBED, for project_test's
// reason: the object this test used to publish was written by hand, so it was a
// statement of what the compiler was believed to produce and nothing ever
// checked it. It had already drifted — it omitted `execution/timeout`, which is
// Required(), so tcc would have refused the very project this test claimed to
// be running. The yaml under projects/with/sync/queue is compiled here, which
// is the only way a queue's declaration and a queue's behaviour are tested as
// one thing.

// lastOf renders whatever the last attempt said, at the moment the assertion
// gives up rather than at the moment it was written.
type lastOf struct{ s *string }

func (l *lastOf) String() string { return *l.s }

// TestAnItemSentByAGuestIsDeliveredToOne — the whole claim of the delivered
// half of a queue.
//
// ONE NODE, because what is under test is delivery and not replication: the
// queue's group is raft either way, and a single-node universe forms it through
// the same bootstrap a fleet does. A fleet would add the placement of the
// dispatcher to the things this test could fail on without adding anything to
// what it proves.
//
// THE HOST IS INERT. deploy names one because a register keeps its pointers by
// fqdn, but every request below is put to the worker directly — the project
// declares no zone and no gateway, exactly as the object it replaces did.
func TestAnItemSentByAGuestIsDeliveredToOne(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	r := deploy(t, "with/sync/queue", "jobs.example.com", cacherWASM(t))

	// THE GUEST IS THE PRODUCER, which is the ordinary shape: a request comes
	// in, the work is queued, and the request answers immediately.
	//
	// RETRIED, because a cloud's consensus for queues forms in the background
	// and a send that arrives first is told `unavailable` rather than made to
	// wait out raft's bootstrap. That refusal IS the contract — the arm exists
	// so a caller can retry — so a test that only ever sent once would be
	// testing a cloud that had already been running.
	//
	// THE BUDGET IS THE FORMING ITSELF, PRICED — see formingBudget.
	var last string
	require.Eventually(t, func() bool {
		res, err := r.onCarrying(t.Context(), target(), http.MethodPost, "/send/jobs/order-1", "ship it")
		if err != nil {
			last = err.Error()
			return false
		}
		last = string(res.Body)
		return res.Status == 204
	}, formingBudget, time.Second, "the guest could never queue anything: %s", &lastOf{&last})

	// AND NOBODY ASKS FOR IT BACK. The node's own claim loop finds the item,
	// invokes the workload that declared `receive`, and the delivery lands on
	// the path the entry declared — which for this component means the payload
	// is written into the cache under `done`.
	//
	// THE CEILING HERE IS ONE MISSED CLAIM CYCLE. By this line the group has a
	// leader — the send above proved it — so what is left is claimEvery
	// (node/dispatch.go:60, 2s) for a poll with no notification behind it, a
	// warm invoke of a component this node has already run, and this loop's own
	// 500ms granularity: under 3s. The rest is the cost of REPORTING a stall
	// rather than a bound on one, and it is the number this assertion has
	// always carried.
	require.Eventually(t, func() bool {
		res, err := r.onCarrying(t.Context(), target(), http.MethodPost, "/get/sessions/done", "")
		return err == nil && res.Status == 200 && string(res.Body) == "ship it"
	}, 60*time.Second, 500*time.Millisecond, "nothing ever delivered the item")

	// AND IT IS ACKNOWLEDGED, so the queue is empty rather than holding an item
	// that will be handed round again.
	//
	// THE HANDLE IS ASKED OF THE COMPILED PROJECT, and out here rather than in
	// the tick. `shop` is the project the register knows this deployment by and
	// `prod` the environment deploy publishes under, and the last segment is
	// whatever the label `jobs` became — read back rather than typed, for
	// idOf's reason. It is resolved once because testify runs the condition on
	// its own goroutine, where a failed require kills that goroutine and leaves
	// this waiting out the whole budget for something it already knew.
	jobs := "shop/prod/" + r.idOf(t, "sync", "jobs")
	require.Eventually(t, func() bool {
		ready, claimed, ok := r.node.QueueDepth(jobs)
		return ok && ready == 0 && claimed == 0
	}, 60*time.Second, 500*time.Millisecond, "the item was delivered and never settled")

	// AND ACKNOWLEDGED IS NOT THE ONLY WAY TO BE GONE, which is why the dead
	// letter is read too. An item handed round until max_attempts is spent is
	// BURIED, and the entry that buries it takes it out of this queue in the
	// same batch (pkg/queue/queue.go:389-411) — so a queue left at 0/0 is what
	// a burial looks like as well, and every assertion above passes on an item
	// nobody ever kept. node/queue.go:255-261 says in its own words that this
	// blind spot once let a workflow step which buried every run through it
	// pass an end-to-end test.
	//
	// IT IS A MOMENT AND NOT A WAIT, because by this line the source queue
	// holds nothing: there is no item left for a later cycle to bury. And that
	// a burial would be VISIBLE here is pkg/queue's own claim, already proven
	// one layer down by TestSweepAloneReachesTheDeadLetter — the source ends at
	// 0/0 and the dead letter ends holding one.
	buried, held, ok := r.node.QueueDepth("shop/prod/" + r.idOf(t, "sync", "jobs_dead"))
	require.True(t, ok, "the dead letter could not be read at all")
	require.Zero(t, buried, "the item was buried rather than acknowledged")
	require.Zero(t, held, "something is holding an item in the dead letter")
}
