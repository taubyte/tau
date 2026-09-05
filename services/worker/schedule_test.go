package worker

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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

// TestATargetNothingRunsIsSAIDRatherThanDropped. The fixture's cron also starts
// a workflow, and nothing in this cloud runs one — so the cron still fires its
// function and the reason the other edge does nothing is on the schedule.
func TestAnUnrunnableTargetIsReported(t *testing.T) {
	h := newHarness(t)
	holding(t, h, "function", "checkout")

	got := schedulesOf(t, h)["nightly"]
	require.NotEmpty(t, got.Refused)
	require.Contains(t, got.Refused, "workflow")
	require.NotEmpty(t, got.Fires, "and the edge that CAN be fired still is")
}

// TestACronThatEdgesToNothingIsNotAScheduleToRun. A clock nobody set an alarm
// on is legal and is not something to spend a loop on.
func TestACronWithNoEdgesIsNotListed(t *testing.T) {
	h := newHarness(t)
	holding(t, h, "function", "checkout")

	for _, s := range h.Worker.Schedules(t.Context()) {
		require.NotEmpty(t, s.Fires, "%s is listed with nothing to fire", s.Name)
	}
}
