//go:build dreaming

package dream_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/services/worker"
)

func TestAScheduleCarriesTheFiringBlockItsAuthorWrote(t *testing.T) {
	r := deploy(t, "with/cron/queue", "nightly.example.com", cacherWASM(t))

	// The node must serve the project first: Schedules only enumerates what a node
	// already holds.
	require.Eventually(t, func() bool {
		_, err := r.node.Worker().Resolve(t.Context(), r.function("cacher"))
		return err == nil
	}, formingBudget, 250*time.Millisecond, "the node never came to serve the project")

	// AND ONLY THEN are its schedules readable.
	var held worker.Schedule
	require.Eventually(t, func() bool {
		for _, s := range r.node.Worker().Schedules(t.Context()) {
			if s.Name == "nightly" {
				held = s
				return true
			}
		}
		return false
	}, formingBudget, 250*time.Millisecond, "the project never resolved a schedule called nightly")

	s := held
	{
		require.Equal(t, "0 3 * * *", s.Spec)
		require.Equal(t, "Europe/Paris", s.Timezone,
			"an omitted zone means UTC, so a zone that survived is the author's")
		require.Equal(t, "queue", s.OnOverlap,
			"skip is the default; queue is the arm a billing run needs")
		require.Equal(t, 30*time.Second, s.Jitter)

		// AND IT STILL EDGES TO SOMETHING. A firing block on a cron that fires
		// nothing would be a decoration the compiler happened to carry.
		require.Len(t, s.Fires, 1)
		require.Equal(t, "cacher", s.Fires[0].Name)
	}
}
