package builder

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"connectrpc.com/connect"

	builderv2 "github.com/taubyte/tau/pkg/proto/gen/tau/builder/v2"
	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
	"github.com/taubyte/tau/services/plm/joblog"
	"github.com/taubyte/tau/services/plm/livelog"
	"github.com/taubyte/tau/services/plm/run"
	"github.com/taubyte/tau/services/storage"
)

// runs.go is a builder's runs, in the process that holds them: the builder service
// serves them over its protocol, and a development cloud running no builder calls
// them in place (instance/dream). Each run writes its log here as it goes, and the
// log stays until plm seals it.

// LogsInstance is the instance of an environment's assets bucket its builds' logs
// are kept in, one version per run. A package's artifacts are the instance named by
// its id, a CID, so the two never meet.
const LogsInstance = "logs"

// maxMessage is how long one message of a log may be; a longer line is cut into
// pieces, since one line of a stranger's plm can be the whole log.
const maxMessage = 16 << 10

// maxSaid bounds what a plan says, which opens every environment job's log.
const maxSaid = 64 << 10

// Clock is what a run's events are stamped with.
type Clock interface {
	Now() time.Time
}

// Runs is a builder's runs: one at a time, each one's log kept until it is sealed
// or, unsealed, until SealWithin has passed.
type Runs struct {
	exec    run.Executor
	confirm run.Confirmer
	assets  Assets
	clock   Clock
	cfg     Logs

	mu      sync.Mutex
	running *inHand
	logs    map[string]*runLog

	also []io.Closer
}

var _ run.Builder = (*Runs)(nil)

// inHand is the run going on.
type inHand struct {
	job     string
	attempt uint32
	cancel  context.CancelFunc
}

// runLog is one run's log and what sealing it needs.
type runLog struct {
	work  run.Work
	log   *livelog.Log
	lines lineWriter
	size  int64
	cut   bool
	ended bool
	// sealed is the log ended by plm's seal, still followed whole until expiry.
	sealed bool
	expiry *time.Timer
	// written orders what is written to the log, which a build may do from more than
	// one goroutine.
	written sync.Mutex
}

// NewRuns is runs executed by exec, each push confirmed by confirm first when there is
// one, artifacts and logs kept in assets when there is one, events stamped by clock.
func NewRuns(exec run.Executor, confirm run.Confirmer, assets Assets, clock Clock, logs Logs) (*Runs, error) {
	if exec == nil || clock == nil {
		return nil, errors.New("builder: runs need something to run with and a clock")
	}
	logs, err := logs.withDefaults()
	if err != nil {
		return nil, err
	}
	return newRuns(exec, confirm, assets, clock, logs), nil
}

func newRuns(exec run.Executor, confirm run.Confirmer, assets Assets, clock Clock, logs Logs) *Runs {
	return &Runs{exec: exec, confirm: confirm, assets: assets, clock: clock, cfg: logs, logs: map[string]*runLog{}}
}

// ErrBusy is a builder asked to run while it runs something.
var ErrBusy = errors.New("builder: already running a job")

// take is w in hand: its log opened, and a context its Cancel ends, as does the
// end of the call that asked for it.
func (r *Runs) take(ctx context.Context, w run.Work) (context.Context, *runLog, func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running != nil {
		return nil, nil, nil, connect.NewError(connect.CodeUnavailable, ErrBusy)
	}
	// THE SAME RUN AGAIN replaces the log it left, whose file has the same name.
	key := runKey(w.JobID, w.Attempt)
	if old, ok := r.logs[key]; ok {
		r.drop(key, old)
	}
	l, err := r.open(w)
	if err != nil {
		return nil, nil, nil, err
	}
	var cancel context.CancelFunc
	if w.Within > 0 {
		ctx, cancel = context.WithTimeout(ctx, w.Within)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	r.running = &inHand{job: w.JobID, attempt: w.Attempt, cancel: cancel}
	r.logs[key] = l
	l.expiry = time.AfterFunc(r.cfg.SealWithin, func() { r.expire(key, l) })
	return ctx, l, func() {
		cancel()
		r.mu.Lock()
		r.running = nil
		r.mu.Unlock()
	}, nil
}

func runKey(job string, attempt uint32) string {
	return job + "/" + strconv.FormatUint(uint64(attempt), 10)
}

// open creates a run's log, named for its job and attempt so a retry gets its own.
// The name is computable by anyone who knows the job, which is why livelog.OwnDir
// and Create refuse a directory or file someone else planted.
func (r *Runs) open(w run.Work) (*runLog, error) {
	if err := livelog.OwnDir(r.cfg.Dir); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("builder: log directory %s: %w", r.cfg.Dir, err))
	}
	sum := sha256.Sum256([]byte(w.JobID))
	name := hex.EncodeToString(sum[:12]) + "." + strconv.FormatUint(uint64(w.Attempt), 10) + ".log"
	log, err := livelog.Create(filepath.Join(r.cfg.Dir, name))
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("builder: job %s: %w", w.JobID, err))
	}
	l := &runLog{work: w, log: log}
	l.lines = lineWriter{to: messages{l: l, r: r}}
	// THE LOG OPENS WITH THE RUN'S CLAIM, so it says from its first record what it is
	// the log of.
	if err := r.status(l, plmv2.JobStatus_JobStatusClaimed); err != nil {
		log.Close()
		log.Unlink()
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("builder: job %s: %w", w.JobID, err))
	}
	return l, nil
}

// Plan reads which environments the pushed commit feeds, confirmed at the forge
// first, so a push is built only for what the repository holds.
func (r *Runs) Plan(ctx context.Context, w run.Work) (*builderv2.Planned, error) {
	ctx, l, done, err := r.take(ctx, w)
	if err != nil {
		return nil, err
	}
	defer done()
	said := &capped{max: maxSaid}
	environments, err := r.plan(ctx, w, io.MultiWriter(said, writer{l: l, r: r}))
	out := &builderv2.Planned{Environments: environments, Said: said.String()}
	if err != nil {
		r.say(l, "error: %v\n", err)
		out.Error = err.Error()
	}
	return out, nil
}

func (r *Runs) plan(ctx context.Context, w run.Work, out io.Writer) (environments []string, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("builder panicked: %v", p)
		}
	}()
	if r.confirm != nil {
		if err := r.confirm.Confirm(ctx, w.RepositoryKey, w.Ref, w.CommitSHA); err != nil {
			return nil, err
		}
	}
	return r.exec.Plan(ctx, w, out)
}

// Build builds one environment, and returns once the run has ended, what it
// stored kept either way.
func (r *Runs) Build(ctx context.Context, w run.Work) (*builderv2.Built, error) {
	ctx, l, done, err := r.take(ctx, w)
	if err != nil {
		return nil, err
	}
	defer done()
	out := &output{writer: writer{l: l, r: r}, runs: r, work: w}
	if w.Prelude != "" {
		io.WriteString(out, w.Prelude)
	}
	built := &builderv2.Built{}
	if err := r.build(ctx, w, out); err != nil {
		r.say(l, "error: %v\n", err)
		built.Error = err.Error()
	}
	out.mu.Lock()
	built.Stored = out.stored
	out.mu.Unlock()
	return built, nil
}

// build runs the job; a panic is a failed build, not a dead builder.
func (r *Runs) build(ctx context.Context, w run.Work, out run.Output) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("builder panicked: %v", p)
		}
	}()
	return r.exec.Run(ctx, w, out)
}

// Cancel stops the run in hand when it is that one.
func (r *Runs) Cancel(_ context.Context, run *builderv2.Run) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h := r.running; h != nil && h.job == run.GetJobId() && h.attempt == run.GetAttempt() {
		h.cancel()
	}
	return nil
}

// Seal ends a run's log with the status its job was settled with, and keeps a
// build's in the storage service. Followers that forked the log read it to its end;
// it is no longer here to fork.
func (r *Runs) Seal(ctx context.Context, s *builderv2.Sealing) (*builderv2.Sealed, error) {
	key := runKey(s.GetRun().GetJobId(), s.GetRun().GetAttempt())
	r.mu.Lock()
	l, ok := r.logs[key]
	busy := ok && r.running != nil && runKey(r.running.job, r.running.attempt) == key
	r.mu.Unlock()
	switch {
	case !ok:
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("builder: no log of attempt %d of job %s is here", s.GetRun().GetAttempt(), s.GetRun().GetJobId()))
	case busy:
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("builder: attempt %d of job %s is still running", s.GetRun().GetAttempt(), s.GetRun().GetJobId()))
	}
	r.mu.Lock()
	sealed := l.sealed
	l.sealed = true
	r.mu.Unlock()
	if sealed {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("builder: attempt %d of job %s is sealed already", s.GetRun().GetAttempt(), s.GetRun().GetJobId()))
	}
	// A SEALED LOG IS STILL FOLLOWED WHOLE for KeepSealed, so a follower that comes
	// as the run ends reads all of it; a discarded one goes at once.
	defer func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.logs[key] != l {
			return
		}
		if s.GetDiscard() {
			r.drop(key, l)
			return
		}
		l.expiry.Reset(r.cfg.KeepSealed)
	}()
	for _, line := range s.GetSaid() {
		r.say(l, "%s\n", line)
	}
	if err := r.status(l, s.GetStatus()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("builder: job %s: writing its end into its log: %w", l.work.JobID, err))
	}
	r.end(l)
	if r.assets == nil || l.work.Environment == "" || s.GetDiscard() {
		return &builderv2.Sealed{}, nil
	}
	return r.keepLog(ctx, l)
}

// keepLog hands a sealed log to the storage service, compressed, reading it through
// a fork so its bytes stream rather than sit in memory. A store failure costs the
// log, not the seal; an unreadable log or a store that took less than all of it is
// an error.
func (r *Runs) keepLog(ctx context.Context, l *runLog) (*builderv2.Sealed, error) {
	whole, err := l.log.Fork(0)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("builder: job %s: reading back its log: %w", l.work.JobID, err))
	}
	defer whole.Close()
	ctx, stop := context.WithTimeout(context.WithoutCancel(ctx), r.cfg.KeepWithin)
	defer stop()
	at, err := storage.NewAddress(l.work.ProjectID, l.work.Environment, storage.AssetsBucket, LogsInstance)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("builder: job %s: %w", l.work.JobID, err))
	}
	source := &counting{from: whole}
	stream := livelog.Compressed(source)
	v, err := r.assets.Put(ctx, at, l.work.JobID+"/"+fmt.Sprintf("%020d", l.work.Attempt),
		storage.Object{ContentType: "text/plain", Metadata: map[string]string{"encoding": livelog.Encoding}},
		stream)
	// A store that stopped reading early leaves the compressor writing into the pipe;
	// closing it ends that.
	stream.Close()
	switch {
	case source.err != nil:
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("builder: job %s: reading back its log: %w", l.work.JobID, source.err))
	case err != nil:
		return &builderv2.Sealed{}, nil
	case !stream.Ended():
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("builder: job %s: the storage service took %d bytes of its compressed log and stopped before its end", l.work.JobID, stream.Taken()))
	case source.n != l.log.Size():
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("builder: job %s: the storage service took %d of the %d bytes of its log", l.work.JobID, source.n, l.log.Size()))
	}
	return &builderv2.Sealed{Log: v.Record.RootCID, Bytes: uint64(source.n)}, nil
}

// Follow reads a run's log from req's offset: to its seal, or with snapshot to what
// it holds now. The read ends with ctx, which a read waiting at the log's end does
// not see on its own.
func (r *Runs) Follow(ctx context.Context, req *plmv2.FollowRequest) (run.Events, error) {
	if req.GetAttempt() < 1 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("builder: attempt %d is not a run", req.GetAttempt()))
	}
	r.mu.Lock()
	l, ok := r.logs[runKey(req.GetJobId(), uint32(req.GetAttempt()))]
	r.mu.Unlock()
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("builder: no log of attempt %d of job %s is here", req.GetAttempt(), req.GetJobId()))
	}
	from := req.GetOffset()
	fork, err := l.log.Fork(from)
	if err != nil {
		if errors.Is(err, livelog.ErrOffset) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("builder: following job %s: %w", req.GetJobId(), err))
	}
	var read io.Reader = fork
	if req.GetSnapshot() {
		read = io.LimitReader(fork, l.log.Size()-from)
	}
	return &events{read: joblog.Read(read, from), fork: fork, stop: context.AfterFunc(ctx, func() { fork.Close() })}, nil
}

// events is a fork of a log, read as its events.
type events struct {
	read *joblog.Events
	fork *livelog.Reader
	stop func() bool
}

func (e *events) Receive() (*plmv2.Job_Event, error) { return e.read.Next() }

func (e *events) Close() error {
	e.stop()
	return e.fork.Close()
}

// expire drops a log whose time is up: unsealed after SealWithin, since nobody
// will seal it now, and sealed after KeepSealed.
func (r *Runs) expire(key string, l *runLog) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.logs[key] == l && (r.running == nil || runKey(r.running.job, r.running.attempt) != key) {
		r.drop(key, l)
	}
}

// drop forgets a log and removes its file; readers that forked keep draining, since
// the inode outlives the name. Called holding mu.
func (r *Runs) drop(key string, l *runLog) {
	delete(r.logs, key)
	if l.expiry != nil {
		l.expiry.Stop()
	}
	r.end(l)
	l.log.Unlink()
}

// end closes a log, which releases every reader waiting at its end, the line the run
// had not ended still written.
func (r *Runs) end(l *runLog) {
	l.written.Lock()
	defer l.written.Unlock()
	if l.ended {
		return
	}
	l.ended = true
	l.lines.flush()
	l.log.Close()
}

// say is one line of the builder's own in a run's log.
func (r *Runs) say(l *runLog, format string, args ...any) {
	fmt.Fprintf(writer{l: l, r: r}, format, args...)
}

// status records a status in a run's log.
func (r *Runs) status(l *runLog, st plmv2.JobStatus) error {
	l.written.Lock()
	defer l.written.Unlock()
	if l.ended {
		return livelog.ErrClosed
	}
	if err := l.lines.flush(); err != nil {
		return err
	}
	return r.record(l, &plmv2.Job_Event{Kind: &plmv2.Job_Event_Status{Status: st}})
}

// record appends one event at this builder's instant. Called holding l.written.
func (r *Runs) record(l *runLog, ev *plmv2.Job_Event) error {
	ev.At = r.clock.Now().UnixNano()
	before := l.log.Size()
	if err := joblog.Write(l.log, ev); err != nil {
		return err
	}
	l.size += l.log.Size() - before
	return nil
}

// messages is a log's lines, each a message, dropped once the log reached its cap.
type messages struct {
	l *runLog
	r *Runs
}

func (m messages) Send(line string) error {
	if m.l.cut {
		return nil
	}
	ev := &plmv2.Job_Event{Kind: &plmv2.Job_Event_Message{Message: line}}
	if max := m.r.cfg.MaxBytes; max > 0 && m.l.size+int64(ev.SizeVT()+4) > max {
		m.l.cut = true
		ev = &plmv2.Job_Event{Kind: &plmv2.Job_Event_Message{Message: fmt.Sprintf("... the log was cut at %d bytes", m.l.size)}}
	}
	return m.r.record(m.l, ev)
}

// writer is what a run writes to its log. A full log is not a failed write: the log
// says it was cut, once, and the run carries on.
type writer struct {
	l *runLog
	r *Runs
}

func (w writer) Write(p []byte) (int, error) {
	w.l.written.Lock()
	defer w.l.written.Unlock()
	if w.l.ended {
		return 0, livelog.ErrClosed
	}
	return w.l.lines.Write(p)
}

// lineSender is where a lineWriter sends each line.
type lineSender interface {
	Send(line string) error
}

// lineWriter cuts output into lines, each a message; a line longer than maxMessage
// is sent in pieces.
type lineWriter struct {
	to      lineSender
	pending []byte
}

func (l *lineWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		end := i
		if i < 0 {
			end = len(p)
		}
		l.pending = append(l.pending, p[:end]...)
		for len(l.pending) >= maxMessage {
			piece := string(l.pending[:maxMessage])
			l.pending = append(l.pending[:0], l.pending[maxMessage:]...)
			if err := l.to.Send(piece); err != nil {
				return n, err
			}
		}
		if i < 0 {
			return n, nil
		}
		p = p[i+1:]
		if err := l.flush(); err != nil {
			return n, err
		}
	}
	return n, nil
}

// flush sends the line held, ended or not.
func (l *lineWriter) flush() error {
	if len(l.pending) == 0 {
		return nil
	}
	line := string(l.pending)
	l.pending = l.pending[:0]
	return l.to.Send(line)
}

// capped keeps what is written up to max bytes and drops the rest, saying so once.
type capped struct {
	max  int
	buf  bytes.Buffer
	full bool
}

func (c *capped) Write(p []byte) (int, error) {
	room := c.max - c.buf.Len()
	if room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	}
	if len(p) > room && !c.full {
		c.full = true
		c.buf.WriteString("\n... the plan said more than this\n")
	}
	return len(p), nil
}

func (c *capped) String() string { return c.buf.String() }

// output is a build's run.Output: its log, and its artifacts kept in the storage
// service by this builder, so their bytes never cross build.
type output struct {
	writer
	runs *Runs
	work run.Work

	mu     sync.Mutex
	stored []*builderv2.Stored
}

func (o *output) Asset(ctx context.Context, resourceID string, content io.Reader) error {
	switch {
	case o.work.Environment == "":
		return errors.New("builder: a plan builds nothing, so it has no artifact to keep")
	case resourceID == "":
		return errors.New("builder: an artifact needs a resource id")
	case content == nil:
		return errors.New("builder: an artifact needs content")
	case o.runs.assets == nil:
		return connect.NewError(connect.CodeUnavailable, errors.New("builder: no storage service to keep an artifact in"))
	}
	at, err := storage.NewAddress(o.work.ProjectID, o.work.Environment, storage.AssetsBucket, resourceID)
	if err != nil {
		return fmt.Errorf("builder: job %s: keeping %s: %w", o.work.JobID, resourceID, err)
	}
	counted := &counting{from: content}
	v, err := o.runs.assets.Put(ctx, at, o.work.JobID, storage.Object{}, counted)
	if err != nil {
		return fmt.Errorf("builder: job %s: keeping %s: %w", o.work.JobID, resourceID, err)
	}
	if v.Record.RootCID == "" {
		return fmt.Errorf("builder: job %s: the storage service named no cid for %s", o.work.JobID, resourceID)
	}
	o.mu.Lock()
	o.stored = append(o.stored, &builderv2.Stored{ResourceId: resourceID, Cid: v.Record.RootCID, Bytes: uint64(counted.n)})
	o.mu.Unlock()
	return nil
}

// counting counts what it hands over, and keeps the first read that failed: an
// artifact's size is what a plan bills, and a cid says nothing about it.
type counting struct {
	from io.Reader
	n    int64
	err  error
}

func (c *counting) Read(p []byte) (int, error) {
	n, err := c.from.Read(p)
	c.n += int64(n)
	if err != nil && !errors.Is(err, io.EOF) && c.err == nil {
		c.err = err
	}
	return n, err
}

// Close drops every log still held, and closes what Open opened.
func (r *Runs) Close() error {
	r.mu.Lock()
	if r.running != nil {
		r.running.cancel()
	}
	for key, l := range r.logs {
		r.drop(key, l)
	}
	r.mu.Unlock()
	var errs []error
	for i := len(r.also) - 1; i >= 0; i-- {
		errs = append(errs, r.also[i].Close())
	}
	r.also = nil
	return errors.Join(errs...)
}

// defaultLogDir is the scratch log directory, per uid so a shared /tmp name cannot
// be created first by another account (which livelog.OwnDir would then refuse).
func defaultLogDir() string {
	return filepath.Join(os.TempDir(), "tau-builder-logs-"+strconv.Itoa(os.Getuid()))
}
