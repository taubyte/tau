package frontend

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
)

func TestAHookIsBelievedForOneTTLAndNoLonger(t *testing.T) {
	h := newHarness(t, Config{SecretCacheTTL: time.Minute})

	h.do(t, webhookRequest(webhookOpts{}))
	h.do(t, webhookRequest(webhookOpts{}))
	require.Equal(t, 1, h.hooks.count())

	// A rotated secret takes effect within the TTL and not before, which is
	// what makes the TTL short.
	h.clock.advance(time.Minute)
	h.do(t, webhookRequest(webhookOpts{}))
	require.Equal(t, 2, h.hooks.count())
}

func TestAMissIsNotRemembered(t *testing.T) {
	h := newHarness(t, Config{})
	linked := false
	h.hooks.get = func(id string) (Hook, error) {
		if !linked {
			return Hook{}, ErrNoHook
		}
		return Hook{Project: id, Repositories: []HookRepository{{Key: testRepoKey, Secret: []byte(testSecret)}}}, nil
	}

	for range 3 {
		require.Equal(t, http.StatusNotFound, h.do(t, webhookRequest(webhookOpts{})).Code)
	}
	require.Equal(t, 3, h.hooks.count())

	linked = true
	require.Equal(t, http.StatusAccepted, h.do(t, webhookRequest(webhookOpts{})).Code)
}

func TestAHookWithNoSecretIsNotRemembered(t *testing.T) {
	h := newHarness(t, Config{})
	h.hooks.get = func(id string) (Hook, error) {
		return Hook{Project: id, Repositories: []HookRepository{{Key: testRepoKey}}}, nil
	}
	for range 2 {
		require.Equal(t, http.StatusInternalServerError, h.do(t, webhookRequest(webhookOpts{})).Code)
	}
	require.Equal(t, 2, h.hooks.count())
}

func TestAFailureToReachAuthIsNotRemembered(t *testing.T) {
	for _, err := range []error{
		connect.NewError(connect.CodeUnavailable, errors.New("no peer answered")),
		connect.NewError(connect.CodeDeadlineExceeded, errors.New("timed out")),
		connect.NewError(connect.CodeInternal, errors.New("boom")),
		errors.New("a plain error"),
	} {
		t.Run(connect.CodeOf(err).String(), func(t *testing.T) {
			h := newHarness(t, Config{})
			h.hooks.get = func(string) (Hook, error) { return Hook{}, err }

			for range 3 {
				require.Equal(t, http.StatusServiceUnavailable, h.do(t, webhookRequest(webhookOpts{})).Code)
			}
			require.Equal(t, 3, h.hooks.count())
		})
	}

	// And once auth answers, the next push works — no stale refusal in the way.
	h := newHarness(t, Config{})
	failing := true
	h.hooks.get = func(id string) (Hook, error) {
		if failing {
			return Hook{}, connect.NewError(connect.CodeUnavailable, errors.New("down"))
		}
		return Hook{Project: id, Repositories: []HookRepository{{Key: testRepoKey, Secret: []byte(testSecret)}}}, nil
	}

	require.Equal(t, http.StatusServiceUnavailable, h.do(t, webhookRequest(webhookOpts{})).Code)
	failing = false
	require.Equal(t, http.StatusAccepted, h.do(t, webhookRequest(webhookOpts{})).Code)
}

func TestTheCacheHasACeiling(t *testing.T) {
	h := newHarness(t, Config{
		SecretCacheMax: 4,
		WebhookRate:    Rate{Burst: 1000, Per: time.Minute},
		HookRate:       Rate{Burst: 1000, Per: time.Minute},
	})
	h.hooks.get = func(id string) (Hook, error) {
		return Hook{Project: id, Repositories: []HookRepository{{Key: testRepoKey, Secret: []byte(testSecret)}}}, nil
	}

	for i := range 8 {
		h.do(t, webhookRequest(webhookOpts{hookID: fmt.Sprintf("hook-%d", i)}))
	}
	require.Equal(t, 8, h.hooks.count())

	// The last four are still remembered; the first four are not.
	h.do(t, webhookRequest(webhookOpts{hookID: "hook-7"}))
	require.Equal(t, 8, h.hooks.count())

	h.do(t, webhookRequest(webhookOpts{hookID: "hook-0"}))
	require.Equal(t, 9, h.hooks.count())
}

func TestAnExpiredEntryIsForgottenRatherThanCounted(t *testing.T) {
	c := newCache[int](2, time.Minute, (&clock{at: time.Unix(0, 0)}).now)
	moving := &clock{at: time.Unix(0, 0)}
	c.now = moving.now

	fetches := 0
	fetch := func() (int, error) { fetches++; return fetches, nil }

	v, err := c.do("a", fetch)
	require.NoError(t, err)
	require.Equal(t, 1, v)

	// Expiry drops the entry AND its slot, so the map does not hold a key it no
	// longer answers with.
	moving.advance(time.Minute)
	v, err = c.do("a", fetch)
	require.NoError(t, err)
	require.Equal(t, 2, v)

	c.mu.Lock()
	defer c.mu.Unlock()
	require.Len(t, c.order, 1)
	require.Len(t, c.items, 1)
}

func TestACacheRemembersNothingThatFailed(t *testing.T) {
	c := newCache[int](4, time.Minute, time.Now)

	fetches := 0
	fetch := func() (int, error) { fetches++; return 0, errors.New("no") }

	for range 3 {
		_, err := c.do("a", fetch)
		require.Error(t, err)
	}
	require.Equal(t, 3, fetches)
}

func TestAbsentNamesTheAnswersThatDeny(t *testing.T) {
	for code, want := range map[connect.Code]bool{
		connect.CodeNotFound:           true,
		connect.CodeInvalidArgument:    true,
		connect.CodeFailedPrecondition: true,
		connect.CodePermissionDenied:   true,
		connect.CodeUnavailable:        false,
		connect.CodeDeadlineExceeded:   false,
		connect.CodeResourceExhausted:  false,
		connect.CodeInternal:           false,
		connect.CodeUnauthenticated:    false,
	} {
		require.Equal(t, want, absent(connect.NewError(code, errors.New("x"))), code.String())
	}
	require.True(t, absent(ErrNoHook))
	require.False(t, absent(errors.New("not a connect error")))
}
