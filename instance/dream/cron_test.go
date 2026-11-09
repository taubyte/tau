//go:build dreaming

package dream_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	nodeDream "github.com/taubyte/tau/instance/dream"
	primitivesService "github.com/taubyte/tau/services/primitives"
)

// A schedule firing with only the cloud watching the clock: two nodes holding one
// deployment fire it once, decided by a replicated write. projects/with/cron/skip is
// compiled, and its function writes each fire it is handed into the log `fires`.

func TestAScheduleFiresOnceAcrossAFleet(t *testing.T) {
	// A SWEEP A SECOND, so a slot is handed out within a second of the minute turning
	// rather than within two default turns of it. The clock is the wall clock: the
	// first sight of a schedule records where it is and fires nothing, so the first
	// fire is the next minute's.
	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Services: fleetServices,
		Copies:   2,
		Configure: func(_ int, c *instance.Config) {
			c.Sections[instance.Primitives] = primitivesService.Config{Sweep: time.Second}
		},
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)

	// The host is inert: a cron reaches its target through the worker.
	cloud := publish(t, u, u.Pids(), deployed{name: "with/cron/skip", host: "cron.example.com"}, artifacts{"": cacherWASM(t)})
	require.Len(t, cloud, 2)
	first, second := cloud[0], cloud[1]

	// Both nodes must hold it; the budget is a stall report.
	holds := func(r *running) bool {
		_, err := r.node.Worker().Resolve(t.Context(), r.function("cacher"))
		return err == nil
	}
	require.Eventually(t, func() bool { return holds(first) && holds(second) },
		formingBudget, time.Second, "the project never reached both nodes")

	// One fire, read back from the log the guest wrote it to. The budget: the
	// primitives group forming, the minute the clock is first seen in, and the one
	// after it.
	fires := func() []string {
		res, err := first.onCarrying(t.Context(), first.function("cacher"), http.MethodPost, "/read/fires/", "")
		if err != nil || res.Status != http.StatusOK {
			return nil
		}
		var out []string
		for _, line := range logLinesOf(t, res.Body) {
			out = append(out, line.data)
		}
		return out
	}
	var fired []string
	require.Eventually(t, func() bool {
		fired = fires()
		return len(fired) > 0
	}, formingBudget+2*time.Minute, time.Second, "the schedule never fired")

	// method, cron, id, path, slot: the whole identity, the cron's id from the
	// compiled project (cron/everyminute.yaml), its path at the project root, which
	// is where this project puts it.
	fields := strings.Fields(fired[0])
	require.Len(t, fields, 5, "the guest wrote %q", fired[0])
	require.Equal(t, http.MethodPost, fields[0])
	require.Equal(t, "everyminute", fields[1])
	require.Equal(t, "QmREVyn7gTwpYmasPnrCgkT9CFHi2YX8w3nmqsd3PzQvvU", fields[2])
	require.Equal(t, "/everyminute", fields[3])
	slot, err := time.Parse(time.RFC3339, fields[4])
	require.NoError(t, err)
	require.Zero(t, slot.Second(), "a slot is a whole minute")

	// And only one node fired it: the rest of the slot is waited out. A duplicate would
	// need two leaders, a minute handed out twice, or a redelivery, none possible here,
	// so a failure is a real duplicate. Later turns fire later slots.
	require.Never(t, func() bool {
		n := 0
		for _, f := range fires() {
			if strings.HasSuffix(f, " "+fields[4]) {
				n++
			}
		}
		return n > 1
	}, 20*time.Second, time.Second, "one slot was fired more than once")
}
