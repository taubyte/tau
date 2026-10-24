package frontend

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/register"
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
	h.projects.role = func(string, string) (register.Role, error) { return register.RoleNone, nil }
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

// building is a log the backend is still writing, read the two ways the real
// one reads it: follow sends what is there and then each line as it is
// written, ending with the build; snapshot sends what is there and ends.
type building struct {
	mu    sync.Mutex
	text  string
	grew  chan struct{}
	ended bool
}

// newBuilding goes after the listener a test follows it through, so that its
// cleanup ends the build before the listener waits on the follow.
func newBuilding(t *testing.T, text string) *building {
	b := &building{text: text, grew: make(chan struct{})}
	t.Cleanup(b.end)
	return b
}

func (b *building) say(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.text += line
	close(b.grew)
	b.grew = make(chan struct{})
}

func (b *building) end() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.ended {
		b.ended = true
		close(b.grew)
	}
}

func (b *building) follow(_ string, from int64, emit func([]byte, int64) error) (int64, error) {
	for {
		b.mu.Lock()
		text, grew, ended := b.text, b.grew, b.ended
		b.mu.Unlock()
		if from < int64(len(text)) {
			if err := emit([]byte(text[from:]), int64(len(text))); err != nil {
				return from, err
			}
			from = int64(len(text))
			continue
		}
		if ended {
			return from, nil
		}
		<-grew
	}
}

func (b *building) snapshot(id string, from int64, emit func([]byte, int64) error) (int64, error) {
	b.mu.Lock()
	text := b.text
	b.mu.Unlock()
	return serves(text)(id, from, emit)
}

// get is an authorized GET through a real listener, so the response can be
// read while it streams. The client's timeout bounds a read that never ends.
func get(t *testing.T, srv *httptest.Server, path string) *http.Response {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+path, nil)
	require.NoError(t, err)
	r.Header.Set("Authorization", bearerPrefix+testToken)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(r)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	require.Equal(t, http.StatusOK, resp.StatusCode)
	return resp
}

func readN(t *testing.T, r io.Reader, n int) string {
	t.Helper()
	buf := make([]byte, n)
	_, err := io.ReadFull(r, buf)
	require.NoError(t, err)
	return string(buf)
}

// A SNAPSHOT IS WHAT A RUNNING BUILD HAS LOGGED SO FAR, and the response ends
// while the build goes on: `tau logs` without --follow. The same route without
// it follows: each line as it is written, ending when the build does.
func TestASnapshotEndsWhileTheBuildRunsAndAFollowEndsWithIt(t *testing.T) {
	h := newHarness(t, Config{})
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(srv.Close)
	build := newBuilding(t, "cloning\n")
	h.backend.follow, h.backend.snapshot = build.follow, build.snapshot

	soFar, err := io.ReadAll(get(t, srv, "/job/"+testJobID+"/logs?snapshot=true").Body)
	require.NoError(t, err, "a snapshot that waited for the build would have timed out")
	require.Equal(t, "cloning\n", string(soFar))

	followed := get(t, srv, "/job/"+testJobID+"/logs").Body
	require.Equal(t, "cloning\n", readN(t, followed, len("cloning\n")))

	build.say("compiling\n")
	require.Equal(t, "compiling\n", readN(t, followed, len("compiling\n")))

	soFar, err = io.ReadAll(get(t, srv, "/job/"+testJobID+"/logs?snapshot=true&offset=8").Body)
	require.NoError(t, err)
	require.Equal(t, "compiling\n", string(soFar))

	build.end()
	rest, err := io.ReadAll(followed)
	require.NoError(t, err)
	require.Empty(t, rest, "the follow ends with the build")
}

// A BUILD THAT HAS ENDED IS WHOLE TO BOTH READS: the follow drains to the end
// and stops, and a snapshot of it is the same bytes.
func TestAnEndedBuildsLogIsWholeToBothReads(t *testing.T) {
	h := newHarness(t, Config{})
	build := newBuilding(t, "cloning\ncompiling\n")
	build.end()
	h.backend.follow, h.backend.snapshot = build.follow, build.snapshot

	for _, path := range []string{"/logs", "/logs?snapshot=true"} {
		w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+path, testToken))
		require.Equal(t, http.StatusOK, w.Code, path)
		require.Equal(t, "cloning\ncompiling\n", w.Body.String(), path)
	}
}

// ?snapshot= is a boolean: false follows, and a value that is not one is
// refused rather than guessed at, as an offset that is not a number is.
func TestASnapshotIsAskedForWithABoolean(t *testing.T) {
	h := newHarness(t, Config{})
	h.backend.follow = serves("followed\n")
	h.backend.snapshot = serves("so far\n")

	for value, want := range map[string]string{"false": "followed\n", "0": "followed\n", "true": "so far\n", "1": "so far\n"} {
		w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs?snapshot="+value, testToken))
		require.Equal(t, http.StatusOK, w.Code, value)
		require.Equal(t, want, w.Body.String(), value)
	}
	for _, value := range []string{"maybe", "yes", "2"} {
		w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs?snapshot="+value, testToken))
		require.Equal(t, http.StatusBadRequest, w.Code, value)
	}
}
