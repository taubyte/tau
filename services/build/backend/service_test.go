package backend

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/queue"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/services/build"
)

func TestServiceNeedsOnlyAMocknetHostAMockClusterAndAFakeAuth(t *testing.T) {
	s, err := New(Config{}, WithHost(newHost(t, newMocknet(t))), WithStore(raft.NewMockCluster()), WithRepositories(newRepositories()))
	require.NoError(t, err)
	require.NoError(t, s.Close())
}

func TestNewRefusesWhatItCannotBeBuiltFrom(t *testing.T) {
	mn := newMocknet(t)
	h := newHost(t, mn)
	store := newStore()
	repos := newRepositories()

	for name, tc := range map[string]func() (*Service, error){
		"no host":     func() (*Service, error) { return New(Config{}, WithStore(store), WithRepositories(repos)) },
		"a nil host":  func() (*Service, error) { return New(Config{}, WithHost(nil)) },
		"no store":    func() (*Service, error) { return New(Config{}, WithHost(h), WithRepositories(repos)) },
		"a nil store": func() (*Service, error) { return New(Config{}, WithStore(nil)) },
		"no repositories": func() (*Service, error) {
			return New(Config{}, WithHost(h), WithStore(store))
		},
		"a nil repositories": func() (*Service, error) { return New(Config{}, WithRepositories(nil)) },
		"a lease shorter than a build": func() (*Service, error) {
			return New(Config{
				BuildTimeout: time.Hour,
				Queue:        queue.Config{VisibilityTimeout: time.Minute},
			}, WithHost(h), WithStore(store), WithRepositories(repos))
		},
		"a negative tunable": func() (*Service, error) {
			return New(Config{ClaimInterval: -1}, WithHost(h), WithStore(store), WithRepositories(repos))
		},
		"an apply timeout the queue refuses": func() (*Service, error) {
			return New(Config{
				Queue: queue.Config{ApplyTimeout: raft.MaxApplyTimeout + time.Second},
			}, WithHost(h), WithStore(store), WithRepositories(repos))
		},
		"an option that fails": func() (*Service, error) {
			return New(Config{}, func(*options) error { return errors.New("no") })
		},
		"a nil runner":     func() (*Service, error) { return New(Config{}, WithRunner(nil)) },
		"a nil authorizer": func() (*Service, error) { return New(Config{}, WithAuthorizer(nil)) },
		"a nil asset store": func() (*Service, error) {
			return New(Config{}, WithAssets(nil))
		},
		"a nil clock": func() (*Service, error) { return New(Config{}, WithClock(nil)) },
	} {
		t.Run(name, func(t *testing.T) {
			s, err := tc()
			require.Error(t, err)
			require.Nil(t, s)
		})
	}
}

func TestNewBindsNothing(t *testing.T) {
	h := newHarness(t)
	require.Nil(t, h.server)

	// The protocol is free until Start takes it, which is what a second
	// listener on the same host proves.
	srv, err := rpc.Listen(t.Context(), h.host, build.Protocol, http.NotFoundHandler())
	require.NoError(t, err)
	require.NoError(t, srv.Close())
}

func TestStartTwiceIsAnErrorAndSoIsStartAfterClose(t *testing.T) {
	h := newHarness(t)

	require.NoError(t, h.Start(t.Context()))
	require.ErrorContains(t, h.Start(t.Context()), "already started")

	require.NoError(t, h.Close())
	require.NoError(t, h.Close(), "Close is idempotent")
	require.ErrorContains(t, h.Start(t.Context()), "closed")
}

func TestASecondListenerOnTheProtocolIsRefused(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, h.Start(t.Context()))

	other, err := New(Config{}, WithHost(h.host), WithStore(newStore()), WithRepositories(newRepositories()))
	require.NoError(t, err)
	t.Cleanup(func() { other.Close() })

	require.ErrorIs(t, other.Start(t.Context()), rpc.ErrProtocolTaken)
}

func TestCloseStopsTheClaimLoop(t *testing.T) {
	h := newHarnessCfg(t, func(c *Config) {
		c.ClaimInterval = time.Millisecond
		c.ReconcileEvery = time.Millisecond
	})

	require.NoError(t, h.Start(t.Context()))
	require.NoError(t, h.Close())

	select {
	case <-h.stopped:
	default:
		t.Fatal("Close returned with the loop still running")
	}
}

func TestCloseLeavesTheStoreAlone(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	require.NoError(t, h.Close())

	_, exists := h.store.Get(jobRecordKey(id))
	require.True(t, exists)
}

func TestTheQueueAndItsDeadLetterAreNamedFromTheDomain(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, build.QueueDomain, h.cfg.Queue.Name)
	require.Equal(t, "build/queue/dead", h.cfg.Queue.DeadLetter)
	require.NotNil(t, h.dead)
}

func TestServerOptionsReachTheListener(t *testing.T) {
	var configured bool
	h := newHarness(t, WithServerOptions(rpc.WithHTTPServer(func(*http.Server) {
		configured = true
	})))

	require.NoError(t, h.Start(t.Context()))
	require.True(t, configured, "a ServerOption given to New must reach rpc.Listen")
}

func TestCloseAbandonsTheRunInFlightRatherThanFailingTheJob(t *testing.T) {
	h := newHarnessCfg(t, func(c *Config) {
		c.ClaimInterval = time.Millisecond
	})
	id := h.planned(t, testRef, testCommit)

	started := make(chan struct{})
	h.runner.run = func(ctx context.Context, _ Work, _ Output) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}

	require.NoError(t, h.Start(t.Context()))
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the claim loop never ran the job")
	}
	require.NoError(t, h.Close())

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusClaimed, rec.status(),
		"a shutdown must not write a terminal status for a build nothing finished")
	require.Empty(t, rec.Reason)

	ready, claimed := h.work.Depth()
	require.Equal(t, 0, ready)
	require.Equal(t, 1, claimed, "an abandoned delivery is left for its claim to expire")

	// And the claim expires, and the queue hands the job to the next claimer.
	h.clock.advance(h.cfg.Queue.VisibilityTimeout + time.Second)
	d, err := h.work.Claim()
	require.NoError(t, err)
	require.Equal(t, id, d.ID)
	require.Equal(t, uint32(2), d.Attempt, "the interrupted delivery cost an attempt")
}

func TestARunThatFinishedDuringAShutdownIsStillSettled(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	// The loop's context as Close leaves it, while a Runner that does not watch
	// it finishes anyway.
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(errShuttingDown)

	worked, err := h.step(ctx)
	require.NoError(t, err)
	require.True(t, worked)

	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, h.status(t, id))
	require.Equal(t, testCommit, h.lastCommit(testRepo, testRef, testEnvironment))

	ready, claimed := h.work.Depth()
	require.Equal(t, 0, ready)
	require.Equal(t, 0, claimed, "a finished run is acknowledged even on the way out")
}
