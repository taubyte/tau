package backend

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/raft"
	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
	"github.com/taubyte/tau/pkg/v2/queue"
	"github.com/taubyte/tau/services/build"
)

// THE NARROW-DEPENDENCY RULE, AS A TEST rather than as a claim in a doc
// comment. A mocknet host, a raft mock over a map, and a two-line fake for the
// one question this service asks auth. No DHT, no DAG, no gossipsub, no temp
// directory, no kvdb factory, no port, and no raft.New — which takes peer.Node
// and would have made every one of those mandatory.
func TestServiceNeedsOnlyAMocknetHostAMockClusterAndAFakeAuth(t *testing.T) {
	s, err := New(newHost(t, newMocknet(t)), raft.NewMockCluster(), newRepositories(), Config{})
	require.NoError(t, err)
	require.NoError(t, s.Close())
}

func TestNewRefusesWhatItCannotBeBuiltFrom(t *testing.T) {
	mn := newMocknet(t)
	h := newHost(t, mn)
	store := newStore()
	repos := newRepositories()

	for name, tc := range map[string]func() (*Service, error){
		"no host":  func() (*Service, error) { return New(nil, store, repos, Config{}) },
		"no store": func() (*Service, error) { return New(h, nil, repos, Config{}) },
		"no repositories": func() (*Service, error) {
			return New(h, store, nil, Config{})
		},
		"a lease shorter than a build": func() (*Service, error) {
			return New(h, store, repos, Config{
				BuildTimeout: time.Hour,
				Queue:        queue.Config{VisibilityTimeout: time.Minute},
			})
		},
		"a negative tunable": func() (*Service, error) {
			return New(h, store, repos, Config{ClaimInterval: -1})
		},
		"an apply timeout the queue refuses": func() (*Service, error) {
			return New(h, store, repos, Config{
				Queue: queue.Config{ApplyTimeout: raft.MaxApplyTimeout + time.Second},
			})
		},
		"an option that fails": func() (*Service, error) {
			return New(h, store, repos, Config{}, func(*Service) error { return errors.New("no") })
		},
		"a nil runner":     func() (*Service, error) { return New(h, store, repos, Config{}, WithRunner(nil)) },
		"a nil authorizer": func() (*Service, error) { return New(h, store, repos, Config{}, WithAuthorizer(nil)) },
		"a nil clock":      func() (*Service, error) { return New(h, store, repos, Config{}, WithClock(nil)) },
	} {
		t.Run(name, func(t *testing.T) {
			s, err := tc()
			require.Error(t, err)
			require.Nil(t, s)
		})
	}
}

// New binds nothing and opens nothing, so a node can construct every service
// before it starts any of them.
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

// A second listener on one protocol id is refused at bind time, so a taken
// protocol is an error rather than a service that starts and is never reached.
func TestASecondListenerOnTheProtocolIsRefused(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, h.Start(t.Context()))

	other, err := New(h.host, newStore(), newRepositories(), Config{})
	require.NoError(t, err)
	t.Cleanup(func() { other.Close() })

	require.ErrorIs(t, other.Start(t.Context()), rpc.ErrProtocolTaken)
}

// Close stops the loop it started, and waits for it — a loop still running
// after Close would write to a store the composition root is about to close.
func TestCloseStopsTheClaimLoop(t *testing.T) {
	h := newHarness(t, func(s *Service) error {
		s.cfg.ClaimInterval = time.Millisecond
		s.cfg.ReconcileEvery = time.Millisecond
		return nil
	})

	require.NoError(t, h.Start(t.Context()))
	require.NoError(t, h.Close())

	select {
	case <-h.stopped:
	default:
		t.Fatal("Close returned with the loop still running")
	}
}

// Close does not touch the store: Store has no Close, and the composition root
// owns the cluster the services beside this one replicate into.
func TestCloseLeavesTheStoreAlone(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	require.NoError(t, h.Close())

	_, exists := h.store.Get(jobRecordKey(id))
	require.True(t, exists)
}

// The queue's own name is the consistency domain, and its dead letter is that
// name plus the suffix — which is what makes draining it an ordinary claim on
// the same group.
func TestTheQueueAndItsDeadLetterAreNamedFromTheDomain(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, build.QueueDomain, h.cfg.Queue.Name)
	require.Equal(t, "build/queue/dead", h.cfg.Queue.DeadLetter)
	require.NotNil(t, h.dead)
}

// The transport's own knobs reach rpc.Listen without this package growing an
// option per knob.
func TestServerOptionsReachTheListener(t *testing.T) {
	var configured bool
	h := newHarness(t, WithServerOptions(rpc.WithHTTPServer(func(*http.Server) {
		configured = true
	})))

	require.NoError(t, h.Start(t.Context()))
	require.True(t, configured, "a ServerOption given to New must reach rpc.Listen")
}

// A ROLLING RESTART MUST NOT BURN WHATEVER WAS BUILDING. Close ends the loop's
// context, so the run in flight returns rather than holding the process open —
// but the delivery is abandoned, not acknowledged: the item stays claimed, the
// claim expires, and the queue redelivers it to whoever is leader then.
func TestCloseAbandonsTheRunInFlightRatherThanFailingTheJob(t *testing.T) {
	h := newHarness(t, func(s *Service) error {
		s.cfg.ClaimInterval = time.Millisecond
		return nil
	})
	id := h.push(t, testRef, testCommit)

	started := make(chan struct{})
	h.runner.run = func(ctx context.Context, _ Work, _ Sink) error {
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
	require.Equal(t, buildv1.JobStatus_JobStatusClaimed, rec.status(),
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

// The other half of the same rule: a run that RETURNED during a shutdown is a
// run that finished, and it is settled. The abandon is for a run the shutdown
// cut short, not for work this node had already done.
func TestARunThatFinishedDuringAShutdownIsStillSettled(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	// The loop's context as Close leaves it, while a Runner that does not watch
	// it finishes anyway.
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(errShuttingDown)

	worked, err := h.step(ctx)
	require.NoError(t, err)
	require.True(t, worked)

	require.Equal(t, buildv1.JobStatus_JobStatusSuccess, h.status(t, id))
	require.Equal(t, testCommit, h.lastCommit(testRepo, testRef))

	ready, claimed := h.work.Depth()
	require.Equal(t, 0, ready)
	require.Equal(t, 0, claimed, "a finished run is acknowledged even on the way out")
}
