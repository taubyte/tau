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

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/register"
)

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

	// 202, not 204: the job is queued, not finished.
	retry := h.do(t, authed(http.MethodPost, "/retry/"+testJobID, testToken))
	require.Equal(t, http.StatusAccepted, retry.Code)
	require.Empty(t, bodyOf(t, retry))
}

func TestRetryingARunningJobIsAConflict(t *testing.T) {
	h := newHarness(t, Config{})
	h.backend.retry = func(string) error {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("job is Claimed, not finished"))
	}

	w := h.do(t, authed(http.MethodPost, "/retry/"+testJobID, testToken))

	require.Equal(t, http.StatusConflict, w.Code)
}

func TestAMutationIsAuthorizedBeforeItHappens(t *testing.T) {
	for _, route := range []string{"/cancel/", "/retry/"} {
		t.Run(route, func(t *testing.T) {
			h := newHarness(t, Config{})
			h.projects.role = func(string, string) (register.Role, error) { return register.RoleViewer, nil }

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
	h.backend.get = func(id string) (*buildv2.Job, error) {
		if id == "unknown" {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("no job"))
		}
		return &buildv2.Job{Id: id, RepositoryKey: "github.com/someone/else"}, nil
	}

	unknown := h.do(t, authed(http.MethodGet, "/job/unknown", testToken))
	denied := h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken))

	require.Equal(t, http.StatusNotFound, unknown.Code)
	require.Equal(t, http.StatusNotFound, denied.Code)
	require.Equal(t, bodyOf(t, unknown), bodyOf(t, denied))
}

func TestAnIdTheBackendCouldNotHaveMintedIsABadRequest(t *testing.T) {
	h := newHarness(t, Config{})
	h.backend.get = func(id string) (*buildv2.Job, error) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%q is not a job id", id))
	}

	w := h.do(t, authed(http.MethodGet, "/job/not-an-id", testToken))
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAnOversizedIdNeverReachesTheBackend(t *testing.T) {
	for _, path := range []string{"/job/", "/jobs/", "/cancel/", "/retry/"} {
		t.Run(path, func(t *testing.T) {
			h := newHarness(t, Config{})
			reached := false
			h.backend.get = func(string) (*buildv2.Job, error) { reached = true; return nil, nil }
			h.backend.list = func(string, func(string) error) error { reached = true; return nil }
			h.projects.role = func(string, string) (register.Role, error) { reached = true; return register.RoleOwner, nil }

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

func TestNoJobRouteSpendsTheCloudOnceForEveryRequest(t *testing.T) {
	const flood = 1000

	countGets := func(h *harness, n *int) {
		h.backend.get = func(string) (*buildv2.Job, error) {
			*n++
			return nil, connect.NewError(connect.CodeNotFound, errors.New("no job"))
		}
	}

	type testCase struct {
		name   string
		method string
		path   string
		count  func(*harness, *int)
	}
	for _, tc := range []testCase{
		{"GET /job", http.MethodGet, "/job/" + testJobID, countGets},
		{"POST /cancel", http.MethodPost, "/cancel/" + testJobID, countGets},
		{"POST /retry", http.MethodPost, "/retry/" + testJobID, countGets},
		{"GET /jobs", http.MethodGet, "/jobs/" + testProject, func(h *harness, n *int) {
			h.projects.role = func(string, string) (register.Role, error) {
				*n++
				return register.RoleNone, connect.NewError(connect.CodeNotFound, errors.New("no project"))
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

// The compile-time guarantee that every job route goes through admitted().
var _ = []jobHandler{
	(&Service{}).handleGetJob,
	(&Service{}).handleListJobs,
	(&Service{}).handleCancelJob,
	(&Service{}).handleRetryJob,
}

func TestAFloodOfInventedCredentialsMeetsTheGlobalJobLimit(t *testing.T) {
	h := newHarness(t, Config{
		JobRate:    Rate{Burst: 2, Per: time.Minute},
		CallerRate: Rate{Burst: 100, Per: time.Minute},
	})
	asked := 0
	h.callers.caller = func(string) (string, error) {
		asked++
		return "", ErrBadCredential
	}

	codes := []int{}
	for i := range 4 {
		codes = append(codes, h.do(t, authed(http.MethodGet, "/job/"+testJobID, fmt.Sprintf("invented-%d", i))).Code)
	}

	require.Equal(t, []int{
		http.StatusUnauthorized, http.StatusUnauthorized,
		http.StatusTooManyRequests, http.StatusTooManyRequests,
	}, codes)
	require.Equal(t, 2, asked, "two verifications, not four")
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
	require.Equal(t, http.StatusUnauthorized, h.do(t, authed(http.MethodGet, "/job/"+testJobID, "someone-else")).Code)

	// And the bucket refills on the clock, not on a sleep.
	h.clock.advance(time.Minute)
	require.Equal(t, http.StatusOK, h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken)).Code)
}

func TestARequestThatCostsNothingSpendsNoLimit(t *testing.T) {
	type testCase struct {
		name    string
		request func() *http.Request
	}
	for _, tc := range []testCase{
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
