package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"

	"github.com/taubyte/tau/services/build/livelog"
	"github.com/taubyte/tau/services/storage"
)

// output.go is what a run writes. No replicated row is written outside a section;
// the asset store, though, is reached outside every section, as it must be, since
// a section is jobLock over the whole job table:
//
//	Asset()    while the build runs, from the runner's goroutine
//	publish()  between settle's two sections
//
// What that writer leaves is content, whose cid reaches a record only from the
// settling section; population_test.go enumerates who may call Put and where.
// A log lives as a file while the build runs (services/build/livelog), is read
// at any offset by followers, and at the end is handed to the asset store with
// its cid recorded in the settling entry.

// produced is what one run left behind: its log's cid and one cid per resource,
// with its own attempt, since the record re-read after the build may carry
// another.
// assetRef is which artifact a cid is, kept as a pair so the settling section
// composes the key from two fields rather than parsing a string.
type assetRef struct {
	environment string
	resource    string
}

type produced struct {
	attempt uint32
	logCID  string
	assets  map[assetRef]string
	// children is the environment jobs a plan enqueued.
	children []string
}

// LogsInstance is the instance of an environment's assets bucket its builds' logs
// are kept in, one version per run. A package's artifacts are the instance named by
// its id, a CID, so the two never meet.
const LogsInstance = "logs"

// logEncoding is how a log is compressed before it is stored; see livelog.Compressed.
const logEncoding = livelog.Encoding

// runOutput is one run's Output: its log file and the assets it hands over. It
// reaches no replicated store, only the asset store.
type runOutput struct {
	s           *Service
	job         string
	project     string
	environment string
	attempt     uint32
	log         *livelog.Log

	// mu guards built, because Asset may be called from whatever goroutine the
	// runner builds a resource on while another writes the log.
	mu    sync.Mutex
	built map[assetRef]string
	// ended is this type's own idempotence: discard is deferred by run and
	// publish closes on its way in, so the two meet on every path.
	ended bool
}

// output opens a run's log and registers it for following. The file is named for
// job and attempt through store.go's seg, so it stays in the log directory and a
// retry gets its own. The name is computable by anyone who knows the repository,
// which is why livelog.OwnDir and Create refuse a directory or file someone else
// planted.
func (s *Service) output(w Work) (*runOutput, error) {
	if err := livelog.OwnDir(s.cfg.LogDir); err != nil {
		return nil, errUnavailable("build: log directory %s: %v", s.cfg.LogDir, err)
	}

	name := seg(w.JobID) + "." + stamp(int64(w.Attempt)) + ".log"
	log, err := livelog.Create(filepath.Join(s.cfg.LogDir, name), livelog.WithMaxBytes(s.cfg.MaxLogBytes))
	if err != nil {
		return nil, errUnavailable("build: job %s: %v", w.JobID, err)
	}

	s.logsMu.Lock()
	s.logs[w.JobID] = runLog{attempt: w.Attempt, log: log}
	s.logsMu.Unlock()

	return &runOutput{
		s: s, job: w.JobID, project: w.ProjectID, environment: w.Environment, attempt: w.Attempt, log: log,
		built: map[assetRef]string{},
	}, nil
}

// Write is the log. A full log is not a failed write: livelog writes the notice
// and the run carries on.
func (o *runOutput) Write(p []byte) (int, error) {
	n, err := o.log.Write(p)
	if errors.Is(err, livelog.ErrFull) {
		return len(p), nil
	}
	return n, err
}

// Asset hands one artifact to the asset store now and keeps its cid for publish.
// Storing is content-addressed, so a failing run costs nothing; the record is
// written once, by the settling section. Called only by the runner; only publish
// reads o.built.
func (o *runOutput) Asset(ctx context.Context, resourceID string, content io.Reader) error {
	if o.environment == "" {
		return errors.New("build: a plan builds nothing, so it has no asset to store")
	}
	if resourceID == "" {
		return errors.New("build: an asset needs a resource id")
	}
	if content == nil {
		return errors.New("build: an asset needs content")
	}
	if o.s.assetStore == nil {
		return errUnavailable("build: this node has no asset store to put %s in", resourceID)
	}

	// THE SAME READER THE LOG GOES THROUGH, for the count and for nothing else:
	// an artifact's bytes are what a plan bills, and a cid says nothing about
	// size. Its error half is unused here — an artifact that failed to read is
	// already the runner's failure, which is where it is raised.
	at, err := storage.NewAddress(o.project, o.environment, storage.AssetsBucket, resourceID)
	if err != nil {
		return fmt.Errorf("build: job %s: storing %s: %w", o.job, resourceID, err)
	}
	counted := &handover{from: content}
	v, err := o.s.assetStore.Put(ctx, at, o.job, storage.Object{}, counted)
	if err != nil {
		return fmt.Errorf("build: job %s: storing %s: %w", o.job, resourceID, err)
	}
	if v.Record.RootCID == "" {
		return errInternal("build: job %s: the asset store returned no cid for %s", o.job, resourceID)
	}

	o.mu.Lock()
	o.built[assetRef{environment: o.environment, resource: resourceID}] = v.Record.RootCID
	o.mu.Unlock()

	// Counted where stored: a cid says nothing about size.
	o.s.emit(ctx, Accounted{
		Project:     o.project,
		Environment: o.environment,
		Job:         o.job,
		Attempt:     o.attempt,
		Op:          OpArtifact,
		Bytes:       uint64(counted.n),
	})
	return nil
}

// publish ends the log and hands it to the asset store, between settle's two
// sections: past the ack (a stale claim ships nothing) and outside jobLock (a
// slow store stalls no other job). A cid reaches a job only for a log stored
// whole; a stored log may still reach no job if leadership is lost before the
// recording entry (doc.go). It reads the log by forking a reader, so the bytes
// stream without being held in memory. Its deadline is PublishTimeout off a
// context the run's cancellation cannot reach. A store failure costs the log; an
// unreadable log fails the build; a short read is caught by counting bytes.
func (o *runOutput) publish(ctx context.Context) (produced, error) {
	// A NIL OUTPUT IS A RUN THAT NEVER STARTED — this node could not open a log
	// for it — and it produced nothing. settle is the only caller, and it hands
	// over whatever the run left rather than branching on whether there was
	// one.
	if o == nil {
		return produced{}, nil
	}

	o.end()

	o.mu.Lock()
	p := produced{attempt: o.attempt, assets: copied(o.built)}
	o.mu.Unlock()

	// A plan keeps no log: what it said opens each environment job's.
	if o.s.assetStore == nil || o.environment == "" {
		return p, nil
	}

	// The fork is taken AFTER end() closed the log, so the read stops at the
	// last byte instead of blocking for a writer that is gone. It still reaches
	// the bytes: a log holds its inode until it is unlinked, and unlinking is
	// discard's (livelog).
	whole, err := o.log.Fork(0)
	if err != nil {
		return p, errInternal("build: job %s: reading back the log it wrote: %v", o.job, err)
	}
	defer whole.Close()

	// `stop`, not `cancel`: the censuses read call-site names, and Service.cancel
	// opens a section.
	ctx, stop := context.WithTimeout(context.WithoutCancel(ctx), o.s.cfg.PublishTimeout)
	defer stop()

	at, err := storage.NewAddress(o.project, o.environment, storage.AssetsBucket, LogsInstance)
	if err != nil {
		return p, errInternal("build: job %s: %v", o.job, err)
	}
	source := &handover{from: whole}
	stream := livelog.Compressed(source)
	v, err := o.s.assetStore.Put(ctx, at, o.job+"/"+stamp(int64(o.attempt)),
		storage.Object{ContentType: "text/plain", Metadata: map[string]string{"encoding": logEncoding}},
		stream)
	// A store that stopped reading early leaves the compressor writing into the
	// pipe; closing it ends that.
	stream.Close()
	if source.err != nil {
		return p, errInternal("build: job %s: reading back the log it wrote: %v", o.job, source.err)
	}
	if err != nil {
		return p, nil
	}
	// A Put that succeeded with less than the whole log stored a prefix: the store
	// stopped before the compressed stream ended, whatever the compressor had read.
	if !stream.Ended() {
		return p, errInternal("build: job %s: the asset store took %d bytes of its compressed log and stopped before its end",
			o.job, stream.Taken())
	}
	if size := o.log.Size(); source.n != size {
		return p, errInternal("build: job %s: the asset store took %d of the %d bytes of its log",
			o.job, source.n, size)
	}
	o.s.emit(ctx, Accounted{
		Project:     o.project,
		Environment: o.environment,
		Job:         o.job,
		Attempt:     o.attempt,
		Op:          OpLog,
		Bytes:       uint64(source.n),
	})

	p.logCID = v.Record.RootCID
	return p, nil
}

// handover is the log on its way into the asset store: how much of it went, and
// the first read that failed. Put returns one error for the store's fault and the
// reader's, which cost different things, and a store that stops reading early
// raises no error at all, so the count catches a prefix.
type handover struct {
	from io.Reader
	n    int64
	err  error
}

func (h *handover) Read(p []byte) (int, error) {
	n, err := h.from.Read(p)
	h.n += int64(n)
	if err != nil && !errors.Is(err, io.EOF) && h.err == nil {
		h.err = err
	}
	return n, err
}

// discard closes the log, unregisters it and removes the file; run defers it past
// settle so the log stays followable until its record names it. Unregistration
// compares the pointer so an older run cannot unregister a newer one, and comes
// before the unlink under the fork lock. Readers that forked keep draining: the
// inode outlives the name.
func (o *runOutput) discard() {
	o.end()

	o.s.logsMu.Lock()
	if o.s.logs[o.job].log == o.log {
		delete(o.s.logs, o.job)
	}
	o.s.logsMu.Unlock()

	o.log.Unlink()
}

// end closes the log, which releases every reader waiting at its end. It is
// idempotent because publish and discard both need it and both happen.
func (o *runOutput) end() {
	o.mu.Lock()
	ended := o.ended
	o.ended = true
	o.mu.Unlock()
	if ended {
		return
	}
	o.log.Close()
}

// Follow returns a reader of one run's log on this node, at `from`, blocking at
// the end until more arrives or the build finishes. A late viewer forks at 0, a
// reconnecting one at its offset. No route here: authorization is the frontend's
// (write access to the job's repository), and routing to the node running the
// build belongs to the backend topology change. A job running elsewhere is
// NotFound here.
func (s *Service) Follow(jobID string, attempt uint32, from int64) (io.ReadCloser, error) {
	_, r, err := s.fork(jobID, attempt, from)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// Snapshot is Follow up to where the log is now, then EOF. The end is read after
// the fork, so it is never short of the checked offset.
func (s *Service) Snapshot(jobID string, attempt uint32, from int64) (io.ReadCloser, error) {
	log, r, err := s.fork(jobID, attempt, from)
	if err != nil {
		return nil, err
	}
	return soFar{Reader: io.LimitReader(r, log.Size()-from), Closer: r}, nil
}

// soFar is a fork that ends at a fixed byte of the log.
type soFar struct {
	io.Reader
	io.Closer
}

// runLog is a log this node holds and the run it belongs to.
type runLog struct {
	attempt uint32
	log     *livelog.Log
}

// fork is this node's log for that run of jobID and a reader at from; another
// run's log is never substituted, since an offset belongs to one run.
func (s *Service) fork(jobID string, attempt uint32, from int64) (*livelog.Log, *livelog.Reader, error) {
	if err := requireJobID(jobID); err != nil {
		return nil, nil, err
	}

	s.logsMu.Lock()
	defer s.logsMu.Unlock()

	kept, ok := s.logs[jobID]
	if !ok || kept.attempt != attempt {
		return nil, nil, errNotFound("no log of attempt %d of job %s is on this node", attempt, jobID)
	}

	r, err := kept.log.Fork(from)
	if err != nil {
		// An offset past the end of this run's log is the caller's arithmetic.
		if errors.Is(err, livelog.ErrOffset) {
			return nil, nil, errInvalidArgument("%v", err)
		}
		return nil, nil, errUnavailable("build: following job %s: %v", jobID, err)
	}
	return kept.log, r, nil
}
