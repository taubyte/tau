package frontend

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
)

// rotatable is a hook whose secret the test changes, the way a relink through
// ANOTHER frontend does: nothing here is told.
func rotatable(h *harness, secret *string, mu *sync.Mutex) {
	h.hooks.get = func(id string) (Hook, error) {
		mu.Lock()
		defer mu.Unlock()
		if *secret == "" {
			return Hook{}, ErrNoHook
		}
		return Hook{Project: id, Repositories: []HookRepository{{Key: testRepoKey, Secret: []byte(*secret)}}}, nil
	}
}

// A ROTATION IS BELIEVED AT THE FIRST DELIVERY UNDER THE NEW SECRET, on a node
// that still remembers the old one, and the old one stops verifying then.
func TestARotatedSecretIsBelievedAtItsFirstDelivery(t *testing.T) {
	h := newHarness(t, Config{})
	var mu sync.Mutex
	secret := "old"
	rotatable(h, &secret, &mu)

	require.Equal(t, http.StatusAccepted, h.do(t, webhookRequest(webhookOpts{signature: "old"})).Code)
	require.Equal(t, 1, h.hooks.count())

	mu.Lock()
	secret = "new"
	mu.Unlock()

	require.Equal(t, http.StatusAccepted, h.do(t, webhookRequest(webhookOpts{signature: "new"})).Code)
	require.Equal(t, 2, h.hooks.count(), "the remembered hook refused it, so it was asked for once more")

	require.Equal(t, http.StatusAccepted, h.do(t, webhookRequest(webhookOpts{signature: "new"})).Code)
	require.Equal(t, 2, h.hooks.count(), "and the answer replaced the remembered one")

	w := h.do(t, webhookRequest(webhookOpts{signature: "old"}))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Len(t, h.backend.enqueues(), 3)
}

// AT MOST ONE LOOKUP PER REQUEST: a fresh answer is never re-asked, and a
// re-asked one is never re-asked again.
func TestARefusedDeliveryCostsAtMostOneLookup(t *testing.T) {
	h := newHarness(t, Config{})

	// Cold: one lookup, refused, not re-asked.
	require.Equal(t, http.StatusNotFound, h.do(t, webhookRequest(webhookOpts{signature: "forged"})).Code)
	require.Equal(t, 1, h.hooks.count())

	// Remembered now (the lookup succeeded): each forged delivery re-asks once.
	for i := range 3 {
		require.Equal(t, http.StatusNotFound, h.do(t, webhookRequest(webhookOpts{signature: "forged"})).Code)
		require.Equal(t, 2+i, h.hooks.count())
	}
}

// Only a delivery that does not VERIFY says anything about the secret.
func TestOnlyAnUnverifiedDeliveryReasks(t *testing.T) {
	for _, err := range []error{ErrMalformed, ErrNotAPush, ErrRefDeleted, errors.New("no driver")} {
		t.Run(err.Error(), func(t *testing.T) {
			h := newHarness(t, Config{})
			h.do(t, webhookRequest(webhookOpts{}))
			h.deliveries.verify = func(Hook, http.Header, []byte) (Push, error) { return Push{}, err }
			h.do(t, webhookRequest(webhookOpts{}))
			require.Equal(t, 1, h.hooks.count())
		})
	}
}

// A project unlinked since it was remembered: the re-ask finds nothing, the
// entry is evicted, and the next delivery asks again.
func TestAReaskThatFindsNothingEvicts(t *testing.T) {
	h := newHarness(t, Config{})
	var mu sync.Mutex
	secret := testSecret
	rotatable(h, &secret, &mu)
	h.do(t, webhookRequest(webhookOpts{}))

	mu.Lock()
	secret = ""
	mu.Unlock()
	require.Equal(t, http.StatusNotFound, h.do(t, webhookRequest(webhookOpts{signature: "forged"})).Code)
	require.Equal(t, 2, h.hooks.count())

	require.Equal(t, http.StatusNotFound, h.do(t, webhookRequest(webhookOpts{})).Code)
	require.Equal(t, 3, h.hooks.count())
}

// A re-ask that cannot reach the cloud is a 503 and leaves the remembered hook
// exactly as it was — its expiry included.
func TestAReaskThatFailsKeepsTheEntryAndItsExpiry(t *testing.T) {
	h := newHarness(t, Config{SecretCacheTTL: time.Minute})
	down := false
	h.hooks.get = func(id string) (Hook, error) {
		if down {
			return Hook{}, connect.NewError(connect.CodeUnavailable, errors.New("down"))
		}
		return Hook{Project: id, Repositories: []HookRepository{{Key: testRepoKey, Secret: []byte(testSecret)}}}, nil
	}
	h.do(t, webhookRequest(webhookOpts{}))

	down = true
	h.clock.advance(30 * time.Second)
	require.Equal(t, http.StatusServiceUnavailable, h.do(t, webhookRequest(webhookOpts{signature: "forged"})).Code)
	require.Equal(t, 2, h.hooks.count())

	require.Equal(t, http.StatusAccepted, h.do(t, webhookRequest(webhookOpts{})).Code)
	require.Equal(t, 2, h.hooks.count(), "still remembered")

	h.clock.advance(30 * time.Second)
	require.Equal(t, http.StatusServiceUnavailable, h.do(t, webhookRequest(webhookOpts{})).Code)
	require.Equal(t, 3, h.hooks.count(), "and expired when it always would have")
}

// A re-ask that returns a hook with no secret is the 500 a cold one would be.
func TestAReaskThatFindsNoSecretIsTheOperators(t *testing.T) {
	h := newHarness(t, Config{})
	var mu sync.Mutex
	secret := testSecret
	h.hooks.get = func(id string) (Hook, error) {
		mu.Lock()
		defer mu.Unlock()
		return Hook{Project: id, Repositories: []HookRepository{{Key: testRepoKey, Secret: []byte(secret)}}}, nil
	}
	h.do(t, webhookRequest(webhookOpts{}))
	mu.Lock()
	secret = ""
	mu.Unlock()
	require.Equal(t, http.StatusInternalServerError, h.do(t, webhookRequest(webhookOpts{signature: "forged"})).Code)
	require.Equal(t, http.StatusInternalServerError, h.do(t, webhookRequest(webhookOpts{})).Code)
	require.Equal(t, 3, h.hooks.count(), "and evicted")
}

// NO LINKED/UNLINKED ORACLE: an absent hook and a forged delivery to a real
// one look the same to the caller.
func TestAnAbsentHookAndABadSignatureAreOneAnswer(t *testing.T) {
	h := newHarness(t, Config{})
	absentW := h.do(t, webhookRequest(webhookOpts{hookID: "nobody-linked-this"}))
	forgedW := h.do(t, webhookRequest(webhookOpts{signature: "forged"}))

	require.Equal(t, absentW.Code, forgedW.Code)
	require.Equal(t, bodyOf(t, absentW), bodyOf(t, forgedW))
	require.Equal(t, absentW.Header(), forgedW.Header())
}

// THE RATE LIMITS PRECEDE THE RE-ASK TOO: it spends its request's token and
// no other, so a refused request is not a free lookup.
func TestTheRateLimitsPrecedeEveryLookup(t *testing.T) {
	h := newHarness(t, Config{HookRate: Rate{Burst: 2, Per: time.Minute}})
	h.do(t, webhookRequest(webhookOpts{}))
	require.Equal(t, http.StatusNotFound, h.do(t, webhookRequest(webhookOpts{signature: "forged"})).Code)
	require.Equal(t, 2, h.hooks.count())

	require.Equal(t, http.StatusTooManyRequests, h.do(t, webhookRequest(webhookOpts{signature: "forged"})).Code)
	require.Equal(t, 2, h.hooks.count())
}

// ── the cache on its own ─────────────────────────────────────────────────────

// gatedFetch blocks each call until the test releases it with an answer.
type gatedFetch struct {
	mu      sync.Mutex
	calls   int
	started chan int
	answers map[int]chan Hook
}

func newGatedFetch() *gatedFetch {
	return &gatedFetch{started: make(chan int, 16), answers: map[int]chan Hook{}}
}

func (g *gatedFetch) answer(n int) chan Hook {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.answers[n] == nil {
		g.answers[n] = make(chan Hook, 1)
	}
	return g.answers[n]
}

func (g *gatedFetch) fetch(context.Context, string) (Hook, error) {
	g.mu.Lock()
	g.calls++
	n := g.calls
	g.mu.Unlock()
	g.started <- n
	return <-g.answer(n), nil
}

func hookWith(secret string) Hook {
	return Hook{Project: testHookID, Repositories: []HookRepository{{Key: testRepoKey, Secret: []byte(secret)}}}
}

func TestConcurrentLookupsShareOneFetch(t *testing.T) {
	g := newGatedFetch()
	c := newHookCache(8, time.Minute, time.Now, g.fetch)

	var wg sync.WaitGroup
	results := make(chan hookLookup, 4)
	for range 4 {
		wg.Go(func() {
			look, err := c.get(t.Context(), testHookID)
			require.NoError(t, err)
			results <- look
		})
	}
	require.Equal(t, 1, <-g.started)
	// A waiter arriving after the flight lands is answered by the cache, so
	// one fetch holds however the goroutines are scheduled.
	g.answer(1) <- hookWith("s")
	wg.Wait()
	close(results)

	for look := range results {
		require.Equal(t, "s", string(look.hook.Repositories[0].Secret))
	}
	require.Equal(t, 1, g.calls)

	look, err := c.get(t.Context(), testHookID)
	require.NoError(t, err)
	require.True(t, look.cached)
}

// AN OLDER FETCH CANNOT OVERWRITE A NEWER ONE. A slow cold lookup that
// answers with the old secret after a refresh answered with the new one
// changes nothing.
func TestAnOlderFetchCannotOverwriteANewerOne(t *testing.T) {
	g := newGatedFetch()
	c := newHookCache(8, time.Minute, time.Now, g.fetch)

	older := make(chan hookLookup, 1)
	go func() {
		look, err := c.get(t.Context(), testHookID)
		require.NoError(t, err)
		older <- look
	}()
	require.Equal(t, 1, <-g.started)

	// A refresh doubting an answer at or after the older fetch's ticket does
	// not join it; it starts its own.
	newer := make(chan hookLookup, 1)
	go func() {
		look, err := c.refresh(t.Context(), testHookID, 1)
		require.NoError(t, err)
		newer <- look
	}()
	require.Equal(t, 2, <-g.started)

	g.answer(2) <- hookWith("new")
	require.Equal(t, "new", string((<-newer).hook.Repositories[0].Secret))
	g.answer(1) <- hookWith("old")
	require.Equal(t, "old", string((<-older).hook.Repositories[0].Secret), "its own caller still gets its answer")

	look, err := c.get(t.Context(), testHookID)
	require.NoError(t, err)
	require.True(t, look.cached)
	require.Equal(t, "new", string(look.hook.Repositories[0].Secret))
}

// A refresh joins a fetch that started after the answer it doubts.
func TestARefreshJoinsANewerFetch(t *testing.T) {
	g := newGatedFetch()
	c := newHookCache(8, time.Minute, time.Now, g.fetch)

	first := make(chan hookLookup, 1)
	go func() {
		look, err := c.get(t.Context(), testHookID)
		require.NoError(t, err)
		first <- look
	}()
	require.Equal(t, 1, <-g.started)

	joined := make(chan hookLookup, 1)
	go func() {
		look, err := c.refresh(t.Context(), testHookID, 0)
		require.NoError(t, err)
		joined <- look
	}()
	// The refresh must have joined before the flight lands for this to
	// prove anything; give it the moment it needs to reach the lock.
	time.Sleep(20 * time.Millisecond)
	g.answer(1) <- hookWith("s")
	<-first
	<-joined
	require.Equal(t, 1, g.calls)
}

// A drop detaches the fetch in flight: whatever made the caller drop may be
// newer than what that fetch read.
func TestADropDetachesTheFetchInFlight(t *testing.T) {
	g := newGatedFetch()
	c := newHookCache(8, time.Minute, time.Now, g.fetch)

	done := make(chan struct{})
	go func() {
		c.get(t.Context(), testHookID)
		close(done)
	}()
	<-g.started
	c.drop(testHookID)
	g.answer(1) <- hookWith("before-the-drop")
	<-done

	go func() { g.answer(2) <- hookWith("after") }()
	look, err := c.get(t.Context(), testHookID)
	require.NoError(t, err)
	require.False(t, look.cached)
	require.Equal(t, "after", string(look.hook.Repositories[0].Secret))
}

// A waiter whose request ends stops waiting; the fetch carries on for the
// others and is still remembered.
func TestAWaiterThatGivesUpDoesNotCancelTheFetch(t *testing.T) {
	g := newGatedFetch()
	c := newHookCache(8, time.Minute, time.Now, g.fetch)

	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() {
		_, err := c.get(ctx, testHookID)
		errc <- err
	}()
	<-g.started
	cancel()
	require.ErrorIs(t, <-errc, context.Canceled)

	g.answer(1) <- hookWith("s")
	require.Eventually(t, func() bool {
		look, err := c.get(t.Context(), testHookID)
		return err == nil && look.cached
	}, time.Second, time.Millisecond)
}
