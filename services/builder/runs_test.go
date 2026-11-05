package builder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	builderv2 "github.com/taubyte/tau/pkg/proto/gen/tau/builder/v2"
	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
	"github.com/taubyte/tau/services/plm/joblog"
	"github.com/taubyte/tau/services/plm/livelog"
	"github.com/taubyte/tau/services/plm/run"
	"github.com/taubyte/tau/services/storage"
)

// says is a build that writes what it is told, then feeds environments as a plan or
// builds nothing as a build.
type says struct {
	lines        string
	environments []string
}

func (s says) Plan(_ context.Context, _ run.Work, out io.Writer) ([]string, error) {
	io.WriteString(out, s.lines)
	return s.environments, nil
}

func (s says) Run(_ context.Context, _ run.Work, out run.Output) error {
	io.WriteString(out, s.lines)
	return nil
}

// fails is a build that writes what it is told, then fails with err.
type fails struct {
	lines string
	err   error
}

func (f fails) Plan(_ context.Context, _ run.Work, out io.Writer) ([]string, error) {
	io.WriteString(out, f.lines)
	return nil, f.err
}

func (f fails) Run(_ context.Context, _ run.Work, out run.Output) error {
	io.WriteString(out, f.lines)
	return f.err
}

// panics is a build that panics.
type panics struct{}

func (panics) Plan(context.Context, run.Work, io.Writer) ([]string, error) { panic("nil map write") }
func (panics) Run(context.Context, run.Work, run.Output) error             { panic("nil map write") }

// keeps is a build that keeps one artifact.
type keeps struct {
	resource string
	content  io.Reader
}

func (keeps) Plan(context.Context, run.Work, io.Writer) ([]string, error) { return nil, nil }

func (k keeps) Run(ctx context.Context, _ run.Work, out run.Output) error {
	return out.Asset(ctx, k.resource, k.content)
}

// waits is a build that says it started, then waits for its context or its release.
type waits struct {
	started chan run.Work
	release chan struct{}
}

func newWaits() waits { return waits{started: make(chan run.Work, 1), release: make(chan struct{})} }

func (w waits) Plan(ctx context.Context, work run.Work, out io.Writer) ([]string, error) {
	return []string{"prod"}, w.Run(ctx, work, nil)
}

func (w waits) Run(ctx context.Context, work run.Work, _ run.Output) error {
	w.started <- work
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-w.release:
		return nil
	}
}

// keeper is a storage service that keeps what it is handed, by its content.
type keeper struct {
	mu   sync.Mutex
	kept map[string][]byte
	meta map[string]map[string]string
	err  error
	// none answers with no cid.
	none bool
}

func (k *keeper) Put(_ context.Context, _ storage.Address, _ string, o storage.Object, content io.Reader) (storage.Version, error) {
	b, err := io.ReadAll(content)
	if err != nil {
		return storage.Version{}, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.err != nil {
		return storage.Version{}, k.err
	}
	if k.none {
		return storage.Version{}, nil
	}
	if k.kept == nil {
		k.kept, k.meta = map[string][]byte{}, map[string]map[string]string{}
	}
	cid := fmt.Sprintf("cid-%d", len(k.kept))
	k.kept[cid], k.meta[cid] = b, o.Metadata
	return storage.Version{Record: storage.VersionRecord{RootCID: cid}}, nil
}

// log is the messages of the log kept at cid, decompressed.
func (k *keeper) log(t *testing.T, cid string) ([]string, []plmv2.JobStatus) {
	t.Helper()
	k.mu.Lock()
	raw, ok := k.kept[cid]
	encoding := k.meta[cid]["encoding"]
	k.mu.Unlock()
	require.True(t, ok, "nothing was kept at %s", cid)
	require.Equal(t, livelog.Encoding, encoding, "a log is kept compressed, and says how")
	plain, err := io.ReadAll(livelog.Decompressed(io.NopCloser(bytes.NewReader(raw))))
	require.NoError(t, err)
	return eventsOf(t, joblog.Read(bytes.NewReader(plain), 0))
}

func eventsOf(t *testing.T, events *joblog.Events) ([]string, []plmv2.JobStatus) {
	t.Helper()
	var (
		lines    []string
		statuses []plmv2.JobStatus
	)
	for {
		ev, err := events.Next()
		if errors.Is(err, io.EOF) {
			return lines, statuses
		}
		require.NoError(t, err)
		switch k := ev.GetKind().(type) {
		case *plmv2.Job_Event_Message:
			lines = append(lines, k.Message)
		case *plmv2.Job_Event_Status:
			statuses = append(statuses, k.Status)
		}
	}
}

// now is a clock that says the wall's time.
type now struct{}

func (now) Now() time.Time { return time.Now() }

func newTestRuns(t *testing.T, exec run.Executor, assets Assets, logs Logs) *Runs {
	t.Helper()
	if logs.Dir == "" {
		logs.Dir = t.TempDir()
	}
	r, err := NewRuns(exec, nil, assets, now{}, logs)
	require.NoError(t, err)
	t.Cleanup(func() { r.Close() })
	return r
}

func work(job, environment string) run.Work {
	return run.Work{JobID: job, RepositoryKey: "github.com/acme/app", Ref: "refs/heads/main",
		CommitSHA: strings.Repeat("c", 40), ProjectID: "QmProject", Attempt: 1, Environment: environment}
}

func sealing(job string, status plmv2.JobStatus) *builderv2.Sealing {
	return &builderv2.Sealing{Run: &builderv2.Run{JobId: job, Attempt: 1}, Status: status}
}

func TestRunsNeedSomethingToRunWithAndAClock(t *testing.T) {
	_, err := NewRuns(nil, nil, nil, now{}, Logs{})
	require.Error(t, err)
	_, err = NewRuns(says{}, nil, nil, nil, Logs{})
	require.Error(t, err)
	_, err = NewRuns(says{}, nil, nil, now{}, Logs{MaxBytes: -1})
	require.Error(t, err)
}

func TestAPlanSaysWhatItFeedsAndWhatItSaid(t *testing.T) {
	r := newTestRuns(t, says{lines: "main feeds prod\n", environments: []string{"prod"}}, &keeper{}, Logs{})
	planned, err := r.Plan(t.Context(), work("plan", ""))
	require.NoError(t, err)
	require.Equal(t, []string{"prod"}, planned.GetEnvironments())
	require.Equal(t, "main feeds prod\n", planned.GetSaid())
	require.Empty(t, planned.GetError())

	sealed, err := r.Seal(t.Context(), sealing("plan", plmv2.JobStatus_JobStatusSuccess))
	require.NoError(t, err)
	require.Empty(t, sealed.GetLog(), "a plan keeps no log")
}

func TestAPlanThatSaysTooMuchIsCut(t *testing.T) {
	r := newTestRuns(t, says{lines: strings.Repeat("x", maxSaid+10)}, nil, Logs{})
	planned, err := r.Plan(t.Context(), work("plan", ""))
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(planned.GetSaid(), "... the plan said more than this\n"))
	require.Len(t, planned.GetSaid(), maxSaid+len("\n... the plan said more than this\n"))
}

func TestAPushIsConfirmedBeforeItIsPlanned(t *testing.T) {
	r, err := NewRuns(says{environments: []string{"prod"}}, refuses{run.ErrNotAtCommit}, nil, now{}, Logs{Dir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { r.Close() })
	planned, err := r.Plan(t.Context(), work("plan", ""))
	require.NoError(t, err)
	require.Equal(t, run.ErrNotAtCommit.Error(), planned.GetError())
	require.Empty(t, planned.GetEnvironments())
}

// refuses is a repository that answers err about every commit.
type refuses struct{ err error }

func (r refuses) Confirm(context.Context, string, string, string) error { return r.err }

func TestABuildKeepsWhatItBuiltAndItsLog(t *testing.T) {
	store := &keeper{}
	r := newTestRuns(t, keeps{resource: "QmResource", content: strings.NewReader("the artifact")}, store, Logs{})
	w := work("build", "prod")
	w.Prelude = "main feeds prod\n"
	built, err := r.Build(t.Context(), w)
	require.NoError(t, err)
	require.Empty(t, built.GetError())
	require.Len(t, built.GetStored(), 1)
	require.Equal(t, "QmResource", built.GetStored()[0].GetResourceId())
	require.Equal(t, uint64(len("the artifact")), built.GetStored()[0].GetBytes())

	sealed, err := r.Seal(t.Context(), sealing("build", plmv2.JobStatus_JobStatusSuccess))
	require.NoError(t, err)
	require.NotEmpty(t, sealed.GetLog())
	require.NotZero(t, sealed.GetBytes())
	lines, statuses := store.log(t, sealed.GetLog())
	require.Equal(t, []string{"main feeds prod"}, lines, "what the plan said opens the build's log")
	require.Equal(t, []plmv2.JobStatus{plmv2.JobStatus_JobStatusClaimed, plmv2.JobStatus_JobStatusSuccess}, statuses,
		"the log opens with the claim and ends with how the job was settled")

	_, err = r.Seal(t.Context(), sealing("build", plmv2.JobStatus_JobStatusSuccess))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "a sealed log is no longer here")
}

func TestAFailedBuildKeepsWhatItPrintedThenWhy(t *testing.T) {
	store := &keeper{}
	r := newTestRuns(t, fails{lines: "undefined: Frobnicate\n", err: errors.New("compile error")}, store, Logs{})
	built, err := r.Build(t.Context(), work("build", "prod"))
	require.NoError(t, err)
	require.Equal(t, "compile error", built.GetError())
	sealed, err := r.Seal(t.Context(), sealing("build", plmv2.JobStatus_JobStatusFailed))
	require.NoError(t, err)
	lines, _ := store.log(t, sealed.GetLog())
	require.Equal(t, []string{"undefined: Frobnicate", "error: compile error"}, lines)
}

func TestAPanickingBuildIsAFailedOne(t *testing.T) {
	r := newTestRuns(t, panics{}, nil, Logs{})
	built, err := r.Build(t.Context(), work("build", "prod"))
	require.NoError(t, err)
	require.Contains(t, built.GetError(), "builder panicked: nil map write")
	planned, err := r.Plan(t.Context(), work("plan", ""))
	require.NoError(t, err)
	require.Contains(t, planned.GetError(), "builder panicked")
}

func TestABuilderRunsOneRunAtATimeAndACancelStopsIt(t *testing.T) {
	build := newWaits()
	r := newTestRuns(t, build, nil, Logs{})
	done := make(chan *builderv2.Built, 1)
	go func() {
		built, err := r.Build(context.Background(), work("build", "prod"))
		require.NoError(t, err)
		done <- built
	}()
	<-build.started

	_, err := r.Plan(t.Context(), work("plan", ""))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	require.ErrorIs(t, err, ErrBusy)
	_, err = r.Seal(t.Context(), sealing("build", plmv2.JobStatus_JobStatusSuccess))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "a run still going is not sealed")

	require.NoError(t, r.Cancel(t.Context(), &builderv2.Run{JobId: "another", Attempt: 1}), "another run is nothing to stop")
	require.NoError(t, r.Cancel(t.Context(), &builderv2.Run{JobId: "build", Attempt: 1}))
	require.Equal(t, context.Canceled.Error(), (<-done).GetError())
}

func TestTheEndOfTheCallThatAskedEndsTheRun(t *testing.T) {
	build := newWaits()
	r := newTestRuns(t, build, nil, Logs{})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan *builderv2.Built, 1)
	go func() {
		built, err := r.Build(ctx, work("build", "prod"))
		require.NoError(t, err)
		done <- built
	}()
	<-build.started
	cancel()
	require.Equal(t, context.Canceled.Error(), (<-done).GetError())
}

func TestADiscardedLogIsNotKept(t *testing.T) {
	store := &keeper{}
	r := newTestRuns(t, says{lines: "half a build\n"}, store, Logs{})
	_, err := r.Build(t.Context(), work("build", "prod"))
	require.NoError(t, err)
	s := sealing("build", plmv2.JobStatus_JobStatusOpen)
	s.Discard = true
	sealed, err := r.Seal(t.Context(), s)
	require.NoError(t, err)
	require.Empty(t, sealed.GetLog())
	require.Empty(t, store.kept)
}

func TestALogPastItsCapIsCutAndTheBuildCarriesOn(t *testing.T) {
	store := &keeper{}
	long := strings.Repeat("well past the cap ", 8) + "\n"
	r := newTestRuns(t, says{lines: "first line\n" + long + "and more\n"}, store, Logs{MaxBytes: 64})
	built, err := r.Build(t.Context(), work("build", "prod"))
	require.NoError(t, err)
	require.Empty(t, built.GetError(), "a full log must not look like a broken pipe to the build")
	sealed, err := r.Seal(t.Context(), sealing("build", plmv2.JobStatus_JobStatusSuccess))
	require.NoError(t, err)
	lines, statuses := store.log(t, sealed.GetLog())
	require.Equal(t, "first line", lines[0])
	require.True(t, strings.HasPrefix(lines[1], "... the log was cut at "), lines[1])
	require.Len(t, lines, 2, "past the cap the output goes, and the log says so once")
	require.Equal(t, plmv2.JobStatus_JobStatusSuccess, statuses[len(statuses)-1], "but the run's end still goes in")
}

func TestALineLongerThanAMessageIsSentInPieces(t *testing.T) {
	store := &keeper{}
	r := newTestRuns(t, says{lines: strings.Repeat("y", maxMessage+5)}, store, Logs{})
	_, err := r.Build(t.Context(), work("build", "prod"))
	require.NoError(t, err)
	sealed, err := r.Seal(t.Context(), sealing("build", plmv2.JobStatus_JobStatusSuccess))
	require.NoError(t, err)
	lines, _ := store.log(t, sealed.GetLog())
	require.Equal(t, []string{strings.Repeat("y", maxMessage), "yyyyy"}, lines, "a line the run had not ended is still something it said")
}

func TestAnArtifactNeedsAnEnvironmentAResourceContentAndAStore(t *testing.T) {
	for name, tc := range map[string]struct {
		environment string
		build       keeps
		store       Assets
	}{
		"a plan":               {"", keeps{resource: "QmR", content: strings.NewReader("x")}, &keeper{}},
		"no resource":          {"prod", keeps{content: strings.NewReader("x")}, &keeper{}},
		"no content":           {"prod", keeps{resource: "QmR"}, &keeper{}},
		"no store":             {"prod", keeps{resource: "QmR", content: strings.NewReader("x")}, nil},
		"a store that refuses": {"prod", keeps{resource: "QmR", content: strings.NewReader("x")}, &keeper{err: errors.New("storage is down")}},
		"a store with no cid":  {"prod", keeps{resource: "QmR", content: strings.NewReader("x")}, &keeper{none: true}},
	} {
		t.Run(name, func(t *testing.T) {
			r := newTestRuns(t, tc.build, tc.store, Logs{})
			w := work("build", tc.environment)
			out := &output{writer: writer{l: &runLog{work: w}, r: r}, runs: r, work: w}
			require.Error(t, tc.build.Run(t.Context(), w, out))
			require.Empty(t, out.stored)
		})
	}
}

func TestAStoreThatRefusesTheLogCostsTheLogAndNotTheSeal(t *testing.T) {
	r := newTestRuns(t, says{lines: "built\n"}, &keeper{err: errors.New("storage is down")}, Logs{})
	_, err := r.Build(t.Context(), work("build", "prod"))
	require.NoError(t, err)
	sealed, err := r.Seal(t.Context(), sealing("build", plmv2.JobStatus_JobStatusSuccess))
	require.NoError(t, err)
	require.Empty(t, sealed.GetLog())
}

// shortStore reads four bytes, stops, and answers with a cid — a store that broke
// the half of its contract an io.Reader cannot state.
type shortStore struct{}

func (shortStore) Put(_ context.Context, _ storage.Address, _ string, _ storage.Object, content io.Reader) (storage.Version, error) {
	raw := make([]byte, 4)
	content.Read(raw)
	return storage.Version{Record: storage.VersionRecord{RootCID: "cid-short"}}, nil
}

// swallowingStore reads what it is handed, ignores a read that failed, and answers
// with a cid anyway.
type swallowingStore struct{}

func (swallowingStore) Put(_ context.Context, _ storage.Address, _ string, _ storage.Object, content io.Reader) (storage.Version, error) {
	io.Copy(io.Discard, content)
	return storage.Version{Record: storage.VersionRecord{RootCID: "cid-swallowed"}}, nil
}

func TestAStoreThatStoppedReadingDoesNotGetToNameTheLog(t *testing.T) {
	r := newTestRuns(t, says{lines: "cloning\ncompiling\nlinking\n"}, shortStore{}, Logs{})
	_, err := r.Build(t.Context(), work("build", "prod"))
	require.NoError(t, err)
	_, err = r.Seal(t.Context(), sealing("build", plmv2.JobStatus_JobStatusSuccess))
	require.ErrorContains(t, err, "stopped before its end")
}

// truncates is a build that shortens its own log file under it, which is what the
// log's own size measurement is against.
type truncates struct{ dir string }

func (truncates) Plan(context.Context, run.Work, io.Writer) ([]string, error) { return nil, nil }

func (t truncates) Run(_ context.Context, _ run.Work, out run.Output) error {
	io.WriteString(out, "cloning\n")
	files, err := filepath.Glob(filepath.Join(t.dir, "*.log"))
	if err != nil || len(files) != 1 {
		return errors.New("no log file")
	}
	return os.Truncate(files[0], 0)
}

func TestALogThatLostBytesIsNotStoredShort(t *testing.T) {
	for name, store := range map[string]Assets{"a store": &keeper{}, "a store that swallows the error": swallowingStore{}} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			r := newTestRuns(t, truncates{dir: dir}, store, Logs{Dir: dir})
			built, err := r.Build(t.Context(), work("build", "prod"))
			require.NoError(t, err)
			require.Empty(t, built.GetError())
			_, err = r.Seal(t.Context(), sealing("build", plmv2.JobStatus_JobStatusSuccess))
			require.Error(t, err, "a log this builder cannot read back whole is named by nothing")
		})
	}
}

func TestALogThatCannotBeReadBackIsAnError(t *testing.T) {
	r := newTestRuns(t, says{lines: "one line\n"}, &keeper{}, Logs{})
	_, err := r.Build(t.Context(), work("build", "prod"))
	require.NoError(t, err)
	r.mu.Lock()
	l := r.logs[runKey("build", 1)]
	r.mu.Unlock()
	require.NoError(t, l.log.Unlink())
	_, err = r.Seal(t.Context(), sealing("build", plmv2.JobStatus_JobStatusSuccess))
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestALogWhoseNameWentIsKeptAnyway(t *testing.T) {
	store := &keeper{}
	dir := t.TempDir()
	r := newTestRuns(t, says{lines: "one line\n"}, store, Logs{Dir: dir})
	_, err := r.Build(t.Context(), work("build", "prod"))
	require.NoError(t, err)
	files, err := filepath.Glob(filepath.Join(dir, "*.log"))
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.NoError(t, os.Remove(files[0]), "somebody else's removal of the name, which is what a tmp sweep is")
	sealed, err := r.Seal(t.Context(), sealing("build", plmv2.JobStatus_JobStatusSuccess))
	require.NoError(t, err)
	lines, _ := store.log(t, sealed.GetLog())
	require.Equal(t, []string{"one line"}, lines)
}

func TestARunWithNowhereToWriteItsLogIsTurnedDown(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(blocked, nil, 0o600))
	r := newTestRuns(t, says{}, nil, Logs{Dir: blocked})
	_, err := r.Build(t.Context(), work("build", "prod"))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	require.ErrorContains(t, err, "log directory")
}

func TestALogIsNeverOpenedThroughASymlinkPlantedAtItsPath(t *testing.T) {
	dir := t.TempDir()
	r := newTestRuns(t, says{}, nil, Logs{Dir: dir})
	_, err := r.Build(t.Context(), work("build", "prod"))
	require.NoError(t, err)
	files, err := filepath.Glob(filepath.Join(dir, "*.log"))
	require.NoError(t, err)
	require.Len(t, files, 1)
	planted := strings.Replace(files[0], ".1.log", ".2.log", 1)

	victim := filepath.Join(t.TempDir(), "authorized_keys")
	const secret = "ssh-ed25519 AAAA-the-operators-own-key\n"
	require.NoError(t, os.WriteFile(victim, []byte(secret), 0o600))
	require.NoError(t, os.Symlink(victim, planted))

	again := work("build", "prod")
	again.Attempt = 2
	_, err = r.Build(t.Context(), again)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err), "a builder that cannot open a log of its own does not run the build")
	kept, err := os.ReadFile(victim)
	require.NoError(t, err)
	require.Equal(t, secret, string(kept), "the file the link pointed at was not written through")
}

func TestTheLogDirectoryIsOneNobodyElseCanWriteIn(t *testing.T) {
	shared := filepath.Join(t.TempDir(), "tau-builder-logs")
	require.NoError(t, os.Mkdir(shared, 0o777))
	r := newTestRuns(t, says{}, nil, Logs{Dir: shared})
	_, err := r.Build(t.Context(), work("build", "prod"))
	require.NoError(t, err)
	fi, err := os.Lstat(shared)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), fi.Mode().Perm(), "a shared directory is tightened before a run writes in it")

	logs, err := Logs{}.withDefaults()
	require.NoError(t, err)
	require.Equal(t, defaultLogDir(), logs.Dir)
	require.Equal(t, int64(DefaultMaxLogBytes), logs.MaxBytes)
	require.Equal(t, DefaultSealWithin, logs.SealWithin)
	require.Equal(t, DefaultKeepWithin, logs.KeepWithin)
}

func TestARunningBuildsLogIsFollowedWhileItIsBeingWritten(t *testing.T) {
	build := &speaks{lines: make(chan string), started: make(chan struct{})}
	r := newTestRuns(t, build, &keeper{}, Logs{})
	done := make(chan error, 1)
	go func() {
		_, err := r.Build(context.Background(), work("build", "prod"))
		done <- err
	}()
	<-build.started

	following, err := r.Follow(t.Context(), &plmv2.FollowRequest{JobId: "build", Attempt: 1})
	require.NoError(t, err)
	defer following.Close()
	first, err := following.Receive()
	require.NoError(t, err)
	require.Equal(t, plmv2.JobStatus_JobStatusClaimed, first.GetStatus())
	build.lines <- "cloning\n"
	next, err := following.Receive()
	require.NoError(t, err)
	require.Equal(t, "cloning", next.GetMessage())

	snapshot, err := r.Follow(t.Context(), &plmv2.FollowRequest{JobId: "build", Attempt: 1, Snapshot: true})
	require.NoError(t, err)
	lines, statuses := eventsOf(t, snapshot.(*events).read)
	require.Equal(t, []string{"cloning"}, lines, "a snapshot is what the log holds now")
	require.Equal(t, []plmv2.JobStatus{plmv2.JobStatus_JobStatusClaimed}, statuses)
	snapshot.Close()

	resumed, err := r.Follow(t.Context(), &plmv2.FollowRequest{JobId: "build", Attempt: 1, Offset: next.GetOffset(), Snapshot: true})
	require.NoError(t, err)
	_, err = resumed.Receive()
	require.ErrorIs(t, err, io.EOF, "a follower resuming at the end has nothing more yet")
	resumed.Close()

	close(build.lines)
	require.NoError(t, <-done)
	_, err = r.Seal(t.Context(), sealing("build", plmv2.JobStatus_JobStatusSuccess))
	require.NoError(t, err)
	last, err := following.Receive()
	require.NoError(t, err)
	require.Equal(t, plmv2.JobStatus_JobStatusSuccess, last.GetStatus(), "the seal is the follower's last event")
	_, err = following.Receive()
	require.ErrorIs(t, err, io.EOF)
}

// speaks is a build that writes each line it is handed until they stop.
type speaks struct {
	lines   chan string
	started chan struct{}
}

func (*speaks) Plan(context.Context, run.Work, io.Writer) ([]string, error) { return nil, nil }

func (s *speaks) Run(_ context.Context, _ run.Work, out run.Output) error {
	close(s.started)
	for line := range s.lines {
		io.WriteString(out, line)
	}
	return nil
}

func TestFollowRefusesWhatItCannotAnswer(t *testing.T) {
	r := newTestRuns(t, says{}, nil, Logs{})
	_, err := r.Follow(t.Context(), &plmv2.FollowRequest{JobId: "build", Attempt: 0})
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	_, err = r.Follow(t.Context(), &plmv2.FollowRequest{JobId: "build", Attempt: 1})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	_, err = r.Build(t.Context(), work("build", "prod"))
	require.NoError(t, err)
	_, err = r.Follow(t.Context(), &plmv2.FollowRequest{JobId: "build", Attempt: 1, Offset: 1 << 20})
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "an offset past the end is the caller's arithmetic")

	r.mu.Lock()
	l := r.logs[runKey("build", 1)]
	r.mu.Unlock()
	require.NoError(t, l.log.Unlink())
	_, err = r.Follow(t.Context(), &plmv2.FollowRequest{JobId: "build", Attempt: 1})
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err), "a log it cannot open is said so")
}

func TestALogNobodySealsIsLetGo(t *testing.T) {
	dir := t.TempDir()
	r := newTestRuns(t, says{lines: "built\n"}, nil, Logs{Dir: dir, SealWithin: 10 * time.Millisecond})
	_, err := r.Build(t.Context(), work("build", "prod"))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_, err := r.Seal(t.Context(), sealing("build", plmv2.JobStatus_JobStatusSuccess))
		return connect.CodeOf(err) == connect.CodeNotFound
	}, 5*time.Second, 10*time.Millisecond)
	files, err := filepath.Glob(filepath.Join(dir, "*.log"))
	require.NoError(t, err)
	require.Empty(t, files)
}

func TestARetryOfARunReplacesTheLogItLeft(t *testing.T) {
	dir := t.TempDir()
	r := newTestRuns(t, says{lines: "built\n"}, nil, Logs{Dir: dir})
	_, err := r.Build(t.Context(), work("build", "prod"))
	require.NoError(t, err)
	_, err = r.Build(t.Context(), work("build", "prod"))
	require.NoError(t, err)
	files, err := filepath.Glob(filepath.Join(dir, "*.log"))
	require.NoError(t, err)
	require.Len(t, files, 1, "the same run again is one log")

	again := work("build", "prod")
	again.Attempt = 2
	_, err = r.Build(t.Context(), again)
	require.NoError(t, err)
	files, err = filepath.Glob(filepath.Join(dir, "*.log"))
	require.NoError(t, err)
	require.Len(t, files, 2, "two attempts of one job write different files")
}

func TestClosingRunsDropsEveryLogAndStopsTheRunInHand(t *testing.T) {
	dir := t.TempDir()
	build := newWaits()
	r, err := NewRuns(build, nil, nil, now{}, Logs{Dir: dir})
	require.NoError(t, err)
	done := make(chan *builderv2.Built, 1)
	go func() {
		built, err := r.Build(context.Background(), work("build", "prod"))
		require.NoError(t, err)
		done <- built
	}()
	<-build.started
	require.NoError(t, r.Close())
	require.Equal(t, context.Canceled.Error(), (<-done).GetError())
	files, err := filepath.Glob(filepath.Join(dir, "*.log"))
	require.NoError(t, err)
	require.Empty(t, files)
}

func TestAWriteToAnEndedLogIsReportedToTheBuild(t *testing.T) {
	r := newTestRuns(t, says{}, nil, Logs{})
	_, err := r.Build(t.Context(), work("build", "prod"))
	require.NoError(t, err)
	r.mu.Lock()
	l := r.logs[runKey("build", 1)]
	r.mu.Unlock()
	r.end(l)
	r.end(l)
	n, err := writer{l: l, r: r}.Write([]byte("late"))
	require.ErrorIs(t, err, livelog.ErrClosed)
	require.Zero(t, n)
	require.ErrorIs(t, r.status(l, plmv2.JobStatus_JobStatusSuccess), livelog.ErrClosed)
}
