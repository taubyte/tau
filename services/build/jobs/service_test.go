package jobs

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
	"github.com/taubyte/tau/services/build/run"
)

func TestServiceNeedsOnlyAMocknetHostAMockClusterAndAFakeAuth(t *testing.T) {
	s, err := newServer(Config{}, withHost(newHost(t, newMocknet(t))), withStore(raft.NewMockCluster()), withRecords(newRecords(t)), withRepositories(newRepositories()))
	require.NoError(t, err)
	require.NoError(t, s.Close())
}

func TestNewRefusesWhatItCannotBeBuiltFrom(t *testing.T) {
	mn := newMocknet(t)
	h := newHost(t, mn)
	store := newStore()
	records := withRecords(newRecords(t))
	repos := newRepositories()

	for name, tc := range map[string]func() (*server, error){
		"no host": func() (*server, error) {
			return newServer(Config{}, withStore(store), records, withRepositories(repos))
		},
		"a nil host":  func() (*server, error) { return newServer(Config{}, withHost(nil)) },
		"no store":    func() (*server, error) { return newServer(Config{}, withHost(h), records, withRepositories(repos)) },
		"a nil store": func() (*server, error) { return newServer(Config{}, withStore(nil)) },
		"no records": func() (*server, error) {
			return newServer(Config{}, withHost(h), withStore(store), withRepositories(repos))
		},
		"a nil records": func() (*server, error) { return newServer(Config{}, withRecords(nil)) },
		"no repositories": func() (*server, error) {
			return newServer(Config{}, withHost(h), withStore(store), records)
		},
		"a nil repositories": func() (*server, error) { return newServer(Config{}, withRepositories(nil)) },
		"a lease shorter than a build": func() (*server, error) {
			return newServer(Config{
				BuildTimeout: time.Hour,
				Queue:        queue.Config{VisibilityTimeout: time.Minute},
			}, withHost(h), withStore(store), records, withRepositories(repos))
		},
		"a negative tunable": func() (*server, error) {
			return newServer(Config{ClaimInterval: -1}, withHost(h), withStore(store), records, withRepositories(repos))
		},
		"an apply timeout the queue refuses": func() (*server, error) {
			return newServer(Config{
				Queue: queue.Config{ApplyTimeout: raft.MaxApplyTimeout + time.Second},
			}, withHost(h), withStore(store), records, withRepositories(repos))
		},
		"an option that fails": func() (*server, error) {
			return newServer(Config{}, func(*options) error { return errors.New("no") })
		},
		"a nil runner":     func() (*server, error) { return newServer(Config{}, withRunner(nil)) },
		"a nil authorizer": func() (*server, error) { return newServer(Config{}, withAuthorizer(nil)) },
		"a nil asset store": func() (*server, error) {
			return newServer(Config{}, withAssets(nil))
		},
		"a nil clock": func() (*server, error) { return newServer(Config{}, withClock(nil)) },
	} {
		t.Run(name, func(t *testing.T) {
			s, err := tc()
			require.Error(t, err)
			require.Nil(t, s)
		})
	}
}

func TestNewServerBindsNothing(t *testing.T) {
	h := newHarness(t)
	require.Nil(t, h.server.server, "no listener until it serves")

	// The protocol is free until Start takes it, which is what a second
	// listener on the same host proves.
	srv, err := rpc.Listen(t.Context(), h.host, build.Protocol, http.NotFoundHandler())
	require.NoError(t, err)
	require.NoError(t, srv.Close())
}

func TestServeTwiceIsAnErrorAndSoIsServeAfterClose(t *testing.T) {
	h := newHarness(t)

	require.NoError(t, h.serve(t.Context()))
	require.ErrorContains(t, h.serve(t.Context()), "already started")

	require.NoError(t, h.Close())
	require.NoError(t, h.Close(), "Close is idempotent")
	require.ErrorContains(t, h.serve(t.Context()), "closed")
}

func TestASecondListenerOnTheProtocolIsRefused(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, h.serve(t.Context()))

	other, err := newServer(Config{}, withHost(h.host), withStore(newStore()), withRecords(newRecords(t)), withRepositories(newRepositories()))
	require.NoError(t, err)
	t.Cleanup(func() { other.Close() })

	require.ErrorIs(t, other.serve(t.Context()), rpc.ErrProtocolTaken)
}

func TestCloseStopsTheClaimLoop(t *testing.T) {
	h := newHarnessCfg(t, func(c *Config) {
		c.ClaimInterval = time.Millisecond
		c.ReconcileEvery = time.Millisecond
	})

	require.NoError(t, h.serve(t.Context()))
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

	require.NotEmpty(t, h.records.keys(t, eventPrefix(id)))
}

func TestTheQueueAndItsDeadLetterAreNamedFromTheDomain(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, build.QueueDomain, h.cfg.Queue.Name)
	require.Equal(t, "build/queue/dead", h.cfg.Queue.DeadLetter)
	require.NotNil(t, h.dead)
}

func TestServerOptionsReachTheListener(t *testing.T) {
	var configured bool
	h := newHarness(t, withServerOptions(rpc.WithHTTPServer(func(*http.Server) {
		configured = true
	})))

	require.NoError(t, h.serve(t.Context()))
	require.True(t, configured, "a ServerOption given to New must reach rpc.Listen")
}

func TestCloseAbandonsTheRunInFlightRatherThanFailingTheJob(t *testing.T) {
	h := newHarnessCfg(t, func(c *Config) {
		c.ClaimInterval = time.Millisecond
	})
	id := h.planned(t, testRef, testCommit)

	started := make(chan struct{})
	h.runner.run = func(ctx context.Context, _ run.Work, _ run.Output) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}

	require.NoError(t, h.serve(t.Context()))
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
