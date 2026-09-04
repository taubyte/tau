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

	authv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/auth/v1"
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

// THE CENTRAL DEFECT THIS ROLE EXISTS TO CLOSE. The signature is verified with
// a secret chosen by the URL; the repository is named by the body. Without the
// binding, a signature made under any registered hook authorizes a job for any
// registered repository.
func TestASignedPushUnderOneHookCannotQueueAnotherRepository(t *testing.T) {
	h := newHarness(t, Config{})
	h.hooks.get = func(id string) (*authv1.GithubHook, error) {
		// A hook this caller legitimately holds the secret for — for a
		// DIFFERENT repository than the body names.
		return &authv1.GithubHook{Id: id, Secret: testSecret, RepositoryKey: "github.com/acme/other"}, nil
	}

	body := pushBody("https://github.com/acme/pay.git", "refs/heads/main", testSHA)
	w := h.do(t, webhookRequest(webhookOpts{body: body, signature: sign(testSecret, body)}))

	require.Equal(t, http.StatusForbidden, w.Code)
	require.Empty(t, h.backend.enqueues())
	require.Contains(t, h.logs.joined(), "github.com/acme/other")
}

// pkg/http resolves a route variable from the QUERY before the path, so on
// every v1 route with one, ?hook= picks the HMAC key. net/http's PathValue
// cannot be reached that way.
func TestAQueryParameterCannotChooseTheHook(t *testing.T) {
	h := newHarness(t, Config{})
	asked := []string{}
	h.hooks.get = func(id string) (*authv1.GithubHook, error) {
		asked = append(asked, id)
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such hook"))
	}

	r := webhookRequest(webhookOpts{})
	r.URL.RawQuery = "hook=forged"
	w := h.do(t, r)

	require.Equal(t, http.StatusNotFound, w.Code)
	require.Equal(t, []string{testHookID}, asked)
}

func TestTheSignatureMustBeSHA256(t *testing.T) {
	body := pushBody("https://github.com/acme/pay.git", "refs/heads/main", testSHA)

	for _, tc := range []struct {
		name      string
		signature string
		omit      bool
		legacy    bool
	}{
		{name: "absent", omit: true},
		{name: "empty", signature: ""},
		{name: "unprefixed", signature: strings.TrimPrefix(sign(testSecret, body), signaturePrefix)},
		{name: "not hex", signature: signaturePrefix + strings.Repeat("z", 64)},
		{name: "short", signature: signaturePrefix + "00ff"},
		{name: "wrong secret", signature: sign("not-the-secret", body)},
		{name: "sha1 instead", legacy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, Config{})
			r := webhookRequest(webhookOpts{body: body, signature: tc.signature, omitSig: tc.omit || tc.signature == ""})
			if tc.legacy {
				// GitHub still sends the sha1 header beside the sha256 one, and
				// v1 verifies THAT one. Sending only it must not work.
				r.Header.Del(signatureHeader)
				r.Header.Set(legacySignatureHeader, "sha1="+strings.Repeat("ab", 20))
			}

			w := h.do(t, r)
			require.Equal(t, http.StatusForbidden, w.Code)
			require.Empty(t, h.backend.enqueues())
		})
	}
}

// gopkg.in/go-playground/webhooks.v5 skips the MAC entirely when the secret is
// empty, and auth's own p2p registration path hands out a hard-coded one. A
// hook this cloud cannot authenticate must fail closed.
func TestAnEmptySecretIsAFailureAndNeverASkippedMAC(t *testing.T) {
	h := newHarness(t, Config{})
	h.hooks.get = func(id string) (*authv1.GithubHook, error) {
		return &authv1.GithubHook{Id: id, RepositoryKey: testRepoKey}, nil
	}

	body := pushBody("https://github.com/acme/pay.git", "refs/heads/main", testSHA)
	w := h.do(t, webhookRequest(webhookOpts{body: body, signature: sign("", body)}))

	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Empty(t, h.backend.enqueues())
	require.Contains(t, h.logs.joined(), "no secret")
}

func TestTheBodyIsBoundedBeforeItIsRead(t *testing.T) {
	h := newHarness(t, Config{MaxBodyBytes: 64})

	// Well past the cap, and validly signed: the cap is not a signature check,
	// it is a memory bound, so it has to bite before anything is verified.
	big := pushBody("https://github.com/acme/pay.git", "refs/heads/main", testSHA)
	require.Greater(t, len(big), 64)

	w := h.do(t, webhookRequest(webhookOpts{body: big}))

	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	// Nothing was verified, nothing was parsed and nothing was queued: the read
	// is where this stops.
	require.Empty(t, h.backend.enqueues())
}

func TestOnlyPushEventsCostAnything(t *testing.T) {
	h := newHarness(t, Config{})

	w := h.do(t, webhookRequest(webhookOpts{event: "ping"}))

	// 202 rather than an error: GitHub sends `ping` when a hook is created, and
	// v1 answers it with an error, which shows the fresh hook as failing in the
	// provider's own UI.
	require.Equal(t, http.StatusAccepted, w.Code)
	require.Equal(t, "not a push event", w.Header().Get("X-Build-Ignored"))
	// Before the lookup: nothing was asked of auth.
	require.Zero(t, h.hooks.count())
	require.Empty(t, h.backend.enqueues())
}

func TestARequestWithNoEventHeaderIsNotAWebhook(t *testing.T) {
	h := newHarness(t, Config{})
	r := webhookRequest(webhookOpts{})
	r.Header.Del(eventHeader)

	w := h.do(t, r)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Zero(t, h.hooks.count())
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

func TestAHookOfAnotherProviderIsRefusedRatherThanTreatedAsSecretless(t *testing.T) {
	h := newHarness(t, Config{})
	h.hooks.get = func(string) (*authv1.GithubHook, error) {
		// What the clients adapter answers for auth's Hook oneof when it holds
		// a bitbucket hook: a decided refusal, not a nil with no secret.
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("not a github hook"))
	}

	w := h.do(t, webhookRequest(webhookOpts{}))
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestARefThisCloudDoesNotBuildIsAcceptedAndIgnored(t *testing.T) {
	h := newHarness(t, Config{})
	body := pushBody("https://github.com/acme/pay.git", "refs/heads/feature/x", testSHA)

	w := h.do(t, webhookRequest(webhookOpts{body: body, signature: sign(testSecret, body)}))

	require.Equal(t, http.StatusAccepted, w.Code)
	require.Equal(t, "ref is not built", w.Header().Get("X-Build-Ignored"))
	require.Empty(t, h.backend.enqueues())
}

func TestTheEligibleRefListIsTheOperatorsAndNotV1s(t *testing.T) {
	h := newHarness(t, Config{EligibleRefs: []string{"refs/heads/trunk"}})
	body := pushBody("https://github.com/acme/pay.git", "refs/heads/trunk", testSHA)

	w := h.do(t, webhookRequest(webhookOpts{body: body, signature: sign(testSecret, body)}))
	require.Equal(t, http.StatusAccepted, w.Code)
	require.Len(t, h.backend.enqueues(), 1)

	// And main is no longer eligible, because the list REPLACES the default
	// rather than extending it.
	main := pushBody("https://github.com/acme/pay.git", "refs/heads/main", testSHA)
	w = h.do(t, webhookRequest(webhookOpts{body: main, signature: sign(testSecret, main)}))
	require.Equal(t, http.StatusAccepted, w.Code)
	require.Len(t, h.backend.enqueues(), 1)
}

// The repository key that crosses to the backend is DERIVED from the payload's
// clone url, so every spelling of one repository becomes one key — and
// therefore one job id, which is what the backend's idempotency rests on.
func TestEverySpellingOfOneRepositoryBecomesOneKey(t *testing.T) {
	for _, cloneURL := range []string{
		"https://github.com/acme/pay",
		"https://github.com/Acme/Pay.git",
		"https://github.com:443/acme/pay/",
		"ssh://git@github.com/acme/pay",
		"git@github.com:acme/pay.git",
	} {
		t.Run(cloneURL, func(t *testing.T) {
			h := newHarness(t, Config{})
			body := pushBody(cloneURL, "refs/heads/main", testSHA)
			w := h.do(t, webhookRequest(webhookOpts{body: body, signature: sign(testSecret, body)}))

			require.Equal(t, http.StatusAccepted, w.Code)
			require.Equal(t, []enqueued{{testRepoKey, "refs/heads/main", testSHA}}, h.backend.enqueues())
		})
	}
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

func TestAMalformedPayloadIsRefusedAfterItIsAuthenticated(t *testing.T) {
	h := newHarness(t, Config{})
	body := []byte("{not json")

	w := h.do(t, webhookRequest(webhookOpts{body: body, signature: sign(testSecret, body)}))

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Empty(t, h.backend.enqueues())
}

// A Hooks implementation that answers with neither a hook nor an error is a
// bug in something else, and this role fails closed on it rather than reading a
// zero-valued secret out of a nil message.
func TestAHookThatIsNeitherAnAnswerNorAnErrorFailsClosed(t *testing.T) {
	h := newHarness(t, Config{})
	h.hooks.get = func(string) (*authv1.GithubHook, error) { return nil, nil }

	w := h.do(t, webhookRequest(webhookOpts{}))

	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Empty(t, h.backend.enqueues())
}
