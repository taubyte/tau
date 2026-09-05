package worker

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/project"
)

// schedule_test is the cron half of the enumeration, against the vocabulary's
// own fixture — which is the interesting case rather than a convenient one,
// because that cron edges to two kinds and this cloud runs one of them.

func schedulesOf(t *testing.T, h *harness) map[string]Schedule {
	t.Helper()

	out := map[string]Schedule{}
	for _, s := range h.Worker.Schedules(t.Context()) {
		out[s.Name] = s
	}
	return out
}

// TestNothingIsFiredUntilSomethingHoldsIt, the same rule Consumers keeps.
func TestAnUnheldDeploymentHasNoSchedules(t *testing.T) {
	h := newHarness(t)
	require.Empty(t, h.Worker.Schedules(t.Context()))
}

// TestTheFIXTURESCronIsRead: every field the vocabulary puts on it reaches the
// thing that fires it, because a schedule the runtime half-read is a job that
// happens at the wrong time.
func TestTheFixturesCronCarriesWhatTheAuthorWrote(t *testing.T) {
	h := newHarness(t)
	holding(t, h, "function", "checkout")

	got, ok := schedulesOf(t, h)["nightly"]
	require.True(t, ok, "the fixture's cron was not found")

	require.Equal(t, "0 3 * * *", got.Spec)
	require.Equal(t, "Europe/Paris", got.Timezone, "a cron expression alone does not name a moment")
	require.Equal(t, "skip", got.OnOverlap)
	require.Equal(t, 45*time.Second, got.Jitter)
	require.Contains(t, got.Handle, "shop/prod/", "scoped to the deployment, like every other handle")
}

// TestACronFiresWhatItEdgesTo, in the form Invoke takes — the vocabulary's own
// sentence is "it fires whatever it edges to; the target declares nothing".
func TestACronsTargetsComeOffItsAccessBlock(t *testing.T) {
	h := newHarness(t)
	holding(t, h, "function", "checkout")

	got := schedulesOf(t, h)["nightly"]
	require.Len(t, got.Fires, 1, "the workflow is not one this cloud runs: %+v", got.Fires)
	require.Equal(t, "function", got.Fires[0].Kind)
	require.Equal(t, "poll_mentions", got.Fires[0].Name)
	require.Equal(t, "shop", got.Fires[0].Project)
	require.Equal(t, "prod", got.Fires[0].Environment)
}

// TestACRONMayStartAWorkflow, which is what the fixture's own does — and a
// workflow is STARTED rather than invoked, because it runs on no tier: it is
// queues, and a start is a send to the first step's.
func TestACronStartsAWorkflow(t *testing.T) {
	h := newHarness(t)
	holding(t, h, "function", "checkout")

	got := schedulesOf(t, h)["nightly"]
	require.Empty(t, got.Refused, "both of its edges are servable now")
	require.Len(t, got.Starts, 1)

	start := got.Starts[0]
	require.Equal(t, "checkout", start.Label)
	require.Equal(t, "queue", start.Settings[settingStartKind])
	require.NotEmpty(t, start.Settings[settingStartHandle],
		"where a start lands, off the workflow's own entry record")
	require.Equal(t, "send", start.Settings[settingStartAs])

	// AND THE HANDLE IS THE FIRST STEP'S QUEUE and not the workflow's own,
	// which is the whole point of the entry record.
	require.NotContains(t, start.Settings[settingStartHandle], start.Handle[len("shop/prod/"):])
}

// TestATargetNothingRunsIsSAIDRatherThanDropped, which is still the answer for
// a kind that runs on nothing and is not started either.
func TestAnUnrunnableTargetIsReported(t *testing.T) {
	require.Contains(t,
		unfireable("log", project.Reach{Kind: "log", Label: "audit"}),
		"nothing in this cloud runs a log")
}

// TestACronThatEdgesToNothingIsNotAScheduleToRun. A clock nobody set an alarm
// on is legal and is not something to spend a loop on.
func TestACronWithNoEdgesIsNotListed(t *testing.T) {
	h := newHarness(t)
	holding(t, h, "function", "checkout")

	for _, s := range h.Worker.Schedules(t.Context()) {
		require.False(t, len(s.Fires) == 0 && len(s.Starts) == 0,
			"%s is listed with nothing to fire or start", s.Name)
	}
}
