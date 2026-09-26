//go:build dreaming

package dream_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/worker"
)

// TestAScheduleCarriesTheFiringBlockItsAuthorWrote.
//
// THE THREE FIRING FIELDS ALL HAVE DEFAULTS, which is what made them invisible:
// with/cron/skip writes UTC and leans on `skip` and no jitter, so every one of
// them compiled to what the vocabulary would have chosen anyway and nothing
// anywhere showed the author's answer surviving. The service's own tests drive
// queue, allow and jitter against a fake clock; what none of them can show is
// that the yaml reached them.
//
// SO THIS RESOLVES AND DOES NOT FIRE. Firing is with/cron/skip's claim and it
// already makes it end to end. What is missing is the path in between -- yaml,
// compile, publish, resolve -- for the fields that project never writes.
//
// IT ALSO CANNOT FIRE, which is worth stating rather than leaving as a gap
// somebody rediscovers: the jitter wait is time.After (services/primitives/
// cron.go:788) and not the injected clock, so a jittered schedule spends real
// seconds no simulated clock can skip. A project that declares jitter and fires
// would cost those seconds on every run of this suite.
func TestAScheduleCarriesTheFiringBlockItsAuthorWrote(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	r := deploy(t, "with/cron/queue", "nightly.example.com", cacherWASM(t))

	// THE NODE HAS TO BE SERVING IT FIRST. Schedules is lazy on purpose -- "what
	// a node can fire is what it is already serving" -- so a deployment nothing
	// has touched enumerates nothing, however long you wait for it. Resolving
	// the cron's own target is what makes this node hold the project, and it is
	// the same thing the fire test does before it reads anything.
	require.Eventually(t, func() bool {
		_, err := r.node.Worker().Resolve(t.Context(), specs.Address{
			Project: "shop", Environment: "prod", Kind: "function", Name: "cacher",
		})
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
