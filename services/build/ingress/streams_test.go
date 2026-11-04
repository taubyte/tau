package ingress

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	streamHost = "review.example"
	streamRepo = "review.example/acme/app"
	headA      = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headB      = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	headC      = "cccccccccccccccccccccccccccccccccccccccc"
)

// fakeStreams is a forge feed a test drives: each subscription hands the test
// its emit, and lasts until the test drops it or its context ends.
type fakeStreams struct {
	mu       sync.Mutex
	heads    map[string][]Push
	headsErr map[string]error
	reads    int
	subs     int
	fail     error // a subscription that is never accepted

	connected chan func(Push, error)
	drop      chan struct{}
}

func newFakeStreams() *fakeStreams {
	return &fakeStreams{
		heads:     map[string][]Push{},
		headsErr:  map[string]error{},
		connected: make(chan func(Push, error), 4),
		drop:      make(chan struct{}),
	}
}

func (f *fakeStreams) Hosts() []string { return []string{streamHost} }

func (f *fakeStreams) Stream(ctx context.Context, host string, ready func(), emit func(Push, error)) error {
	f.mu.Lock()
	f.subs++
	fail := f.fail
	f.mu.Unlock()
	if host != streamHost {
		return errors.New("wrong host")
	}
	if fail != nil {
		return fail
	}
	ready()
	f.connected <- emit
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.drop:
		return errors.New("connection reset")
	}
}

func (f *fakeStreams) Heads(_ context.Context, repository string) ([]Push, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return f.heads[repository], f.headsErr[repository]
}

func (f *fakeStreams) set(repository string, heads ...Push) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.heads[repository] = heads
}

func (f *fakeStreams) count() (subs, reads int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.subs, f.reads
}

type fakeLinked struct {
	repos []string
	err   error
}

func (f fakeLinked) LinkedRepositories(context.Context) ([]string, error) { return f.repos, f.err }

func (h *harness) waitEnqueued(t *testing.T, want enqueued) {
	t.Helper()
	require.Eventually(t, func() bool {
		for _, e := range h.backend.enqueues() {
			if e == want {
				return true
			}
		}
		return false
	}, 10*time.Second, 5*time.Millisecond, "never enqueued %+v; enqueued %+v", want, h.backend.enqueues())
}

func startStreaming(t *testing.T, f *fakeStreams, linked Linked, cfg Config) *harness {
	t.Helper()
	cfg.Listen = "127.0.0.1:0"
	h := newHarness(t, cfg, WithStreams(f, linked))
	require.NoError(t, h.Start(t.Context()))
	t.Cleanup(func() { h.Close() })
	return h
}

func TestAStreamedPushIsBuiltAndAMissedOneIsFoundOnReconnect(t *testing.T) {
	f := newFakeStreams()
	f.set(streamRepo,
		Push{Repository: streamRepo, Ref: "refs/heads/main", Commit: headA},
		Push{Repository: streamRepo, Ref: "refs/heads/dev", Commit: headA})
	f.headsErr["review.example/acme/broken"] = errors.New("forbidden")
	linked := fakeLinked{repos: []string{
		"review.example/acme/broken", streamRepo, "Review.Example/acme/app", "github.com/acme/app", "not a key",
	}}
	h := startStreaming(t, f, linked, Config{StreamBackoff: time.Millisecond, StreamResync: time.Hour})

	// THE FIRST SUBSCRIPTION COMPARES HEADS: main is built, dev is not
	// eligible, another forge's repository is not this stream's.
	emit := <-f.connected
	h.waitEnqueued(t, enqueued{streamRepo, "refs/heads/main", headA})
	require.Eventually(t, func() bool {
		return strings.Contains(h.logs.joined(), "reading the heads of review.example/acme/broken")
	}, 5*time.Second, 5*time.Millisecond)

	// A push on the stream goes the webhook's way.
	emit(Push{Repository: streamRepo, Ref: "refs/heads/main", Commit: headB}, nil)
	h.waitEnqueued(t, enqueued{streamRepo, "refs/heads/main", headB})
	emit(Push{Repository: streamRepo, Ref: "refs/heads/dev", Commit: headB}, nil)
	emit(Push{}, errors.New("a claim for another host"))
	require.Contains(t, h.logs.joined(), "refused a claim: a claim for another host")

	// A PUSH WHILE THE STREAM IS DOWN IS ON NO STREAM, and is found by
	// comparing heads once it is back.
	f.set(streamRepo, Push{Repository: streamRepo, Ref: "refs/heads/main", Commit: headC})
	f.drop <- struct{}{}
	<-f.connected
	h.waitEnqueued(t, enqueued{streamRepo, "refs/heads/main", headC})
	require.Contains(t, h.logs.joined(), "connection reset; subscribing again")

	for _, e := range h.backend.enqueues() {
		require.NotEqual(t, "refs/heads/dev", e.ref, "an ineligible ref was built")
		require.Equal(t, streamRepo, e.repositoryKey)
	}

	// Close ends the subscription and waits for it.
	require.NoError(t, h.Close())
	subs, _ := f.count()
	require.Equal(t, 2, subs)
}

func TestAConnectedStreamComparesHeadsAgainPeriodically(t *testing.T) {
	f := newFakeStreams()
	f.set(streamRepo, Push{Repository: streamRepo, Ref: "refs/heads/main", Commit: headA})
	startStreaming(t, f, fakeLinked{repos: []string{streamRepo}}, Config{StreamResync: 5 * time.Millisecond})
	<-f.connected
	require.Eventually(t, func() bool { _, reads := f.count(); return reads >= 3 }, 10*time.Second, 5*time.Millisecond)
}

func TestAStreamThatNeverConnectsBacksOff(t *testing.T) {
	f := newFakeStreams()
	f.fail = errors.New("connection refused")
	h := startStreaming(t, f, fakeLinked{err: errors.New("register down")}, Config{StreamBackoff: time.Millisecond, StreamBackoffMax: 4 * time.Millisecond})
	require.Eventually(t, func() bool { subs, _ := f.count(); return subs >= 5 }, 10*time.Second, time.Millisecond)
	logged := h.logs.joined()
	require.Contains(t, logged, "subscribing again in 1ms")
	require.Contains(t, logged, "subscribing again in 4ms")
	require.NotContains(t, logged, "subscribing again in 8ms", "the backoff is capped")

	// A failing link list is logged and does not end the subscription.
	f.mu.Lock()
	f.fail = nil
	f.mu.Unlock()
	<-f.connected
	require.Eventually(t, func() bool { return strings.Contains(h.logs.joined(), "listing linked repositories: register down") }, 5*time.Second, time.Millisecond)
}

func TestAStreamedPushTheBackendRefusesIsLogged(t *testing.T) {
	f := newFakeStreams()
	h := newHarness(t, Config{Listen: "127.0.0.1:0", StreamResync: time.Hour}, WithStreams(f, fakeLinked{}))
	h.backend.enqueue = func(string, string, string) (string, error) { return "", errors.New("belongs to no project") }
	require.NoError(t, h.Start(t.Context()))
	t.Cleanup(func() { h.Close() })
	emit := <-f.connected
	emit(Push{Repository: streamRepo, Ref: "refs/heads/main", Commit: headA}, nil)
	require.Eventually(t, func() bool { return strings.Contains(h.logs.joined(), "belongs to no project") }, 5*time.Second, time.Millisecond)
}

func TestStreamsNeedBothHalvesAndSaneTimings(t *testing.T) {
	_, err := New(Config{}, WithStreams(nil, fakeLinked{}))
	require.ErrorContains(t, err, "event streams")
	_, err = New(Config{}, WithStreams(newFakeStreams(), nil))
	require.ErrorContains(t, err, "event streams")
	_, err = Config{StreamResync: -1}.withDefaults()
	require.ErrorContains(t, err, "streamResync")

	c, err := Config{StreamBackoff: time.Hour}.withDefaults()
	require.NoError(t, err)
	require.Equal(t, time.Hour, c.StreamBackoffMax, "a ceiling below the first wait is raised to it")
	c, err = Config{}.withDefaults()
	require.NoError(t, err)
	require.Equal(t, DefaultStreamBackoff, c.StreamBackoff)
	require.Equal(t, DefaultStreamResync, c.StreamResync)
}
