package frontend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/build/joblog"
)

// read is Backend.Follow or Backend.Snapshot.
type read = func(id string, run int32, from int64, emit func(*buildv2.Job_Event) error) (int64, error)

// logSaying is the log a run that said text writes: a message per line.
func logSaying(text string) []byte {
	var log bytes.Buffer
	for _, line := range strings.SplitAfter(text, "\n") {
		if line == "" {
			continue
		}
		joblog.Write(&log, &buildv2.Job_Event{Kind: &buildv2.Job_Event_Message{Message: strings.TrimSuffix(line, "\n")}})
	}
	return log.Bytes()
}

// after is the offset after the nth line of text's log.
func after(text string, n int) int64 {
	return int64(len(logSaying(strings.Join(strings.SplitAfter(text, "\n")[:n], ""))))
}

// emitFrom sends the events of text's log from `from`, and answers where it ended.
func emitFrom(text string, from int64, emit func(*buildv2.Job_Event) error) (int64, error) {
	log := logSaying(text)
	if from > int64(len(log)) {
		return from, connect.NewError(connect.CodeInvalidArgument, errors.New("offset past the end"))
	}
	events := joblog.Read(bytes.NewReader(log[from:]), from)
	for {
		ev, err := events.Next()
		if errors.Is(err, io.EOF) {
			return events.Offset(), nil
		}
		if err != nil {
			return events.Offset(), err
		}
		if err := emit(ev); err != nil {
			return ev.GetOffset(), err
		}
	}
}

// serves is a read that hands text's log over from the offset it was asked for,
// whichever run it was asked about.
func serves(text string) read {
	return func(_ string, _ int32, from int64, emit func(*buildv2.Job_Event) error) (int64, error) {
		return emitFrom(text, from, emit)
	}
}

// said is what a response's events said, a line per message.
func said(t *testing.T, body io.Reader) string {
	t.Helper()
	var out strings.Builder
	lines := bufio.NewScanner(body)
	for lines.Scan() {
		var ev buildv2.Job_Event
		require.NoError(t, protojson.Unmarshal(lines.Bytes(), &ev))
		if m, ok := ev.GetKind().(*buildv2.Job_Event_Message); ok {
			out.WriteString(m.Message + "\n")
		}
	}
	require.NoError(t, lines.Err())
	return out.String()
}

// nextSaid is the next message on a streaming response.
func nextSaid(t *testing.T, events *bufio.Reader) string {
	t.Helper()
	for {
		line, err := events.ReadBytes('\n')
		require.NoError(t, err)
		var ev buildv2.Job_Event
		require.NoError(t, protojson.Unmarshal(line, &ev))
		if m, ok := ev.GetKind().(*buildv2.Job_Event_Message); ok {
			return m.Message + "\n"
		}
	}
}

// asked is what a read was asked for: the run and the offset.
type asked struct {
	run  int32
	from int64
}

// recorded is a read that notes what it was asked before answering as next
// does.
type recorded struct {
	mu   sync.Mutex
	asks []asked
}

func (r *recorded) of(next read) read {
	return func(id string, run int32, from int64, emit func(*buildv2.Job_Event) error) (int64, error) {
		r.mu.Lock()
		r.asks = append(r.asks, asked{run, from})
		r.mu.Unlock()
		return next(id, run, from, emit)
	}
}

func (r *recorded) all() []asked {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]asked(nil), r.asks...)
}

// jobAt is a record of the test job: its status, how many runs it has had, and
// the logs it files under them.
func jobAt(status buildv2.JobStatus, attempt int32, logs map[string]string) func(string) (*buildv2.Job, error) {
	return func(id string) (*buildv2.Job, error) {
		return &buildv2.Job{Id: id, Status: status, RepositoryKey: testRepoKey, Attempt: attempt, Logs: logs}, nil
	}
}

// key is the key a record files a run's log under.
func key(run int) string { return fmt.Sprintf("%020d", run) }

func TestFollowStreamsTheLog(t *testing.T) {
	h := newHarness(t, Config{})
	h.backend.follow = serves("cloning\ncompiling\n")

	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs", testToken))

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "cloning\ncompiling\n", said(t, w.Body))

	// The body is the run's events, one JSON object per line, each saying where a
	// client that lost the stream resumes from.
	require.Equal(t, "application/x-ndjson", w.Header().Get("Content-Type"))
	require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))

	// A PROXY THAT BUFFERED THIS WOULD DEFEAT THE ROUTE, so it says not to.
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Equal(t, "no", w.Header().Get("X-Accel-Buffering"))

	// And it says which run it is, which a resume sends back.
	require.Equal(t, "1", w.Header().Get("X-Job-Attempt"))
}

func TestFollowResumesAtAnOffset(t *testing.T) {
	h := newHarness(t, Config{})
	var follows recorded
	h.backend.follow = follows.of(serves("first\nsecond\n"))

	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs?attempt=1&offset="+strconv.FormatInt(after("first\nsecond\n", 1), 10), testToken))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "second\n", said(t, w.Body))
	require.Equal(t, []asked{{1, after("first\nsecond\n", 1)}}, follows.all())
}

func TestFollowRefusesAnOffsetThatIsNotOne(t *testing.T) {
	h := newHarness(t, Config{})
	h.backend.follow = serves("output\n")

	for _, offset := range []string{"-1", "abc", "1.5", "9223372036854775808"} {
		w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs?offset="+offset, testToken))
		require.Equal(t, http.StatusBadRequest, w.Code, offset)
	}
}

func TestFollowRefusesAnAttemptThatIsNotOne(t *testing.T) {
	h := newHarness(t, Config{})
	h.backend.follow = serves("output\n")

	for _, attempt := range []string{"0", "-1", "abc", "1.5", "2147483648"} {
		w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs?attempt="+attempt, testToken))
		require.Equal(t, http.StatusBadRequest, w.Code, attempt)
	}

	h.backend.snapshot = func(_ string, run int32, from int64, _ func(*buildv2.Job_Event) error) (int64, error) {
		return from, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("attempt %d is not the job's next", run))
	}
	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs?attempt=9", testToken))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Equal(t, "9", w.Header().Get("X-Job-Attempt"))
}

func TestFollowIsAuthorizedLikeTheRest(t *testing.T) {
	h := newHarness(t, Config{})
	h.backend.follow = serves("secret build output\n")

	// No bearer at all.
	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs", ""))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.NotContains(t, w.Body.String(), "secret build output",
		"nothing of the log reached an unauthenticated caller")

	// A caller without a role on the project gets a 404, not a 403: a job id carries a
	// repository.
	h.projects.role = func(string, string) (register.Role, error) { return register.RoleNone, nil }
	w = h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs", testToken))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.NotContains(t, w.Body.String(), "secret build output")
}

func TestTheResponseIsOneRunAndSaysWhich(t *testing.T) {
	type testCase struct {
		name    string
		status  buildv2.JobStatus
		attempt int32
		query   string
		run     int32
	}
	for _, tc := range []testCase{
		{"claimed", buildv2.JobStatus_JobStatusClaimed, 2, "", 2},
		{"queued again", buildv2.JobStatus_JobStatusOpen, 2, "", 3},
		{"queued", buildv2.JobStatus_JobStatusOpen, 0, "", 1},
		{"ended", buildv2.JobStatus_JobStatusSuccess, 3, "", 3},
		{"an earlier run", buildv2.JobStatus_JobStatusSuccess, 3, "?attempt=1", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, Config{})
			h.backend.get = jobAt(tc.status, tc.attempt, nil)
			var reads recorded
			h.backend.snapshot, h.backend.follow = reads.of(serves("so far\n")), reads.of(serves("so far\nand more\n"))

			w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs"+tc.query, testToken))
			require.Equal(t, http.StatusOK, w.Code)
			require.Equal(t, "so far\nand more\n", said(t, w.Body))
			require.Equal(t, strconv.Itoa(int(tc.run)), w.Header().Get("X-Job-Attempt"))
			require.Equal(t, []asked{{tc.run, 0}, {tc.run, after("so far\n", 1)}}, reads.all())
		})
	}
}

func TestAResumeIsNeverAnotherRunsLog(t *testing.T) {
	first, second := "first run, line one\nfirst run, line two\n", "second run, line one\nsecond run, line two\n"
	h := newHarness(t, quick, WithAssets(fakeAssets{"bafy1": lzwOf(t, first)}))
	h.backend.get = jobAt(buildv2.JobStatus_JobStatusClaimed, 2, map[string]string{key(1): "bafy1"})
	byRun := func(id string, run int32, from int64, emit func(*buildv2.Job_Event) error) (int64, error) {
		if run == 2 {
			return serves(second)(id, run, from, emit)
		}
		return endedAt(id, run, from, emit)
	}
	h.backend.follow, h.backend.snapshot = byRun, byRun

	read := strconv.FormatInt(after(first, 1), 10)
	for _, path := range []string{"/logs?attempt=1&offset=", "/logs?snapshot=true&attempt=1&offset="} {
		w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+path+read, testToken))
		require.Equal(t, http.StatusOK, w.Code, path)
		require.Equal(t, "first run, line two\n", said(t, w.Body), path)
		require.Equal(t, "1", w.Header().Get("X-Job-Attempt"), path)
	}

	// The first run's node went away before its log was stored: gone, and
	// still not the second run's bytes.
	h.backend.get = jobAt(buildv2.JobStatus_JobStatusClaimed, 2, nil)
	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs?attempt=1&offset="+read, testToken))
	require.Equal(t, http.StatusGone, w.Code)
	require.Equal(t, "1", w.Header().Get("X-Job-Attempt"))
	require.NotContains(t, w.Body.String(), "second run")
}

func TestAnEndedRunIsNeverAnotherRunsStoredLog(t *testing.T) {
	h := newHarness(t, quick, WithAssets(fakeAssets{"bafy1": lzwOf(t, "the first run\n"), "bafy2": lzwOf(t, "the second run\n")}))
	h.backend.get = jobAt(buildv2.JobStatus_JobStatusCancelled, 2, map[string]string{key(1): "bafy1"})
	h.backend.follow, h.backend.snapshot = endedAt, endedAt

	for _, path := range []string{"/logs", "/logs?snapshot=true"} {
		w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+path, testToken))
		require.Equal(t, http.StatusGone, w.Code, path)
		require.Equal(t, "2", w.Header().Get("X-Job-Attempt"), path)
		require.NotContains(t, w.Body.String(), "the first run", path)
	}

	h.backend.get = jobAt(buildv2.JobStatus_JobStatusCancelled, 2, map[string]string{key(1): "bafy1", key(2): "bafy2"})
	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs", testToken))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "the second run\n", said(t, w.Body))
	w = h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs?attempt=1", testToken))
	require.Equal(t, "the first run\n", said(t, w.Body))
}

func TestARunTheJobNeverReachedIsEmpty(t *testing.T) {
	h := newHarness(t, quick, WithAssets(fakeAssets{}))
	h.backend.get = jobAt(buildv2.JobStatus_JobStatusCancelled, 0, nil)
	h.backend.follow, h.backend.snapshot = endedAt, endedAt

	for _, path := range []string{"/logs", "/logs?snapshot=true"} {
		w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+path, testToken))
		require.Equal(t, http.StatusOK, w.Code, path)
		require.Empty(t, said(t, w.Body), path)
		require.Equal(t, "1", w.Header().Get("X-Job-Attempt"), path)
	}
	require.Empty(t, h.logs.joined())
}

func TestAFollowThatBreaksResumesItsRun(t *testing.T) {
	h := newHarness(t, quick)
	var follows recorded
	broke := false
	h.backend.follow = follows.of(func(id string, run int32, from int64, emit func(*buildv2.Job_Event) error) (int64, error) {
		if !broke {
			broke = true
			if _, err := emitFrom("cloning\n", 0, emit); err != nil {
				return from, err
			}
			return after("cloning\n", 1), connect.NewError(connect.CodeUnavailable, errors.New("the node writing this log went away"))
		}
		return serves("cloning\ncompiling\n")(id, run, from, emit)
	})

	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs", testToken))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "cloning\ncompiling\n", said(t, w.Body))
	require.Equal(t, []asked{{1, 0}, {1, after("cloning\n", 1)}}, follows.all())
	require.Empty(t, h.logs.joined())
}

func TestAFollowWhoseRunWasLostIsCut(t *testing.T) {
	h := newHarness(t, quick, WithAssets(fakeAssets{}))
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(srv.Close)
	h.backend.get = jobAt(buildv2.JobStatus_JobStatusClaimed, 1, nil)
	lost := func(_ string, _ int32, from int64, emit func(*buildv2.Job_Event) error) (int64, error) {
		if from > 0 {
			return from, connect.NewError(connect.CodeFailedPrecondition, errors.New("attempt 1 has ended"))
		}
		if _, err := emitFrom("cloning\n", 0, emit); err != nil {
			return from, err
		}
		// The node went away, the claim lapsed and the job ran again.
		h.backend.get = jobAt(buildv2.JobStatus_JobStatusClaimed, 2, nil)
		return after("cloning\n", 1), connect.NewError(connect.CodeUnavailable, errors.New("the node writing this log went away"))
	}
	h.backend.follow, h.backend.snapshot = lost, lost

	resp := get(t, srv, "/job/"+testJobID+"/logs")
	require.Equal(t, "1", resp.Header.Get("X-Job-Attempt"))
	events := bufio.NewReader(resp.Body)
	require.Equal(t, "cloning\n", nextSaid(t, events))
	_, err := io.ReadAll(events)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF, "a cut response is not the end of the log")
	require.Contains(t, h.logs.joined(), "following job "+testJobID)

	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs?attempt=1&offset="+strconv.FormatInt(after("cloning\n", 1), 10), testToken))
	require.Equal(t, http.StatusGone, w.Code)
	require.Equal(t, "1", w.Header().Get("X-Job-Attempt"))
	type refusalJSON struct{ Error string }
	var refusal refusalJSON
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &refusal))
	require.Equal(t, "this run's log was not kept", refusal.Error)
}

// quick is a follow that asks again at once, for a test that waits on its asks
// and not on its pace.
var quick = Config{FollowBackoff: time.Millisecond, FollowBackoffMax: 4 * time.Millisecond}

// notWritten is every backend's answer for a log no node is writing yet.
func notWritten(_ string, _ int32, from int64, _ func(*buildv2.Job_Event) error) (int64, error) {
	return from, connect.NewError(connect.CodeUnavailable, errors.New("no build of that job is running"))
}

func TestAFollowsPaceHasDefaults(t *testing.T) {
	c, err := Config{}.withDefaults()
	require.NoError(t, err)
	require.Equal(t, DefaultFollowBackoff, c.FollowBackoff)
	require.Equal(t, DefaultFollowBackoffMax, c.FollowBackoffMax)

	c, err = Config{FollowBackoff: time.Minute}.withDefaults()
	require.NoError(t, err)
	require.Equal(t, time.Minute, c.FollowBackoffMax, "a ceiling below the first wait is raised to it")
}

func TestAFollowWaitsForAJobToStart(t *testing.T) {
	h := newHarness(t, quick)
	var asked []int64
	h.backend.follow = func(id string, run int32, from int64, emit func(*buildv2.Job_Event) error) (int64, error) {
		asked = append(asked, from)
		if len(asked) < 3 {
			return notWritten(id, run, from, emit)
		}
		return serves("cloning\ncompiling\n")(id, run, from, emit)
	}

	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs", testToken))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "cloning\ncompiling\n", said(t, w.Body))
	require.Equal(t, []int64{0, 0, 0}, asked)
	require.Empty(t, h.logs.joined(), "waiting for a queued job is not a failure")
}

// queuedJob is a job no node writes a log for until it starts.
type queuedJob struct {
	asks    atomic.Int32
	started atomic.Pointer[building]
}

func (q *queuedJob) follow(id string, run int32, from int64, emit func(*buildv2.Job_Event) error) (int64, error) {
	q.asks.Add(1)
	if b := q.started.Load(); b != nil {
		return b.follow(id, run, from, emit)
	}
	return notWritten(id, run, from, emit)
}

func (q *queuedJob) snapshot(id string, run int32, from int64, emit func(*buildv2.Job_Event) error) (int64, error) {
	if b := q.started.Load(); b != nil {
		return b.snapshot(id, run, from, emit)
	}
	return notWritten(id, run, from, emit)
}

func TestAQueuedJobIsFollowedFromBeforeItStarts(t *testing.T) {
	h := newHarness(t, quick)
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(srv.Close)
	job := &queuedJob{}
	h.backend.follow, h.backend.snapshot = job.follow, job.snapshot

	require.Empty(t, said(t, get(t, srv, "/job/"+testJobID+"/logs?snapshot=true").Body))

	followed := bufio.NewReader(get(t, srv, "/job/"+testJobID+"/logs").Body)
	require.Eventually(t, func() bool { return job.asks.Load() >= 3 }, 5*time.Second, time.Millisecond,
		"the follow keeps asking while the job is queued")

	b := newBuilding(t, "cloning\n")
	job.started.Store(b)
	require.Equal(t, "cloning\n", nextSaid(t, followed))
	b.say("compiling\n")
	require.Equal(t, "compiling\n", nextSaid(t, followed))
	b.end()
	require.Empty(t, said(t, followed), "the follow ends with the build")
	require.Empty(t, h.logs.joined())
}

func TestAWaitingFollowEndsWithItsViewer(t *testing.T) {
	h := newHarness(t, Config{FollowBackoff: time.Hour})
	ctx, cancel := context.WithCancel(t.Context())
	h.backend.follow = func(id string, run int32, from int64, emit func(*buildv2.Job_Event) error) (int64, error) {
		cancel()
		return notWritten(id, run, from, emit)
	}

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		h.Handler().ServeHTTP(w, authed(http.MethodGet, "/job/"+testJobID+"/logs", testToken).WithContext(ctx))
		done <- w
	}()
	w := <-done
	require.Equal(t, http.StatusOK, w.Code)
	require.Empty(t, said(t, w.Body))
	require.Empty(t, h.logs.joined(), "a viewer hanging up is not a failure")
}

// endedAt is a backend whose run ended before the read reached its log.
func endedAt(_ string, _ int32, from int64, _ func(*buildv2.Job_Event) error) (int64, error) {
	return from, connect.NewError(connect.CodeFailedPrecondition, errors.New("job finished as JobStatusFailed"))
}

// retried is a job whose tenth run ended, each run's log stored under its
// zero-padded number.
func retried(id string) (*buildv2.Job, error) {
	return &buildv2.Job{
		Id: id, Status: buildv2.JobStatus_JobStatusFailed, RepositoryKey: testRepoKey, Attempt: 10,
		Logs: map[string]string{key(9): "bafy9", key(10): "bafy10"},
	}, nil
}

func TestAnEndedJobsLogIsTheStoredOne(t *testing.T) {
	h := newHarness(t, quick, WithAssets(fakeAssets{"bafy9": lzwOf(t, "the attempt before\n"), "bafy10": lzwOf(t, "cloning\nerror: failed\n")}))
	h.backend.get = retried
	asks := 0
	h.backend.snapshot = notWritten
	h.backend.follow = func(id string, run int32, from int64, emit func(*buildv2.Job_Event) error) (int64, error) {
		if asks++; asks == 1 {
			return notWritten(id, run, from, emit)
		}
		return endedAt(id, run, from, emit)
	}

	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs", testToken))
	require.Equal(t, "cloning\nerror: failed\n", said(t, w.Body), "it ran between two asks")
	require.Equal(t, 2, asks)

	h.backend.snapshot = endedAt
	w = h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs?offset="+strconv.FormatInt(after("cloning\nerror: failed\n", 1), 10), testToken))
	require.Equal(t, "error: failed\n", said(t, w.Body))
	w = h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs?snapshot=true", testToken))
	require.Equal(t, "cloning\nerror: failed\n", said(t, w.Body))
	w = h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs?attempt=9", testToken))
	require.Equal(t, "the attempt before\n", said(t, w.Body))
	require.Equal(t, "9", w.Header().Get("X-Job-Attempt"))
	require.Empty(t, h.logs.joined())

	// A run that stored no log has none to send, and it is not an empty one.
	h.backend.get = func(id string) (*buildv2.Job, error) {
		job, err := retried(id)
		job.Logs = nil
		return job, err
	}
	w = h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs?snapshot=true", testToken))
	require.Equal(t, http.StatusGone, w.Code)
	require.Equal(t, "10", w.Header().Get("X-Job-Attempt"))
}

func TestAnEndedJobsLogThatCannotBeRead(t *testing.T) {
	type testCase struct {
		h      *harness
		status int
	}
	for name, tc := range map[string]testCase{
		"no asset store": {newHarness(t, quick), http.StatusServiceUnavailable},
		"not stored":     {newHarness(t, quick, WithAssets(fakeAssets{})), http.StatusBadGateway},
	} {
		tc.h.backend.get = retried
		tc.h.backend.follow, tc.h.backend.snapshot = endedAt, endedAt
		w := tc.h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs", testToken))
		require.Equal(t, tc.status, w.Code, name)
	}

	h := newHarness(t, quick, WithAssets(fakeAssets{"bafy10": lzwOf(t, "x")}))
	authorized := false
	h.backend.get = func(id string) (*buildv2.Job, error) {
		if !authorized {
			authorized = true
			return retried(id)
		}
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("no leader"))
	}
	h.backend.follow, h.backend.snapshot = endedAt, endedAt
	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs", testToken))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestASnapshotOfAQueuedJobIsEmpty(t *testing.T) {
	h := newHarness(t, quick)
	asks := 0
	h.backend.snapshot = func(id string, run int32, from int64, emit func(*buildv2.Job_Event) error) (int64, error) {
		asks++
		return notWritten(id, run, from, emit)
	}

	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs?snapshot=true", testToken))
	require.Equal(t, http.StatusOK, w.Code)
	require.Empty(t, said(t, w.Body))
	require.Equal(t, 1, asks)
	require.Empty(t, h.logs.joined())
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

func (b *building) follow(_ string, _ int32, from int64, emit func(*buildv2.Job_Event) error) (int64, error) {
	for {
		b.mu.Lock()
		text, grew, ended := b.text, b.grew, b.ended
		b.mu.Unlock()
		if end := int64(len(logSaying(text))); from < end {
			reached, err := emitFrom(text, from, emit)
			if err != nil {
				return reached, err
			}
			from = reached
			continue
		}
		if ended {
			return from, nil
		}
		<-grew
	}
}

func (b *building) snapshot(id string, run int32, from int64, emit func(*buildv2.Job_Event) error) (int64, error) {
	b.mu.Lock()
	text := b.text
	b.mu.Unlock()
	return serves(text)(id, run, from, emit)
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

func TestASnapshotEndsWhileTheBuildRunsAndAFollowEndsWithIt(t *testing.T) {
	h := newHarness(t, Config{})
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(srv.Close)
	b := newBuilding(t, "cloning\n")
	h.backend.follow, h.backend.snapshot = b.follow, b.snapshot

	require.Equal(t, "cloning\n", said(t, get(t, srv, "/job/"+testJobID+"/logs?snapshot=true").Body),
		"a snapshot that waited for the build would hang")

	followed := bufio.NewReader(get(t, srv, "/job/"+testJobID+"/logs").Body)
	require.Equal(t, "cloning\n", nextSaid(t, followed))

	b.say("compiling\n")
	require.Equal(t, "compiling\n", nextSaid(t, followed))

	require.Equal(t, "compiling\n", said(t, get(t, srv, "/job/"+testJobID+"/logs?snapshot=true&offset="+strconv.FormatInt(after("cloning\n", 1), 10)).Body))

	b.end()
	require.Empty(t, said(t, followed), "the follow ends with the build")
}

func TestAnEndedBuildsLogIsWholeToBothReads(t *testing.T) {
	h := newHarness(t, Config{})
	b := newBuilding(t, "cloning\ncompiling\n")
	b.end()
	h.backend.follow, h.backend.snapshot = b.follow, b.snapshot

	for _, path := range []string{"/logs", "/logs?snapshot=true"} {
		w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+path, testToken))
		require.Equal(t, http.StatusOK, w.Code, path)
		require.Equal(t, "cloning\ncompiling\n", said(t, w.Body), path)
	}
}

func TestASnapshotIsAskedForWithABoolean(t *testing.T) {
	h := newHarness(t, Config{})
	var reads []string
	h.backend.follow = func(id string, run int32, from int64, emit func(*buildv2.Job_Event) error) (int64, error) {
		reads = append(reads, "follow")
		return serves("log\n")(id, run, from, emit)
	}
	h.backend.snapshot = func(id string, run int32, from int64, emit func(*buildv2.Job_Event) error) (int64, error) {
		reads = append(reads, "snapshot")
		return serves("log\n")(id, run, from, emit)
	}

	for value, want := range map[string][]string{"false": {"snapshot", "follow"}, "0": {"snapshot", "follow"}, "true": {"snapshot"}, "1": {"snapshot"}} {
		reads = nil
		w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs?snapshot="+value, testToken))
		require.Equal(t, http.StatusOK, w.Code, value)
		require.Equal(t, "log\n", said(t, w.Body), value)
		require.Equal(t, want, reads, value)
	}
	for _, value := range []string{"maybe", "yes", "2"} {
		w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs?snapshot="+value, testToken))
		require.Equal(t, http.StatusBadRequest, w.Code, value)
	}
}

func TestAConsoleSeesWhichRunALogIs(t *testing.T) {
	h := newHarness(t, Config{Origins: []string{"https://console.example"}})
	h.backend.follow = serves("output\n")

	r := authed(http.MethodGet, "/job/"+testJobID+"/logs", testToken)
	r.Header.Set("Origin", "https://console.example")
	w := h.do(t, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Header().Get("Access-Control-Expose-Headers"), "X-Job-Attempt")
}
