package backend

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/v2/queue"
	"github.com/taubyte/tau/services/build"
)

// The zero Config must be a working one, because a node that declared nothing
// still has to start. Every default is checked, not just that it is non-zero:
// a default that silently changed is a behaviour change nothing else notices.
func TestTheZeroConfigIsValidAndItsDefaultsAreThese(t *testing.T) {
	c, err := Config{}.withDefaults()
	require.NoError(t, err)

	require.Equal(t, DefaultBuildTimeout, c.BuildTimeout)
	require.Equal(t, DefaultClaimMargin, c.ClaimMargin)
	require.Equal(t, DefaultClaimInterval, c.ClaimInterval)
	require.Equal(t, DefaultReconcileEvery, c.ReconcileEvery)
	require.Equal(t, DefaultMaxQueueDepth, c.MaxQueueDepth)
	require.Equal(t, DefaultMaxOpenPerRepo, c.MaxOpenPerRepo)
	require.Equal(t, DefaultMaxMessageBytes, c.MaxMessageBytes)
	require.Equal(t, build.QueueDomain, c.Queue.Name)
	require.Equal(t, DefaultBuildTimeout+DefaultClaimMargin, c.Queue.VisibilityTimeout)
}

// The queue's name is the consistency domain build.proto fixes by literal. Two
// nodes of one cloud disagreeing about it is two queues where there is meant
// to be one, so it is not a deployment's to set.
func TestTheQueueNameIsForcedToTheConsistencyDomain(t *testing.T) {
	c, err := Config{Queue: queue.Config{Name: "something-else"}}.withDefaults()
	require.NoError(t, err)
	require.Equal(t, build.QueueDomain, c.Queue.Name)
}

// A lease that does not outlast a build is a second claimer with the same job.
func TestALeaseThatDoesNotOutlastABuildIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name              string
		buildTimeout      time.Duration
		visibilityTimeout time.Duration
	}{
		{"equal", time.Hour, time.Hour},
		{"shorter", time.Hour, time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Config{
				BuildTimeout: tc.buildTimeout,
				Queue:        queue.Config{VisibilityTimeout: tc.visibilityTimeout},
			}.withDefaults()
			require.ErrorContains(t, err, "must outlast")
		})
	}

	c, err := Config{
		BuildTimeout: time.Minute,
		Queue:        queue.Config{VisibilityTimeout: 2 * time.Minute},
	}.withDefaults()
	require.NoError(t, err)
	require.Equal(t, 2*time.Minute, c.Queue.VisibilityTimeout)
}

func TestNegativeTunablesAreRefused(t *testing.T) {
	for name, c := range map[string]Config{
		"buildTimeout":    {BuildTimeout: -1},
		"claimMargin":     {ClaimMargin: -1},
		"claimInterval":   {ClaimInterval: -1},
		"reconcileEvery":  {ReconcileEvery: -1},
		"maxQueueDepth":   {MaxQueueDepth: -1},
		"maxOpenPerRepo":  {MaxOpenPerRepo: -1},
		"maxMessageBytes": {MaxMessageBytes: -1},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := c.withDefaults()
			require.ErrorContains(t, err, name)
		})
	}
}

func TestActionsName(t *testing.T) {
	require.Equal(t, "enqueue", ActEnqueue.String())
	require.Equal(t, "read", ActRead.String())
	require.Equal(t, "cancel", ActCancel.String())
	require.Equal(t, "retry", ActRetry.String())
	require.Equal(t, "action(99)", Action(99).String())
}

// THE PUBLISH IS SPENT OUT OF THE MARGIN, so a publish that could use all of it
// is an acknowledgement made after the claim expired — which the queue refuses,
// correctly, and the job is then rebuilt by whoever takes the redelivery.
func TestAPublishTimeoutMustFitInsideTheClaimMargin(t *testing.T) {
	for name, c := range map[string]Config{
		"the whole margin": {ClaimMargin: 30 * time.Second, PublishTimeout: 30 * time.Second},
		"more than it":     {ClaimMargin: time.Second, PublishTimeout: time.Minute},
	} {
		_, err := c.withDefaults()
		require.ErrorContains(t, err, "publishTimeout", name)
	}

	c, err := Config{ClaimMargin: time.Minute, PublishTimeout: 30 * time.Second}.withDefaults()
	require.NoError(t, err)
	require.Equal(t, 30*time.Second, c.PublishTimeout)
}
