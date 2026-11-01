package worker

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/specs"
)

// The fixture's cron edges to two kinds and this cloud runs one of them.

func schedulesOf(t *testing.T, h *harness) map[string]Schedule {
	t.Helper()

	out := map[string]Schedule{}
	for _, s := range h.Worker.Schedules(t.Context()) {
		out[s.Name] = s
	}
	return out
}

func TestAnUnheldDeploymentHasNoSchedules(t *testing.T) {
	h := newHarness(t)
	require.Empty(t, h.Worker.Schedules(t.Context()))
}

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

func TestACronStartsAWorkflow(t *testing.T) {
	h := newHarness(t)
	holding(t, h, "function", "checkout")

	got := schedulesOf(t, h)["nightly"]
	require.Empty(t, got.Refused, "both of its edges are servable now")
	require.Len(t, got.Starts, 1)

	start := got.Starts[0]
	require.Equal(t, "checkout", start.Label)
	require.Equal(t, "sync", start.Settings[settingStartKind])
	require.Equal(t, "queue", start.Settings[settingSyncType],
		"a start is a SEND, so the entry has to name the queue type and not only the kind")
	require.Equal(t, "send", start.Settings[settingStartAs])

	// The grant is the first step's queue, where a send lands.
	require.Equal(t, "sync", start.Kind)
	require.Equal(t, []string{"send"}, start.Ops)
	require.Equal(t, start.Settings[settingStartHandle], start.Handle,
		"a firer gets the queue the entry record names, already resolved")
	require.Contains(t, start.Handle, "shop/prod/")
}

func TestAnUnrunnableTargetIsReported(t *testing.T) {
	require.Contains(t,
		unfireable("zone", specs.Reach{Kind: "zone", Label: "example_com"}),
		"nothing in this cloud runs a zone")
}

func TestACronWithNoEdgesIsNotListed(t *testing.T) {
	h := newHarness(t)
	holding(t, h, "function", "checkout")

	for _, s := range h.Worker.Schedules(t.Context()) {
		require.False(t, len(s.Fires) == 0 && len(s.Starts) == 0,
			"%s is listed with nothing to fire or start", s.Name)
	}
}

func TestAStartCarriesTheQueuesOwnConfig(t *testing.T) {
	h := newHarness(t)
	holding(t, h, "function", "checkout")

	start := schedulesOf(t, h)["nightly"].Starts[0]
	require.Equal(t, "4", start.Settings["queue/max_attempts"],
		"the retry budget the FIRST STEP declared — `attempts: 4` on `reserve`")
	require.Equal(t, "5s", start.Settings["queue/retry/interval"],
		"and its backoff, which the workflow shell itself carries nothing of")
	require.NotEmpty(t, start.Settings["queue/visibility_timeout"])
}
