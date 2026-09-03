package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/taubyte/tau/services/build/livelog"
)

// output.go is what a run WRITES, and it is the whole of what replaced the
// sink.
//
// THE SINK WROTE REPLICATED RAFT ROWS FOR LOG LINES. One entry per line, keyed
// by (job, attempt, timestamp), agreed by a consensus group — for content that
// nothing has to agree about and that no read of a job record decides. That is
// what made it THE ONE WRITER IN THIS PACKAGE OUTSIDE EVERY SECTION, by design,
// and three of the last four escapes this package found went through it: a
// second row spliced beside its own, a one-line row-verb wrapper applied from
// it, and a job id flowing into its subject. Each moved another repository's
// ref anchor on the happy path of every build. Deleting it deletes the class,
// not a component: there is no writer outside a section left to guard.
//
// SO A LOG HAS THREE LIVES AND ONLY THE LAST ONE TOUCHES THE STORE:
//
//	while the build runs   the writer appends to a FILE (services/build/livelog).
//	                       No consensus, no RPC per line, no entry per row.
//	while somebody watches a reader FORKS off that file at any offset and
//	                       follows it. Follow() below is the seam; the route is
//	                       not here, and says why.
//	when the build ends    the file is handed to the ASSET STORE and the cid
//	                       goes on the job record, in the same entry that
//	                       settles it — which is Job.logs in build.proto, and
//	                       is what raft holds about a log.
//
// AND THE BYTES TRAVEL, NOT THE LOG. That is v1's pushStash kept verbatim: what
// this node made goes from this node to storage, and the record carries only
// the content id. Nothing about a log is replicated by the consensus group that
// runs the queue.

// produced is what one run left behind: the cid of its log, and one cid per
// resource it built.
//
// IT IS A VALUE THE RUN HANDS BACK rather than a set of rows it wrote as it
// went, and that is the sentence the deletion above reduces to. The store hears
// about it once, from settle, inside the section that read the record.
//
// IT CARRIES ITS OWN ATTEMPT, and that is not the same number as the one on the
// record settle re-reads. A log belongs to the run that wrote it; the record is
// read again after the build, outside the lock the run did not hold, and what
// it says by then is somebody else's business. Filing a log under the attempt
// the RECORD happens to carry would put this run's output on another run's
// line the one time the two differ.
type produced struct {
	attempt uint32
	logCID  string
	assets  map[string]string
}

// runOutput is one run's half of Output: the log file it writes and the assets
// it hands over.
//
// It reaches no store. There is nothing here that can name a key, compose a
// row, or apply an entry — which is why this type is not in any of the censuses
// population_test.go keeps over the writers.
type runOutput struct {
	s       *Service
	job     string
	attempt uint32
	log     *livelog.Log

	// mu guards built, because Asset may be called from whatever goroutine the
	// runner builds a resource on while another writes the log.
	mu    sync.Mutex
	built map[string]string
	// ended is this type's own idempotence: discard is deferred by run and
	// publish closes on its way in, so the two meet on every path.
	ended bool
}

// output opens a run's log and registers it so it can be followed.
//
// THE FILE IS NAMED FOR THE JOB AND THE ATTEMPT, through the same encoding the
// key grammar uses (store.go's seg) and for the same reason: the id is 43
// base64url characters when this package minted it, and this is one of the
// places that is true of every id it will ever be handed rather than only of
// the ones it made. A path segment that cannot contain "/" and cannot be "." or
// ".." is a filename that stays inside the log directory whatever arrives.
//
// The attempt is in the name because two runs of one job must not share a file:
// a retry writes its own log, and the previous one's cid stays on the record.
func (s *Service) output(w Work) (*runOutput, error) {
	if err := os.MkdirAll(s.cfg.LogDir, 0o700); err != nil {
		return nil, errUnavailable("build: log directory %s: %v", s.cfg.LogDir, err)
	}

	name := seg(w.JobID) + "." + stamp(int64(w.Attempt)) + ".log"
	log, err := livelog.Create(filepath.Join(s.cfg.LogDir, name), livelog.WithMaxBytes(s.cfg.MaxLogBytes))
	if err != nil {
		return nil, errUnavailable("build: job %s: %v", w.JobID, err)
	}

	s.logsMu.Lock()
	s.logs[w.JobID] = log
	s.logsMu.Unlock()

	return &runOutput{
		s: s, job: w.JobID, attempt: w.Attempt, log: log,
		built: map[string]string{},
	}, nil
}

// Write is the log. A run writes to Output directly — it IS an io.Writer — so
// piping a build's stdout into it is an io.Copy.
//
// A FULL LOG IS NOT A FAILED WRITE, from the runner's side. The cap is this
// service's policy on a stranger's build script, not something the build did
// wrong, and a runner that treated it as an I/O error would abort a build for
// being verbose. The file says where it stopped keeping bytes (livelog writes
// the notice), and the run carries on.
func (o *runOutput) Write(p []byte) (int, error) {
	n, err := o.log.Write(p)
	if errors.Is(err, livelog.ErrFull) {
		return len(p), nil
	}
	return n, err
}

// Asset hands one built artifact to the asset store and remembers its cid.
//
// THE BYTES GO NOW AND THE CID GOES AT THE END. Storing is content-addressed
// and idempotent, so doing it during the run costs nothing if the run then
// fails — the same bytes store to the same id — while the RECORD is written
// once, by the section that settles the job. That is the split the sink did not
// have: publishing content as it is produced is safe, and publishing a POINTER
// from outside a section is what was not.
func (o *runOutput) Asset(ctx context.Context, resourceID string, content io.Reader) error {
	if resourceID == "" {
		return errors.New("build: an asset needs a resource id")
	}
	if content == nil {
		return errors.New("build: an asset needs content")
	}
	if o.s.assets == nil {
		return errUnavailable("build: this node has no asset store to put %s in", resourceID)
	}

	cid, err := o.s.assets.Put(ctx, content)
	if err != nil {
		return fmt.Errorf("build: job %s: storing %s: %w", o.job, resourceID, err)
	}
	if cid == "" {
		return errInternal("build: job %s: the asset store returned no cid for %s", o.job, resourceID)
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	o.built[resourceID] = cid
	return nil
}

// publish ends the log and hands it to the asset store.
//
// IT IS THE ONE PLACE THE WHOLE LOG IS READ, and it reads it by forking a
// reader like any other watcher — so the bytes stream from the file to the
// store, this process never holds the build's output in memory, and the read is
// the same mechanism a viewer uses rather than a second one beside it.
//
// ITS CONTEXT IS NOT THE RUN'S. The run's context is dead on every path that
// matters here — a cancel, the claim deadline, a build that ran out of time —
// and those are exactly the runs whose logs somebody wants. So the deadline is
// the operator's PublishTimeout off a context the cancellation cannot reach,
// and it is bounded well inside ClaimMargin (config.go) because what follows it
// is the ack, and the ack is the commit point.
//
// A LOG THAT COULD NOT BE STORED LEAVES THE JOB WITH NO LOG CID, and does not
// fail the build: a compile that succeeded did not stop succeeding because the
// asset store was unreachable for ten seconds. What a reader sees is a job with
// no log, which is the truth.
func (o *runOutput) publish(ctx context.Context) produced {
	o.end()

	o.mu.Lock()
	p := produced{attempt: o.attempt, assets: merged(o.built, nil)}
	o.mu.Unlock()

	if o.s.assets == nil {
		return p
	}

	// The fork is taken AFTER end() closed the log, so the read stops at the
	// last byte instead of blocking for a writer that is gone.
	whole, err := o.log.Fork(0)
	if err != nil {
		return p
	}
	defer whole.Close()

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), o.s.cfg.PublishTimeout)
	defer cancel()

	cid, err := o.s.assets.Put(ctx, whole)
	if err != nil {
		return p
	}
	p.logCID = cid
	return p
}

// discard is the cleanup run defers: the log is closed whatever happened, and
// the file goes.
//
// THE FILE IS SCRATCH. What survives a run is the cid, and a file kept after a
// publish is a file nothing will ever delete — v1's own defect, one directory
// over: nothing there deletes an archived job or its assets either. A reader
// that forked before this keeps draining what it had; the inode outlives the
// name (livelog).
func (o *runOutput) discard() {
	o.end()
	o.log.Unlink()
}

// end closes the log and takes it off the followable set. It is idempotent
// because publish and discard both need it and both happen.
//
// THE UNREGISTRATION COMPARES THE POINTER rather than deleting the key: this
// map is keyed by job, and a second run of the same job on this node — which
// the queue's exclusion should make impossible, and which a deleted key would
// hide rather than survive — must not have its log unfollowable because an
// older run of it finished.
func (o *runOutput) end() {
	o.mu.Lock()
	ended := o.ended
	o.ended = true
	o.mu.Unlock()
	if ended {
		return
	}

	o.log.Close()

	o.s.logsMu.Lock()
	if o.s.logs[o.job] == o.log {
		delete(o.s.logs, o.job)
	}
	o.s.logsMu.Unlock()
}

// Follow hands back a reader of the log of a build THIS NODE IS RUNNING,
// positioned at `from`, which blocks at the end until there is more or the
// build finishes. Closing it is how a caller lets go.
//
// THE OFFSET IS THE WHOLE MECHANISM, and it is what v1 has no version of: a
// viewer joining late forks at 0 and catches up, a viewer whose connection
// dropped forks at the offset it had, and a proxy is a fork and an io.Copy. v1
// can only hand you a log once the build is over and the whole of it has
// reached IPFS.
//
// THERE IS NO ROUTE ON IT, AND THAT IS DELIBERATE — this is the seam, not half
// a feature. Two reasons, and the second is the one that decides it:
//
//   - AUTHORIZATION IS THE FRONTEND'S. A log read authorizes on the CALLER'S
//     WRITE ACCESS TO THE JOB'S REPOSITORY, which is the rule
//     frontend/authz.go already applies to Get, Cancel and Retry, through the
//     admitted() gate that spends a rate limit before it costs this cloud a
//     leader read. v1 serves logs at GET /logs/{cid}, gated only by a GitHub
//     token being valid — so a cid is a bearer token, handed out in API
//     responses, and any GitHub account can read any log of any project given
//     one. A cid is an ADDRESS, not a credential. When the route lands it is
//     GET /job/{jid}/logs, authorized exactly as Get is.
//   - ROUTING TO IT IS THE TOPOLOGY CHANGE'S. The file is on the node running
//     the build, and only that node can answer. Which node that is, and how a
//     request reaches it, is decided by the move that makes the backend a
//     stateless executor leasing work over RPC — so building a route here
//     would be building it against a topology that is going away, and the
//     answer to "which node" would be a leader read of a claim record this
//     package deliberately does not forward on (config.go: no Leader, no
//     Members, no peer selection).
//
// It is on the Service rather than inside the run because the run owns the
// writer and a watcher owns nothing: the map is what turns a job id into the
// log being written for it, and it is this node's own — a job running elsewhere
// is a NotFound here, not a forward.
func (s *Service) Follow(jobID string, from int64) (io.ReadCloser, error) {
	if err := requireJobID(jobID); err != nil {
		return nil, err
	}

	s.logsMu.Lock()
	log, running := s.logs[jobID]
	s.logsMu.Unlock()

	if !running {
		return nil, errNotFound("no build of job %s is running on this node", jobID)
	}

	r, err := log.Fork(from)
	if err != nil {
		// An offset past the end is the caller's: it is resuming against a log
		// that never reached it, which is almost always a different attempt's.
		if errors.Is(err, livelog.ErrOffset) {
			return nil, errInvalidArgument("%v", err)
		}
		return nil, errUnavailable("build: following job %s: %v", jobID, err)
	}
	return r, nil
}
