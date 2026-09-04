package frontend

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
	buildv1 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v1"
)

// The four routes v1 guards with "does this bearer hold a working GitHub
// token", after which no handler reads the resulting client.

func TestGetJobAnswersTheSchemasOwnShape(t *testing.T) {
	h := newHarness(t, Config{})

	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken))

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "application/json", w.Header().Get("Content-Type"))
	require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))

	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	// Proto field names, because the body is protojson over the generated
	// message rather than a struct typed a second time beside it.
	require.Equal(t, testJobID, got["id"])
	require.Equal(t, testRepoKey, got["repository_key"])
	require.Equal(t, "JobStatusSuccess", got["status"])
	require.Equal(t, map[string]any{"resource-1": "bafyasset"}, got["asset_cid"])
}

func TestListJobsStreamsAJSONArray(t *testing.T) {
	h := newHarness(t, Config{})
	h.backend.list = func(_ string, emit func(string) error) error {
		for _, id := range []string{"a", "b", "c"} {
			if err := emit(id); err != nil {
				return err
			}
		}
		return nil
	}

	w := h.do(t, authed(http.MethodGet, "/jobs/"+testProject, testToken))

	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `["a","b","c"]`, bodyOf(t, w))
}

func TestAProjectWithNoJobsIsAnEmptyArrayAndNotNull(t *testing.T) {
	h := newHarness(t, Config{})
	h.backend.list = func(string, func(string) error) error { return nil }

	w := h.do(t, authed(http.MethodGet, "/jobs/"+testProject, testToken))

	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `[]`, bodyOf(t, w))
}

// A failure before the first id can still choose a status. One after it cannot,
// so the array is left open and a decoder reports truncation rather than
// accepting a short list as complete.
func TestAListThatBreaksAfterItStartedIsLeftVisiblyTruncated(t *testing.T) {
	h := newHarness(t, Config{})
	h.backend.list = func(_ string, emit func(string) error) error {
		if err := emit("a"); err != nil {
			return err
		}
		return connect.NewError(connect.CodeUnavailable, errors.New("leader lost"))
	}

	w := h.do(t, authed(http.MethodGet, "/jobs/"+testProject, testToken))

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, `["a"`, bodyOf(t, w))
	require.Error(t, json.Unmarshal(w.Body.Bytes(), &[]string{}))
	require.Contains(t, h.logs.joined(), "leader lost")
}

func TestAListThatBreaksBeforeItStartedStillChoosesAStatus(t *testing.T) {
	h := newHarness(t, Config{})
	h.backend.list = func(string, func(string) error) error {
		return connect.NewError(connect.CodeUnavailable, errors.New("no backend"))
	}

	w := h.do(t, authed(http.MethodGet, "/jobs/"+testProject, testToken))

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestCancelSaysNothingAndRetrySaysQueued(t *testing.T) {
	h := newHarness(t, Config{})

	cancel := h.do(t, authed(http.MethodPost, "/cancel/"+testJobID, testToken))
	require.Equal(t, http.StatusNoContent, cancel.Code)
	require.Empty(t, bodyOf(t, cancel))

	// 202, not 204: the job is queued, not finished. v1 answers both with
	// {"cancelled": jid} / {"retry": jid} — the id the caller just sent.
	retry := h.do(t, authed(http.MethodPost, "/retry/"+testJobID, testToken))
	require.Equal(t, http.StatusAccepted, retry.Code)
	require.Empty(t, bodyOf(t, retry))
}

// v1's retry answered success without doing anything when the job was still
// open. The backend answers FailedPrecondition; it must reach the caller as a
// conflict and not as a success.
func TestRetryingARunningJobIsAConflict(t *testing.T) {
	h := newHarness(t, Config{})
	h.backend.retry = func(string) error {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("job is Claimed, not finished"))
	}

	w := h.do(t, authed(http.MethodPost, "/retry/"+testJobID, testToken))

	require.Equal(t, http.StatusConflict, w.Code)
}

// The read that authorizes happens before the mutation, so a caller who may not
// touch a job cannot cancel it and find out afterwards that they could not.
func TestAMutationIsAuthorizedBeforeItHappens(t *testing.T) {
	for _, route := range []string{"/cancel/", "/retry/"} {
		t.Run(route, func(t *testing.T) {
			h := newHarness(t, Config{})
			h.identity.write = func(string, repokey.Key) (Access, error) { return AccessDenied, nil }

			acted := false
			h.backend.cancel = func(string) error { acted = true; return nil }
			h.backend.retry = func(string) error { acted = true; return nil }

			w := h.do(t, authed(http.MethodPost, route+testJobID, testToken))

			require.Equal(t, http.StatusNotFound, w.Code)
			require.False(t, acted)
		})
	}
}

func TestAnUnknownJobAndADeniedOneAreTheSameAnswer(t *testing.T) {
	h := newHarness(t, Config{})
	h.backend.get = func(id string) (*buildv1.Job, error) {
		if id == "unknown" {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("no job"))
		}
		return &buildv1.Job{Id: id, RepositoryKey: "github.com/someone/else"}, nil
	}
	h.identity.write = func(_ string, key repokey.Key) (Access, error) {
		if key.String() == testRepoKey {
			return AccessGranted, nil
		}
		return AccessDenied, nil
	}

	unknown := h.do(t, authed(http.MethodGet, "/job/unknown", testToken))
	denied := h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken))

	require.Equal(t, http.StatusNotFound, unknown.Code)
	require.Equal(t, http.StatusNotFound, denied.Code)
	require.Equal(t, bodyOf(t, unknown), bodyOf(t, denied))
}

func TestAnIdTheBackendCouldNotHaveMintedIsABadRequest(t *testing.T) {
	h := newHarness(t, Config{})
	h.backend.get = func(id string) (*buildv1.Job, error) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%q is not a job id", id))
	}

	w := h.do(t, authed(http.MethodGet, "/job/not-an-id", testToken))
	require.Equal(t, http.StatusBadRequest, w.Code)
}

// A caller-chosen path segment that leaves this process is bounded before it
// does. The backend refuses anything but a 43-character id; it should not have
// to receive a megabyte to say so.
func TestAnOversizedIdNeverReachesTheBackend(t *testing.T) {
	for _, path := range []string{"/job/", "/jobs/", "/cancel/", "/retry/"} {
		t.Run(path, func(t *testing.T) {
			h := newHarness(t, Config{})
			reached := false
			h.backend.get = func(string) (*buildv1.Job, error) { reached = true; return nil, nil }
			h.backend.list = func(string, func(string) error) error { reached = true; return nil }
			h.projects.repositories = func(string) ([]string, error) { reached = true; return nil, nil }

			method := http.MethodGet
			if strings.HasPrefix(path, "/cancel") || strings.HasPrefix(path, "/retry") {
				method = http.MethodPost
			}
			w := h.do(t, authed(method, path+strings.Repeat("x", maxIDLength+1), testToken))

			require.Equal(t, http.StatusNotFound, w.Code)
			require.False(t, reached)
		})
	}
}

// ── what a request costs before anything is proven ───────────────────────────

// bearer() checks the SHAPE of the Authorization header and nothing else, so
// "Bearer x" reaches mayTouchJob — which has to call the backend first, because
// the job's repository_key IS the authorization subject. Every backend read
// goes through its lead(), and pkg/raft/cluster.go's Barrier is
// hashicorp/raft's: api.go puts a logFuture{log: Log{Type: LogBarrier}} on
// applyCh and raft.go's leaderLoop hands it to dispatchLogs. That is a log
// entry replicated to quorum, not a local read.
//
// So one stranger's HTTP request costs the whole consistency domain one
// replicated entry. webhook.go's own doc comment names this defect and closes
// it for the webhook route with two limits; these four had none.
func TestNoJobRouteSpendsTheCloudOnceForEveryRequest(t *testing.T) {
	const flood = 1000

	countGets := func(h *harness, n *int) {
		h.backend.get = func(string) (*buildv1.Job, error) {
			*n++
			return nil, connect.NewError(connect.CodeNotFound, errors.New("no job"))
		}
	}

	for _, tc := range []struct {
		name   string
		method string
		path   string
		count  func(*harness, *int)
	}{
		{"GET /job", http.MethodGet, "/job/" + testJobID, countGets},
		{"POST /cancel", http.MethodPost, "/cancel/" + testJobID, countGets},
		{"POST /retry", http.MethodPost, "/retry/" + testJobID, countGets},
		{"GET /jobs", http.MethodGet, "/jobs/" + testProject, func(h *harness, n *int) {
			h.projects.repositories = func(string) ([]string, error) {
				*n++
				return nil, connect.NewError(connect.CodeNotFound, errors.New("no project"))
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, Config{})
			reached := 0
			tc.count(h, &reached)

			limited := 0
			for range flood {
				if h.do(t, authed(tc.method, tc.path, "x")).Code == http.StatusTooManyRequests {
					limited++
				}
			}

			require.Less(t, reached, flood, "a stranger reached the cloud once per request")
			require.Positive(t, limited, "nothing was ever refused")
		})
	}
}

// Retry is the one route that CREATES WORK — it queues a build — so it is the
// one where a cap that was skipped costs more than a read. A tenant holding
// write access to one of their own repositories must not be able to ask for
// builds as fast as it can open connections.
func TestRetryIsUnderTheSameCapsAsEveryOtherJobRoute(t *testing.T) {
	const flood = 1000

	h := newHarness(t, Config{})
	queued := 0
	h.backend.retry = func(string) error { queued++; return nil }

	limited := 0
	for range flood {
		if h.do(t, authed(http.MethodPost, "/retry/"+testJobID, testToken)).Code == http.StatusTooManyRequests {
			limited++
		}
	}

	require.Less(t, queued, flood, "a tenant queued one build per request")
	require.Positive(t, limited, "nothing was ever refused")
}

// admitted() is the only thing in this package that turns a job route's handler
// into one the mux will take, and these four lines are that guarantee restated
// where a reader looks for it rather than only where it is enforced: the day one
// of them can be mounted directly, this file stops compiling.
var _ = []jobHandler{
	(&Service{}).handleGetJob,
	(&Service{}).handleListJobs,
	(&Service{}).handleCancelJob,
	(&Service{}).handleRetryJob,
}

// The per-caller limiter is keyed by a string the stranger chooses, so a fresh
// bearer token buys a fresh bucket. Only the unkeyable one can stop that — the
// same sentence ratelimit.go makes about invented hook ids, on the surface
// where the cost is a replicated raft entry rather than a lookup.
func TestAFloodOfInventedCredentialsMeetsTheGlobalJobLimit(t *testing.T) {
	h := newHarness(t, Config{
		JobRate:    Rate{Burst: 2, Per: time.Minute},
		CallerRate: Rate{Burst: 100, Per: time.Minute},
	})
	asked := 0
	h.backend.get = func(string) (*buildv1.Job, error) {
		asked++
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no job"))
	}

	codes := []int{}
	for i := range 4 {
		codes = append(codes, h.do(t, authed(http.MethodGet, "/job/"+testJobID, fmt.Sprintf("invented-%d", i))).Code)
	}

	require.Equal(t, []int{
		http.StatusNotFound, http.StatusNotFound,
		http.StatusTooManyRequests, http.StatusTooManyRequests,
	}, codes)
	require.Equal(t, 2, asked, "two barriers, not four")
}

func TestOneCredentialsBurstDoesNotSpendTheWholeJobAPI(t *testing.T) {
	h := newHarness(t, Config{
		CallerRate: Rate{Burst: 2, Per: time.Minute},
		JobRate:    Rate{Burst: 100, Per: time.Minute},
	})

	for range 2 {
		require.Equal(t, http.StatusOK, h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken)).Code)
	}
	limited := h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken))
	require.Equal(t, http.StatusTooManyRequests, limited.Code)
	require.Equal(t, "60", limited.Header().Get("Retry-After"), "the caller is told how long to wait")

	// Another credential is unaffected: the limit is per caller, not per node.
	require.Equal(t, http.StatusNotFound, h.do(t, authed(http.MethodGet, "/job/"+testJobID, "someone-else")).Code)

	// And the bucket refills on the clock, not on a sleep.
	h.clock.advance(time.Minute)
	require.Equal(t, http.StatusOK, h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken)).Code)
}

// The order inside credentials() is the fix, not just the presence of a
// limiter. A request refused by bearer() or pathID() has cost this cloud
// nothing, so it must not spend a token: if it did, a stranger could empty the
// global bucket with requests that ask for nothing and lock out every caller
// whose request would have been served — the rate limit doing the job it exists
// to prevent.
func TestARequestThatCostsNothingSpendsNoLimit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request func() *http.Request
	}{
		{"no credential", func() *http.Request {
			return authed(http.MethodGet, "/job/"+testJobID, "")
		}},
		{"a credential that is only a scheme", func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/job/"+testJobID, nil)
			r.Header.Set("Authorization", bearerPrefix+"   ")
			return r
		}},
		{"an oversized id", func() *http.Request {
			return authed(http.MethodGet, "/job/"+strings.Repeat("x", maxIDLength+1), testToken)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, Config{
				JobRate:    Rate{Burst: 1, Per: time.Minute},
				CallerRate: Rate{Burst: 1, Per: time.Minute},
			})

			for range 100 {
				require.NotEqual(t, http.StatusTooManyRequests, h.do(t, tc.request()).Code)
			}

			// The one token in each bucket is still there for a caller who
			// actually asks for something.
			require.Equal(t, http.StatusOK, h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken)).Code)
		})
	}
}

// Two public surfaces, four limits, and neither surface's flood may spend the
// other's budget. A webhook refused is a delivery GitHub stops retrying; a job
// route refused is a CLI that cannot read its own build. They are not
// interchangeable, so they do not share a ceiling.
func TestTheTwoPublicSurfacesDoNotSpendEachOthersBudget(t *testing.T) {
	exhausting := Config{
		JobRate:     Rate{Burst: 1, Per: time.Minute},
		CallerRate:  Rate{Burst: 1, Per: time.Minute},
		WebhookRate: Rate{Burst: 1, Per: time.Minute},
		HookRate:    Rate{Burst: 1, Per: time.Minute},
	}

	t.Run("a job flood leaves the webhook alone", func(t *testing.T) {
		h := newHarness(t, exhausting)
		for range 10 {
			h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken))
		}
		require.Equal(t, http.StatusTooManyRequests, h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken)).Code)

		require.Equal(t, http.StatusAccepted, h.do(t, webhookRequest(webhookOpts{})).Code)
	})

	t.Run("a webhook flood leaves the job API alone", func(t *testing.T) {
		h := newHarness(t, exhausting)
		for range 10 {
			h.do(t, webhookRequest(webhookOpts{}))
		}
		require.Equal(t, http.StatusTooManyRequests, h.do(t, webhookRequest(webhookOpts{})).Code)

		require.Equal(t, http.StatusOK, h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken)).Code)
	})
}
