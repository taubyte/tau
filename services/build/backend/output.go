package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
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
// ref anchor on the happy path of every build. Deleting it deletes that class,
// not a component: NO REPLICATED ROW IS WRITTEN OUTSIDE A SECTION ANY MORE.
//
// WHICH IS NOT THE SAME SENTENCE AS "NOTHING IS", and this file is where the
// difference lives. There are two stores: the raft one, and the asset store —
// durable on another node before it returns (config.go), the only door in this
// package that leaves this machine. BOTH OF ITS DOORS ARE OUTSIDE EVERY
// SECTION, and neither may be otherwise: a section is jobLock, one lock over
// the whole job table, so a Put held inside one puts every job on this node —
// every claim, every webhook's Enqueue, every Cancel — behind a round trip to
// another machine.
//
//	Asset()    opened WHILE THE BUILD RUNS, from the runner's goroutine. Inside
//	           a section it would be the length of a compile.
//	publish()  opened BETWEEN settle's two sections. Inside one it was up to
//	           PublishTimeout per settled build, which is where it sat for a
//	           round and what runner.go's settle now says out loud.
//
// So the writer outside every section did not go away, it changed what it may
// write — CONTENT, whose cid goes into a map in the run's own memory and
// reaches a record only from inside the section that settles the job — and it
// is enumerated rather than asserted: population_test.go's
// TestEveryWriteToTheAssetStoreIsNamedAndPlaced says who may name the store,
// who may call Put, and where each of them is. It was written because a second
// door on the Service here, storing an object per viewer from Follow, was GREEN
// across every package under services/build.
//
// SO A LOG HAS THREE LIVES AND ONLY THE LAST ONE LEAVES THIS NODE:
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
// assetRef is which artifact a cid is: one resource, in one deployment.
//
// IT IS A PAIR AND NOT A COMPOSED STRING, and that is what lets the section
// that files it compose a key from two fields of this run's own output rather
// than by parsing one back apart. A key built out of a parsed string is a key
// whose parts nothing can be said about — and everything this package's
// censuses ask is about where a key's parts came from.
type assetRef struct {
	environment string
	resource    string
}

type produced struct {
	attempt uint32
	logCID  string
	assets  map[assetRef]string
}

// runOutput is one run's half of Output: the log file it writes and the assets
// it hands over.
//
// IT REACHES NO REPLICATED STORE. There is nothing here that can name a key,
// compose a row, or apply an entry, which is why this type is in none of the
// censuses over the ROW writers. It does reach the asset store, from both of
// its methods and from outside every section in one of them, and that is the
// census it IS in.
type runOutput struct {
	s       *Service
	job     string
	attempt uint32
	log     *livelog.Log

	// mu guards built, because Asset may be called from whatever goroutine the
	// runner builds a resource on while another writes the log.
	mu    sync.Mutex
	built map[assetRef]string
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
//
// AND THAT NAME IS PUBLIC, WHICH IS WHY THE DIRECTORY IS OWNED. Both halves are
// derived: seg(jobID) is a hash of the repository, the ref and the commit —
// deterministic on purpose and documented as guessable (jobid.go) — and the
// attempt starts at one. So anybody who can name the repository can compute the
// path of the file this node is about to open with O_CREATE and truncate, and
// the default directory was under the OS temp dir, where anybody can create a
// name. os.MkdirAll was what stood there, and it is not a check: it returns nil
// for a directory that already exists whoever made it and whatever its mode.
// livelog.OwnDir is, and Create refuses the file itself as well — the two
// halves and what each costs are argued where they are.
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
	s.logs[w.JobID] = log
	s.logsMu.Unlock()

	return &runOutput{
		s: s, job: w.JobID, attempt: w.Attempt, log: log,
		built: map[assetRef]string{},
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
//
// SO THIS IS THE WRITER OUTSIDE EVERY SECTION, said plainly rather than left to
// be found: it is called by the RUNNER, on a stranger's build, from a goroutine
// holding no lock, and what it leaves behind is bytes and a cid in o.built that
// only publish reads. Nothing in this package calls it, nothing else writes
// that map, and nothing else reads it — all three are census lines, because
// each of them was a way for a cid to be spent where no section could see it.
func (o *runOutput) Asset(ctx context.Context, environment, resourceID string, content io.Reader) error {
	if environment == "" {
		return errors.New("build: an asset needs the deployment it was built for")
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

	cid, err := o.s.assetStore.Put(ctx, content)
	if err != nil {
		return fmt.Errorf("build: job %s: storing %s: %w", o.job, resourceID, err)
	}
	if cid == "" {
		return errInternal("build: job %s: the asset store returned no cid for %s", o.job, resourceID)
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	o.built[assetRef{environment: environment, resource: resourceID}] = cid
	return nil
}

// publish ends the log and hands it to the asset store.
//
// IT IS CALLED BETWEEN THE TWO SECTIONS THAT SETTLE THE JOB: past the ack, and
// before the entry that records what it returns. Both halves of that are the
// placement, and neither is free.
//
// PAST THE ACK, because ack is this service's only compare-and-swap. Evaluated
// before it — which is where this was, as an argument to settle() — a node
// whose claim had expired or which had lost leadership shipped the whole log to
// the store and then wrote no record naming it, which is the orphan run()
// refuses to make on the shutdown path for exactly the same reason. It is also
// made only when the first section found a record this run's output still
// belongs to, so the two branches that record nothing ship nothing.
//
// AND OUTSIDE BOTH OF THEM, because a section is jobLock and jobLock is one
// lock over every job on this node. Inside settle's body — which is where the
// fix for the paragraph above put it, for a round — one slow storage node
// stalled webhook ingestion and every user-facing cancel and retry for up to
// PublishTimeout per settled build. runner.go's settle argues the order; this
// is the half of it this function is responsible for knowing.
//
// SO ONE DIRECTION HOLDS AND THE OTHER DOES NOT, and it was written down for a
// round as a biconditional that was false:
//
//	A CID REACHES A JOB       only for a log that reached the store, whole. That
//	                          is this function's contract and the three
//	                          refusals below are what make it true.
//	A LOG REACHES THE STORE   and a cid for it may still reach NO job. Between
//	                          the store taking the bytes and the entry naming
//	                          them being applied, this node can stop leading —
//	                          and then the object is in the store, the record
//	                          reads Claimed with no queue item, and reconcile
//	                          rebuilds the job into a SECOND log. Nothing here
//	                          unreferences the first. The window is as wide as
//	                          the rest of the publish plus one entry, bounded by
//	                          PublishTimeout; doc.go names it under what is not
//	                          covered at all, and output_test.go measures it.
//
// Closing it needs the two stores to commit together, which is a transaction
// across a raft group and a storage service; the shape that comes closest —
// publishing inside the section that writes the record — is the one section.go
// refuses, and for a bigger reason.
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
// and it is bounded well inside ClaimMargin (config.go), which is what the
// whole of this settle is spent out of.
//
// THE TWO WAYS IT CAN FAIL ARE NOT THE SAME FAILURE, and telling them apart is
// the whole of the error it returns:
//
//	THE STORE COULD NOT TAKE IT   the job keeps its outcome and carries no log
//	                              cid. A compile that succeeded did not stop
//	                              succeeding because the asset store was
//	                              unreachable for ten seconds, and what a reader
//	                              sees is a job with no log, which is the truth.
//	THE LOG COULD NOT BE READ     this node cannot say what the build printed,
//	                              and that is REPORTED rather than swallowed. A
//	                              run this node could not open a log for is
//	                              already a failed build (invoke); a run whose
//	                              log this node cannot read back is the same
//	                              fact at the other end, and settling it Success
//	                              with an empty map is a job that says a build
//	                              printed nothing.
//
// Which of the two happened is not in the error Put returns — it is one error
// for both — so the reader is wrapped and asked afterwards. A store that fails
// AND consumed a broken reader is the log's failure, because the object it
// might have written is not the log either way.
//
// AND THERE IS A THIRD WAY TO GET A CID THAT IS NOT THE LOG, with no error in
// it at all: a store that STOPS READING and answers anyway. Nobody is asked for
// the rest, so nothing has anything to report, and the record ends up naming an
// object that is a PREFIX of the build's output — which reads as a complete log
// to everything downstream. So the bytes are COUNTED on the way out and
// compared with what the log says it holds. It is the same sentence as the two
// above: what may go on the record is this log, whole, or nothing.
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

	if o.s.assetStore == nil {
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

	// `stop`, not `cancel`: this package declares Service.cancel, and the
	// censuses in population_test.go read the NAME at a call site — a local
	// spelled cancel() makes this body read as one that opens a section, which
	// inside another section is a deadlock rather than a defect somebody sees.
	// The check fails closed, which is what it is for; the rename is the answer
	// to it.
	ctx, stop := context.WithTimeout(context.WithoutCancel(ctx), o.s.cfg.PublishTimeout)
	defer stop()

	source := &handover{from: whole}
	cid, err := o.s.assetStore.Put(ctx, source)
	if source.err != nil {
		return p, errInternal("build: job %s: reading back the log it wrote: %v", o.job, source.err)
	}
	if err != nil {
		return p, nil
	}
	// THE STORE'S FAILURE IS ANSWERED FIRST, above, because a Put that gave up
	// halfway is the store's fault and costs the log rather than the build. A
	// Put that SUCCEEDED holding less than the whole log is the other thing.
	if size := o.log.Size(); source.n != size {
		return p, errInternal("build: job %s: the asset store took %d of the %d bytes of its log",
			o.job, source.n, size)
	}
	p.logCID = cid
	return p, nil
}

// handover is the log on its way into the asset store: HOW MUCH OF IT WENT, and
// the first read that failed.
//
// IT EXISTS BECAUSE Put RETURNS ONE ERROR FOR TWO FAULTS. The store reads what
// it is handed, so a store that reports "could not store this" is reporting
// either its own failure or the reader's, and the job's outcome is different
// for each: an unreachable store costs the log, an unreadable log costs the
// build. Asking the reader afterwards is what separates them, and it is asked
// even when Put SUCCEEDED — a store that swallowed a read error and answered
// with a cid stored something that is not this log, and naming it on the record
// would be worse than having no log at all.
//
// AND THE COUNT IS FOR THE FAULT THAT RAISES NO ERROR ANYWHERE: a store that
// simply stops reading. There is nothing to report, because nothing was asked;
// what comes back is a cid over a prefix. Counting is the only way to tell that
// from a log, and it costs an addition per read.
//
// It assumes Put consumes the reader before it returns, which is what taking an
// io.Reader means: a store that read it on another goroutine would be reading
// bytes whose lifetime it was not given — and the count then catches that too,
// because what was consumed by the time Put answered is what it can honestly
// have stored.
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
