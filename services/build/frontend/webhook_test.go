package frontend

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
)

// A hostile caller gets one of these. The friendly case is one test; the rest
// of this file is what a stranger can try.

func TestAVerifiedPushIsQueuedAndTheCallerIsToldNothing(t *testing.T) {
	h := newHarness(t, Config{})

	w := h.do(t, webhookRequest(webhookOpts{}))

	require.Equal(t, http.StatusAccepted, w.Code)
	// v1 returns the whole Job — id included — to whoever posted the webhook,
	// and its unauthenticated /download route is keyed by that id.
	require.Empty(t, bodyOf(t, w))
	require.Equal(t, []enqueued{{testRepoKey, "refs/heads/main", testSHA}}, h.backend.enqueues())
	// The id is logged, where an operator tracing a delivery can find it.
	require.Contains(t, h.logs.joined(), testJobID)
}

// pkg/http resolves a route variable from the QUERY before the path, so on
// every v1 route with one, ?hook= picks the HMAC key. net/http's PathValue
// cannot be reached that way.
// WHAT THE FORGE SAYS A DELIVERY IS decides what this role does with it — and
// nothing is queued unless the answer is a push.
func TestEachAnswerAboutADeliveryHasItsOwnStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		says   string
	}{
		{"not genuine", fmt.Errorf("%w: signature", ErrUnverified), http.StatusForbidden, ""},
		{"a ping", ErrNotAPush, http.StatusAccepted, "not a push event"},
		{"a deleted branch", ErrRefDeleted, http.StatusAccepted, "ref deleted"},
		{"genuine and unreadable", fmt.Errorf("%w: no repository", ErrMalformed), http.StatusBadRequest, ""},
		{"a hook this cloud cannot check", errors.New("no driver for host"), http.StatusInternalServerError, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, Config{})
			h.deliveries.verify = func(*authv2.Hook, http.Header, []byte) (Push, error) { return Push{}, tc.err }

			w := h.do(t, webhookRequest(webhookOpts{}))

			require.Equal(t, tc.status, w.Code)
			require.Equal(t, tc.says, w.Header().Get("X-Build-Ignored"))
			require.Empty(t, h.backend.enqueues())
		})
	}
}

// THE REPOSITORY QUEUED IS THE ONE THE VERIFICATION VOUCHED FOR, never one the
// body named: the forge binds a push to the hook's own repository, and this
// role passes that on without reading the payload itself.
func TestWhatIsQueuedIsWhatWasVerified(t *testing.T) {
	h := newHarness(t, Config{})
	h.deliveries.verify = func(hook *authv2.Hook, _ http.Header, _ []byte) (Push, error) {
		return Push{Repository: "gitlab.example.com/group/sub/pay", Ref: "refs/heads/main", Commit: testSHA}, nil
	}

	require.Equal(t, http.StatusAccepted, h.do(t, webhookRequest(webhookOpts{})).Code)
	require.Equal(t, []enqueued{{"gitlab.example.com/group/sub/pay", "refs/heads/main", testSHA}}, h.backend.enqueues())
}

func TestAQueryParameterCannotChooseTheHook(t *testing.T) {
	h := newHarness(t, Config{})
	asked := []string{}
	h.hooks.get = func(id string) (*authv2.Hook, error) {
		asked = append(asked, id)
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such hook"))
	}

	r := webhookRequest(webhookOpts{})
	r.URL.RawQuery = "hook=forged"
	w := h.do(t, r)

	require.Equal(t, http.StatusNotFound, w.Code)
	require.Equal(t, []string{testHookID}, asked)
}

// gopkg.in/go-playground/webhooks.v5 skips the MAC entirely when the secret is
// empty, and auth's own p2p registration path hands out a hard-coded one. A
// hook this cloud cannot authenticate must fail closed.
func TestAnEmptySecretIsAFailureAndNeverASkippedMAC(t *testing.T) {
	h := newHarness(t, Config{})
	h.hooks.get = func(id string) (*authv2.Hook, error) {
		return &authv2.Hook{Id: id, RepositoryKey: testRepoKey}, nil
	}

	w := h.do(t, webhookRequest(webhookOpts{omitSig: true}))

	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Empty(t, h.backend.enqueues())
	require.Contains(t, h.logs.joined(), "no secret")
}

func TestTheBodyIsBoundedBeforeItIsRead(t *testing.T) {
	h := newHarness(t, Config{MaxBodyBytes: 64})

	// Well past the cap, and validly signed: the cap is not a signature check,
	// it is a memory bound, so it has to bite before anything is verified.
	big := pushBody("refs/heads/main/"+strings.Repeat("x", 64), testSHA)
	require.Greater(t, len(big), 64)

	w := h.do(t, webhookRequest(webhookOpts{body: big}))

	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	// Nothing was verified, nothing was parsed and nothing was queued: the read
	// is where this stops.
	require.Empty(t, h.backend.enqueues())
}

// auth creates every hook with ContentType "json"
// (services/auth/github_client.go), so the form encoding is a second parser for
// a stranger's bytes that this cloud never asks for.
func TestOnlyJSONIsParsed(t *testing.T) {
	for _, contentType := range []string{"application/x-www-form-urlencoded", "text/plain", "application/json; boundary"} {
		t.Run(contentType, func(t *testing.T) {
			h := newHarness(t, Config{})
			w := h.do(t, webhookRequest(webhookOpts{contentType: contentType}))
			require.Equal(t, http.StatusUnsupportedMediaType, w.Code)
			require.Zero(t, h.hooks.count())
		})
	}
}

func TestAMissingContentTypeIsRefused(t *testing.T) {
	h := newHarness(t, Config{})
	r := webhookRequest(webhookOpts{})
	r.Header.Del("Content-Type")

	w := h.do(t, r)

	require.Equal(t, http.StatusUnsupportedMediaType, w.Code)
	require.Zero(t, h.hooks.count())
}

func TestAJSONContentTypeWithParametersIsAccepted(t *testing.T) {
	h := newHarness(t, Config{})
	w := h.do(t, webhookRequest(webhookOpts{contentType: "application/json; charset=utf-8"}))
	require.Equal(t, http.StatusAccepted, w.Code)
}

func TestAnUnknownHookIsNotFound(t *testing.T) {
	h := newHarness(t, Config{})
	w := h.do(t, webhookRequest(webhookOpts{hookID: "nobody"}))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Empty(t, h.backend.enqueues())
}

func TestAnOversizedHookIdNeverReachesAuth(t *testing.T) {
	h := newHarness(t, Config{})
	w := h.do(t, webhookRequest(webhookOpts{hookID: strings.Repeat("a", maxHookIDLength+1)}))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Zero(t, h.hooks.count())
}

func TestARefThisCloudDoesNotBuildIsAcceptedAndIgnored(t *testing.T) {
	h := newHarness(t, Config{})
	body := pushBody("refs/heads/feature/x", testSHA)

	w := h.do(t, webhookRequest(webhookOpts{body: body, signature: testSecret}))

	require.Equal(t, http.StatusAccepted, w.Code)
	require.Equal(t, "ref is not built", w.Header().Get("X-Build-Ignored"))
	require.Empty(t, h.backend.enqueues())
}

func TestTheEligibleRefListIsTheOperatorsAndNotV1s(t *testing.T) {
	h := newHarness(t, Config{EligibleRefs: []string{"refs/heads/trunk"}})
	body := pushBody("refs/heads/trunk", testSHA)

	w := h.do(t, webhookRequest(webhookOpts{body: body, signature: testSecret}))
	require.Equal(t, http.StatusAccepted, w.Code)
	require.Len(t, h.backend.enqueues(), 1)

	// And main is no longer eligible, because the list REPLACES the default
	// rather than extending it.
	main := pushBody("refs/heads/main", testSHA)
	w = h.do(t, webhookRequest(webhookOpts{body: main, signature: testSecret}))
	require.Equal(t, http.StatusAccepted, w.Code)
	require.Len(t, h.backend.enqueues(), 1)
}

func TestABackendRefusalReachesTheCallerAsAStatusAndNotAsWords(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"queue full", connect.NewError(connect.CodeResourceExhausted, errors.New("build queue is full (10000)")), http.StatusServiceUnavailable},
		{"no leader", connect.NewError(connect.CodeUnavailable, errors.New("not the leader of build/queue")), http.StatusServiceUnavailable},
		{"unregistered", connect.NewError(connect.CodeFailedPrecondition, errors.New("repository is not registered")), http.StatusConflict},
		{"bad ref", connect.NewError(connect.CodeInvalidArgument, errors.New("ref is not a branch ref")), http.StatusBadRequest},
		{"frontend not a peer", connect.NewError(connect.CodeUnauthenticated, errors.New("not over a libp2p stream")), http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, Config{})
			h.backend.enqueue = func(string, string, string) (string, error) { return "", tc.err }

			w := h.do(t, webhookRequest(webhookOpts{}))

			require.Equal(t, tc.want, w.Code)
			// The backend's own words stay in the log and never in the answer.
			require.NotContains(t, bodyOf(t, w), tc.err.Error())
			require.Contains(t, h.logs.joined(), tc.err.Error())
		})
	}
}

func TestTheGlobalLimitIsWhatAFloodOfInventedHookIdsMeets(t *testing.T) {
	h := newHarness(t, Config{
		WebhookRate: Rate{Burst: 2, Per: time.Minute},
		HookRate:    Rate{Burst: 100, Per: time.Minute},
	})

	// Every request names a DIFFERENT hook, so the per-hook limiter hands each
	// one a fresh bucket and never fires. Only the global one can stop this.
	codes := []int{}
	for i := range 4 {
		w := h.do(t, webhookRequest(webhookOpts{hookID: fmt.Sprintf("invented-%d", i)}))
		codes = append(codes, w.Code)
	}

	require.Equal(t, []int{http.StatusNotFound, http.StatusNotFound, http.StatusTooManyRequests, http.StatusTooManyRequests}, codes)
	// Two round trips to auth, not four.
	require.Equal(t, 2, h.hooks.count())
}

func TestOneHooksBurstDoesNotSpendTheWholeNode(t *testing.T) {
	h := newHarness(t, Config{
		HookRate:    Rate{Burst: 2, Per: time.Minute},
		WebhookRate: Rate{Burst: 100, Per: time.Minute},
	})

	for range 2 {
		require.Equal(t, http.StatusAccepted, h.do(t, webhookRequest(webhookOpts{})).Code)
	}
	limited := h.do(t, webhookRequest(webhookOpts{}))
	require.Equal(t, http.StatusTooManyRequests, limited.Code)
	require.Equal(t, "60", limited.Header().Get("Retry-After"))

	// Another hook is unaffected: the limit is per hook, not per node.
	other := h.do(t, webhookRequest(webhookOpts{hookID: "hook-2"}))
	require.Equal(t, http.StatusNotFound, other.Code)

	// And the bucket refills on the clock, not on a sleep.
	h.clock.advance(time.Minute)
	require.Equal(t, http.StatusAccepted, h.do(t, webhookRequest(webhookOpts{})).Code)
}

func TestARedeliveredWebhookCostsOneLookupNotTwo(t *testing.T) {
	h := newHarness(t, Config{})

	for range 3 {
		require.Equal(t, http.StatusAccepted, h.do(t, webhookRequest(webhookOpts{})).Code)
	}

	require.Equal(t, 1, h.hooks.count())
	// Three enqueues, and the backend deduplicates them: idempotency lives
	// there, on the job id, and is not imitated here.
	require.Len(t, h.backend.enqueues(), 3)
}

// A Hooks implementation that answers with neither a hook nor an error is a
// bug in something else, and this role fails closed on it rather than reading a
// zero-valued secret out of a nil message.
func TestAHookThatIsNeitherAnAnswerNorAnErrorFailsClosed(t *testing.T) {
	h := newHarness(t, Config{})
	h.hooks.get = func(string) (*authv2.Hook, error) { return nil, nil }

	w := h.do(t, webhookRequest(webhookOpts{}))

	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Empty(t, h.backend.enqueues())
}
