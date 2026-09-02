package backend

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/raft"
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
