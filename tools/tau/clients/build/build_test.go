package build

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"

	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	buildv1 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v1"
	tauBuild "github.com/taubyte/tau/services/build"
	"github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/config"
)

// The frontend, as far as this client is concerned: five routes and a bearer.
type frontend struct {
	mu     sync.Mutex
	tokens []string
	paths  []string

	handle func(w http.ResponseWriter, r *http.Request)
}

func (f *frontend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.tokens = append(f.tokens, r.Header.Get("Authorization"))
	f.paths = append(f.paths, r.URL.RequestURI())
	f.mu.Unlock()
	f.handle(w, r)
}

func (f *frontend) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

func serve(t *testing.T, handle func(http.ResponseWriter, *http.Request)) (*HTTP, *frontend) {
	t.Helper()

	f := &frontend{handle: handle}
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)

	c, err := New(server.URL, "a-token")
	require.NoError(t, err)
	return c, f
}

func TestJobs(t *testing.T) {
	c, f := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]string{"one", "two"})
	})

	ids, err := c.Jobs(t.Context(), "shop")
	require.NoError(t, err)
	require.Equal(t, []string{"one", "two"}, ids)
	require.Equal(t, []string{"/jobs/shop"}, f.seen())

	// THE TOKEN GOES ON EVERY CALL. The frontend authorizes each route on the
	// caller's write access, so one sent without it is refused rather than
	// answered publicly.
	require.Equal(t, "Bearer a-token", f.tokens[0])
}

// A LISTING LEFT OPEN IS REPORTED, NOT TRUNCATED SILENTLY. The frontend streams
// ids and stops mid-array when the backend breaks after it has committed to a
// 200 — precisely so the truncation is visible — and returning the prefix as if
// it were the whole would throw that away.
func TestJobsThatWasCutShort(t *testing.T) {
	c, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`["one","two"`))
	})

	_, err := c.Jobs(t.Context(), "shop")
	require.ErrorContains(t, err, "cut short")
}

func TestJob(t *testing.T) {
	c, f := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"id":"j1","status":"JobStatusSuccess","repository_key":"github.com/acme/shop",` +
			`"ref":"refs/heads/main","commit_sha":"abcdef0123","timestamp":"1700000000"}`))
	})

	job, err := c.Job(t.Context(), "j1")
	require.NoError(t, err)
	require.Equal(t, "j1", job.GetId())
	require.Equal(t, buildv1.JobStatus_JobStatusSuccess, job.GetStatus())
	require.Equal(t, "github.com/acme/shop", job.GetRepositoryKey())
	require.Equal(t, int64(1700000000), job.GetTimestamp())
	require.Equal(t, []string{"/job/j1"}, f.seen())
}

// A FIELD THIS BINARY HAS NOT HEARD OF IS NOT AN ERROR. A cloud can be ahead of
// the CLI talking to it, and refusing to show a job because it grew a field is
// a worse answer than showing the fields both sides know.
func TestJobFromANewerCloud(t *testing.T) {
	c, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"id":"j1","something_new":42}`))
	})

	job, err := c.Job(t.Context(), "j1")
	require.NoError(t, err)
	require.Equal(t, "j1", job.GetId())
}

func TestJobThatIsNotJSON(t *testing.T) {
	c, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("not json"))
	})

	_, err := c.Job(t.Context(), "j1")
	require.ErrorContains(t, err, "reading job j1")
}

func TestCancelAndRetry(t *testing.T) {
	c, f := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	require.NoError(t, c.Cancel(t.Context(), "j1"))
	require.NoError(t, c.Retry(t.Context(), "j1"))
	require.Equal(t, []string{"/cancel/j1", "/retry/j1"}, f.seen())
}

// ── following ────────────────────────────────────────────────────────────

// A LOG IS FOLLOWED WHILE IT IS WRITTEN, which is what v1 could not do at all:
// patrick could hand one over only once the build had ended and the whole of it
// had reached IPFS.
func TestFollowStreamsWhileTheBuildRuns(t *testing.T) {
	release := make(chan struct{})
	c, f := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("cloning\n"))
		w.(http.Flusher).Flush()
		<-release
		w.Write([]byte("done\n"))
	})

	var out safeBuffer
	done := make(chan error, 1)
	go func() {
		_, err := c.Follow(t.Context(), "j1", 0, &out)
		done <- err
	}()

	require.Eventually(t, func() bool { return out.String() == "cloning\n" }, 5*time.Second, 5*time.Millisecond,
		"the first line arrived before the build finished")

	close(release)
	require.NoError(t, <-done)
	require.Equal(t, "cloning\ndone\n", out.String())
	require.Equal(t, []string{"/job/j1/logs?offset=0"}, f.seen())
}

// THE OFFSET IS WHAT A DROPPED CONNECTION COSTS: nothing.
func TestFollowResumes(t *testing.T) {
	c, f := serve(t, func(w http.ResponseWriter, r *http.Request) {
		at, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		w.Write([]byte("first\nsecond\n"[at:]))
	})

	var out bytes.Buffer
	end, err := c.Follow(t.Context(), "j1", 6, &out)
	require.NoError(t, err)
	require.Equal(t, "second\n", out.String())
	require.Equal(t, int64(6+len("second\n")), end)
	require.Equal(t, []string{"/job/j1/logs?offset=6"}, f.seen())
}

// THE OFFSET COUNTS WHAT WAS WRITTEN, NOT WHAT WAS READ. Bytes this client took
// off the wire but could not hand on are bytes the caller does not have, and
// resuming past them would leave a hole nobody ever sees.
func TestFollowCountsWhatTheCallerActuallyGot(t *testing.T) {
	c, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("kept\nlost\n"))
	})

	end, err := c.Follow(t.Context(), "j1", 0, &shortWriter{limit: len("kept\n")})
	require.ErrorContains(t, err, "no room")
	require.Equal(t, int64(len("kept\n")), end)
}

func TestFollowRefusesWhatItCannotAsk(t *testing.T) {
	c, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	_, err := c.Follow(t.Context(), "", 0, io.Discard)
	require.ErrorContains(t, err, "no job")

	_, err = c.Follow(t.Context(), "j1", -1, io.Discard)
	require.ErrorContains(t, err, "before the start")

	require.ErrorContains(t, c.Cancel(t.Context(), ""), "no job")
	require.ErrorContains(t, c.Retry(t.Context(), ""), "no job")
	_, err = c.Job(t.Context(), "")
	require.ErrorContains(t, err, "no job")
	_, err = c.Jobs(t.Context(), "")
	require.ErrorContains(t, err, "no project")
}

// ── what the cloud says no with ──────────────────────────────────────────

func TestStatusesAreKept(t *testing.T) {
	for _, tc := range []struct {
		code int
		body string
		want error
	}{
		{http.StatusUnauthorized, `{"error":"unauthorized"}`, ErrUnauthorized},
		{http.StatusNotFound, `{"error":"not found"}`, ErrNotFound},
		{http.StatusTooManyRequests, `{"error":"slow down"}`, ErrRateLimited},
	} {
		c, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.code)
			w.Write([]byte(tc.body))
		})

		_, err := c.Job(t.Context(), "j1")
		require.ErrorIs(t, err, tc.want)
	}

	// Anything else keeps the status line, because a caller cannot act on a
	// code this package has not thought about and a person can.
	c, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("upstream is unwell"))
	})
	_, err := c.Job(t.Context(), "j1")
	require.ErrorContains(t, err, "502")
	require.ErrorContains(t, err, "upstream is unwell")
}

func TestNewRefusesWhatItCannotDial(t *testing.T) {
	_, err := New("", "token")
	require.ErrorContains(t, err, "no address")

	_, err = New("://nope", "token")
	require.Error(t, err)

	// A trailing slash is not a second cloud.
	c, err := New("http://example.invalid/", "token")
	require.NoError(t, err)
	require.Equal(t, "http://example.invalid", c.base)

	// AND THE DEFAULT TRANSPORT MUST NOT HAVE A WHOLE-REQUEST TIMEOUT: a follow
	// is a body written for as long as a build runs, and Timeout would cut off
	// exactly the case this client exists for.
	require.Zero(t, c.http.Timeout)
}

func TestWithHTTPClient(t *testing.T) {
	mine := &http.Client{}
	c, err := New("http://example.invalid", "t", WithHTTPClient(mine))
	require.NoError(t, err)
	require.Same(t, mine, c.http)

	// A nil one is ignored rather than leaving the client without a transport.
	c, err = New("http://example.invalid", "t", WithHTTPClient(nil))
	require.NoError(t, err)
	require.NotNil(t, c.http)
}

// A cloud that is not there is the error the caller sees, not a panic.
func TestACloudThatIsNotThere(t *testing.T) {
	c, err := New("http://127.0.0.1:1", "t")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	_, err = c.Job(ctx, "j1")
	require.Error(t, err)
}

// ── helpers ──────────────────────────────────────────────────────────────

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// shortWriter accepts limit bytes and then refuses, which is what a terminal
// that went away or a full disk looks like.
type shortWriter struct {
	limit int
	got   int
}

func (s *shortWriter) Write(p []byte) (int, error) {
	room := s.limit - s.got
	if room <= 0 {
		return 0, errors.New("no room")
	}
	if len(p) <= room {
		s.got += len(p)
		return len(p), nil
	}
	s.got = s.limit
	return room, errors.New("no room")
}

// ── where a cloud's build service answers ────────────────────────────────

// THE PUBLIC ROLE'S NAME IS THE BUILD SERVICE'S OWN. Spelling it again here
// would let a rename leave the CLI looking for a host nothing serves, and the
// failure would read as "the cloud is down".
func TestAddress(t *testing.T) {
	url, err := address(config.Profile{CloudType: common.RemoteCloud, Cloud: "acme.io"})
	require.NoError(t, err)
	require.Equal(t, "https://"+tauBuild.Frontend.Name+".tau.acme.io", url)

	_, err = address(config.Profile{CloudType: "something-else"})
	require.Error(t, err)

	// A dream cloud is found by asking the running universe, so with none up
	// this fails rather than guessing a port.
	_, err = address(config.Profile{CloudType: common.DreamCloud})
	require.Error(t, err)
}
