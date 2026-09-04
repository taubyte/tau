package frontend

import (
	"errors"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
)

// followapi_test is GET /job/{jid}/logs — the route v1 has no version of,
// because patrick can hand over a log only once the build has ended and the
// whole of it has reached IPFS.

// serves is a Follow that hands the whole of text over in one chunk from the
// offset it was asked for.
func serves(text string) func(string, int64, func([]byte, int64) error) (int64, error) {
	return func(_ string, from int64, emit func([]byte, int64) error) (int64, error) {
		if from > int64(len(text)) {
			return from, connect.NewError(connect.CodeInvalidArgument, errors.New("offset past the end"))
		}
		end := int64(len(text))
		if err := emit([]byte(text[from:]), end); err != nil {
			return from, err
		}
		return end, nil
	}
}

func TestFollowStreamsTheLog(t *testing.T) {
	h := newHarness(t, Config{})
	h.backend.follow = serves("cloning\ncompiling\n")

	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs", testToken))

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "cloning\ncompiling\n", w.Body.String())

	// The body is the log and nothing else: no framing on this hop, so a client
	// resuming counts the bytes it wrote.
	require.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
	require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))

	// A PROXY THAT BUFFERED THIS WOULD DEFEAT THE ROUTE, so it says not to.
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Equal(t, "no", w.Header().Get("X-Accel-Buffering"))
}

func TestFollowResumesAtAnOffset(t *testing.T) {
	h := newHarness(t, Config{})
	h.backend.follow = serves("first\nsecond\n")

	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs?offset=6", testToken))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "second\n", w.Body.String())
}

// A NEGATIVE OFFSET IS REFUSED RATHER THAN CLAMPED: it means the caller's
// arithmetic went backwards, and starting somebody's build over from the
// beginning would hide that.
func TestFollowRefusesAnOffsetThatIsNotOne(t *testing.T) {
	h := newHarness(t, Config{})
	h.backend.follow = serves("output\n")

	for _, offset := range []string{"-1", "abc", "1.5", "9223372036854775808"} {
		w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs?offset="+offset, testToken))
		require.Equal(t, http.StatusBadRequest, w.Code, offset)
	}
}

// THE SAME AUTHORIZATION AS EVERY OTHER JOB ROUTE, decided before the first
// byte: on the caller's WRITE access to the job's repository, through
// admitted() like everything else in jobapi.go.
func TestFollowIsAuthorizedLikeTheRest(t *testing.T) {
	h := newHarness(t, Config{})
	h.backend.follow = serves("secret build output\n")

	// No bearer at all.
	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs", ""))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.NotContains(t, w.Body.String(), "secret build output",
		"nothing of the log reached an unauthenticated caller")

	// A caller who may read the repository but not write to it. Read access is
	// not enough because a public repository reads to everyone, which is
	// exactly what let an unrelated caller reach another project's jobs in v1.
	//
	// IT IS A 404 AND NOT A 403, which is this package's own choice and worth
	// pinning on a route that streams: telling a stranger "you may not read
	// this job" confirms the job exists, and a job id carries a repository.
	h.identity.write = func(string, repokey.Key) (Access, error) { return AccessDenied, nil }
	w = h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs", testToken))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.NotContains(t, w.Body.String(), "secret build output")
}

// THE STATUS IS ALREADY SENT when a follow fails, so there is nothing to
// respond with: the connection ends, which is what a client tailing a log sees
// as "it stopped" and what its offset lets it recover from.
func TestFollowThatBreaksMidStream(t *testing.T) {
	h := newHarness(t, Config{})
	h.backend.follow = func(_ string, from int64, emit func([]byte, int64) error) (int64, error) {
		if err := emit([]byte("cloning\n"), 8); err != nil {
			return from, err
		}
		return 8, connect.NewError(connect.CodeUnavailable, errors.New("the node writing this log went away"))
	}

	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs", testToken))

	// What arrived before the break is what the caller keeps.
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "cloning\n", w.Body.String())
}

// A build nobody is running is not an error the caller can act on beyond
// trying again — every backend answering Unavailable is exactly what that
// looks like from here.
func TestFollowWhenNothingIsRunningIt(t *testing.T) {
	h := newHarness(t, Config{})

	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs", testToken))
	require.Equal(t, http.StatusOK, w.Code)
	require.Empty(t, w.Body.String())
}
