//go:build dreaming

package dream_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	nodeDream "github.com/taubyte/tau/instance/dream"
)

// Background work end to end, nothing faked: a guest pushes an item, the platform
// claims it off a raft-replicated queue unprompted, and the same guest is invoked
// with the payload. Compiled from projects/with/sync/queue.

// lastOf renders whatever the last attempt said, at the moment the assertion
// gives up rather than at the moment it was written.
type lastOf struct{ s *string }

func (l *lastOf) String() string { return *l.s }

func TestAnItemSentByAGuestIsDeliveredToOne(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	r := deploy(t, "with/sync/queue", "jobs.example.com", cacherWASM(t))

	// The guest is the producer. Retried, since queue consensus forms in the background
	// and an early send is told `unavailable`. Budget: formingBudget.
	var last string
	require.Eventually(t, func() bool {
		res, err := r.onCarrying(t.Context(), r.function("cacher"), http.MethodPost, "/send/jobs/order-1", "ship it")
		if err != nil {
			last = err.Error()
			return false
		}
		last = string(res.Body)
		return res.Status == 204
	}, formingBudget, time.Second, "the guest could never queue anything: %s", &lastOf{&last})

	// Nobody asks for it back: the node's claim loop invokes the workload declaring
	// `receive`, which writes the payload under `done`. The group has a leader by now,
	// so the ceiling is one claim cycle (2s) plus a warm invoke; the rest is headroom
	// for reporting a stall.
	require.Eventually(t, func() bool {
		res, err := r.onCarrying(t.Context(), r.function("cacher"), http.MethodPost, "/get/sessions/done", "")
		return err == nil && res.Status == 200 && string(res.Body) == "ship it"
	}, 60*time.Second, 500*time.Millisecond, "nothing ever delivered the item")

	// And it is acknowledged, so the queue is empty. The handle is resolved once, out
	// here, since a failed require inside Eventually's goroutine would leave this
	// waiting out the budget.
	jobs := r.project + "/prod/" + r.idOf(t, "sync", "jobs")
	require.Eventually(t, func() bool {
		ready, claimed, ok := r.node.QueueDepth(jobs)
		return ok && ready == 0 && claimed == 0
	}, 60*time.Second, 500*time.Millisecond, "the item was delivered and never settled")

	// The dead letter is read too: a buried item also leaves the source queue empty
	// (pkg/queue/queue.go). By now the source holds nothing, so a moment suffices.
	buried, held, ok := r.node.QueueDepth(r.project + "/prod/" + r.idOf(t, "sync", "jobs_dead"))
	require.True(t, ok, "the dead letter could not be read at all")
	require.Zero(t, buried, "the item was buried rather than acknowledged")
	require.Zero(t, held, "something is holding an item in the dead letter")
}
