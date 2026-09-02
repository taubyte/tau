package frontend

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
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
