package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/queue"
	"github.com/taubyte/tau/services/build/jobs/row"
	"github.com/taubyte/tau/services/build/jobs/section"
)

// section.go holds every write in this package, reachable only from inside the
// transition that decided it, about the job that transition read:
//
//	WHEN       made while the section the read was taken in is still held
//	WHO        made by the body that took that read, or what it handed its handle
//	ABOUT WHAT every row it names belongs to the job the read was taken on
//
// A transition is one closure:
//
//	err := s.withJob(id, func(j *held) error {
//	        ...decide on j.rec()...
//	        return j.finish(...)
//	})
//
// withJob takes the lock and the read, and hands the body a handle. Every write is
// a method on it, and every key is composed from h.in.Subject(), a field of
// jobs/section this package cannot assign. Rows are a row.Entry, made only by
// Set and Delete and reaching kvdb only by Apply, as one delta. Go has no lifetime
// on a parameter, so a handle that outlives withJob (captured, deferred, sent) is
// refused at runtime (section_test.go). What no type can say (who may reach an
// entry or the queue's paired writes, which key each row builds) is enumerated in
// population_test.go.
//
// A JOB IS NEVER REWRITTEN. The read is the fold of the job's events (events.go)
// and a transition appends one more, so a node that read a replica behind another's
// writes adds an event rather than overwriting one. The lock orders this node's own
// transitions (Cancel and Retry on rpc goroutines against the claim loop); the fold
// is what makes the outcome the same on every node.

// jobLock makes a job's read and the entry it decides one decision on this node.
// Package-level, since two Services may share one store.
//
// Lock order is jobLock, then pkg/queue's group lock; it is not reentrant. The
// queue write paired with an event (push, withdrawal, ack) is inside the section.
// Outside it: the build, the auth round trip, and the log handoff to the storage
// service.
var jobLock section.Lock[jobRecord]

// jobReader is which job a section is about and where its record comes from,
// chosen once by enter(). First takes the queue's barrier when withJob asked for
// one.
type jobReader struct {
	s       *server
	id      string
	barrier bool
}

// Subject is the job this section is about. Every key every write in this file
// composes is built from it.
func (r jobReader) Subject() string { return r.id }

func (r jobReader) First() (jobRecord, bool, error) {
	if r.barrier {
		if err := r.s.lead(); err != nil {
			return jobRecord{}, false, err
		}
	}
	return r.s.readRecord(r.id)
}

// held is one job under the lock: its record and every write the read can decide,
// the only route to the job's events, the queue's push, ack and withdrawal, and the
// running map. It exists only inside a body and is dead once withJob returns. It
// has no id field: the id is inside the holding, readable but not assignable here.
type held struct {
	s  *server
	in section.Holding[jobRecord]
}

// withJob is every transition's shape: lock, barrier, read, and a handle that is
// the only way to write. The barrier is here so no transition forgets it.
func (s *server) withJob(id string, body func(*held) error) error {
	return s.enter(id, true, body)
}

// withJobUnbarriered is mend's alone: a barrier per row of a reconcile pass would
// be one queue entry per open job, and the pass took one for all of them.
func (s *server) withJobUnbarriered(id string, body func(*held) error) error {
	return s.enter(id, false, body)
}

// enter is the one place a handle is made and a subject named.
func (s *server) enter(id string, barrier bool, body func(*held) error) error {
	return jobLock.Do(
		jobReader{s: s, id: id, barrier: barrier},
		func(in section.Holding[jobRecord]) error { return body(&held{s: s, in: in}) },
	)
}

// id reads the subject out of the holding, so no assignment here can re-point a
// handle.
func (h *held) id() string { return h.in.Subject() }

// rec is the record this section read. found is whether there was one.
func (h *held) rec() jobRecord { return h.in.Value() }
func (h *held) found() bool    { return h.in.Found() }

// under is the one gate, taking the write as its argument so no caller can ask
// and then write anyway. A refusal is CodeInternal (refused).
func (h *held) under(w func() error) error {
	if err := h.in.Write(w); err != nil {
		if errors.Is(err, section.ErrStale) {
			return stale(err)
		}
		return err
	}
	return nil
}

// refused spells a write that did not happen. A store refusal is Unavailable (ask
// another peer); a section that had already returned is this process's own fault.
// It wraps so errors.Is still reaches ErrStale.
func refused(err error, format string, args ...any) error {
	if errors.Is(err, section.ErrStale) {
		return errInternal(format+": %w", append(args, err)...)
	}
	return errUnavailable(format+": %w", append(args, err)...)
}

// stale turns a refusal into this package's wire code. CodeInternal, because
// there is no other node to ask.
func stale(err error) error { return errInternal("%w", err) }

// entry is one kvdb delta under construction: one event of this job and the rows
// that go with it. Every row is composed here from the handle's subject (the
// anchor and live rows also from the record the section read). Nothing takes a key
// or hands rows in or out, and the rows are a row.Entry.
type entry struct {
	h   *held
	ops row.Entry
	// at is the event's instant, which names its key and the rows it opens.
	at int64
	// written is how many events the entry carries so far.
	written int
	// err carries an encoding failure to commit(), so a chain reads as one
	// expression; nothing is written before commit.
	err error
}

// stated is an entry carrying this job's event. Rows that are claims about the job
// (open, project, retention, anchor, live) exist only on stated, so an entry without
// an event cannot write them. dropRetained and mend's drops stay on entry.
type stated struct {
	*entry
	rec jobRecord
	// at is this event's instant, which the rows beside it are named by.
	at int64
}

// writes opens an entry about this job. It is the only constructor there is.
func (h *held) writes() *entry {
	// Five is the widest fixed entry: event, open row, its drop, retention row, ref
	// anchor. Live rows grow it.
	return &entry{h: h, ops: row.Sized(5), at: h.s.journal.at()}
}

// No put(key) or drop(key): a method taking a key would let any handle write any
// key. Each row method composes its own key.

// event writes one of this job's events, under this writer. The entry's first
// event is at the entry's instant; a second one in the same entry takes the next,
// so each is a key of its own.
func (e *entry) event(ev *buildv2.JobEvent, rec jobRecord) stated {
	at := e.at
	if e.written > 0 {
		at = e.h.s.journal.at()
	}
	e.written++
	ev.At, ev.Writer = at, e.h.s.journal.writer
	raw, err := ev.MarshalVT()
	if err != nil {
		e.err = errInternal("encoding an event of job %s: %v", e.h.id(), err)
		return stated{entry: e, rec: rec, at: at}
	}
	e.ops.Set(eventKey(e.h.id(), at, e.h.s.journal.writer), raw)
	rec.ID = e.h.id()
	return stated{entry: e, rec: rec, at: at}
}

// created is a new job: the push it builds.
func (e *entry) created(rec jobRecord) stated {
	return e.event(&buildv2.JobEvent{Kind: &buildv2.JobEvent_Created{Created: &buildv2.JobCreated{
		RepositoryKey: rec.RepositoryKey, Ref: rec.Ref, CommitSha: rec.CommitSHA,
		ProjectId: rec.ProjectID, Environment: rec.Environment, Prelude: rec.Prelude,
		CreatedAt: rec.CreatedAt,
	}}}, rec)
}

// claimed is this node taking the job's next attempt.
func (e *entry) claimed(attempt uint32, deadline time.Time) stated {
	rec := e.h.rec()
	rec.Status = int32(buildv2.JobStatus_JobStatusClaimed)
	rec.Attempt, rec.Holder, rec.ClaimedUntil = attempt, e.h.s.host.ID().String(), deadline.UnixNano()
	return e.event(&buildv2.JobEvent{Kind: &buildv2.JobEvent_Claimed{Claimed: &buildv2.JobClaimed{
		Attempt: attempt, Holder: rec.Holder, ClaimedUntil: rec.ClaimedUntil,
	}}}, rec)
}

// settled is how one attempt ended, with what it produced. A job that is not
// Claimed has no attempt to end, so its next one is claimed in the same entry
// first: the queue spending its attempts, or a record that never reached a
// builder, still ends as a settle of an attempt.
func (e *entry) settled(status buildv2.JobStatus, reason string, p produced) stated {
	attempt := e.h.rec().Attempt
	if e.h.rec().status() != buildv2.JobStatus_JobStatusClaimed {
		attempt++
		e.claimed(attempt, time.Unix(0, e.at))
	}
	rec := e.h.rec().with(p)
	rec.Attempt = attempt
	rec.Status, rec.Reason = int32(status), reason
	rec.UpdatedAt = time.Unix(0, e.at).Unix()
	assets := make(map[string]string, len(p.assets))
	for ref, cid := range p.assets {
		assets[assetKey(ref.environment, ref.resource)] = cid
	}
	// A LOG IS FILED UNDER THE RUN THAT WROTE IT, which is not the attempt being
	// ended when the job moved on while the run went: it goes as an event of its own.
	log := p.logCID
	if p.attempt != attempt {
		log = ""
	}
	ended := e.event(&buildv2.JobEvent{Kind: &buildv2.JobEvent_Settled{Settled: &buildv2.JobSettled{
		Attempt: attempt, Status: status, Reason: reason, Log: log,
		Assets: assets, Children: p.children,
	}}}, rec)
	if log == "" && p.logCID != "" {
		e.loggedEvent(p)
	}
	return ended
}

// cancelled is a caller ending the job.
func (e *entry) cancelled(reason string) stated {
	rec := e.h.rec()
	rec.Status, rec.Reason = int32(buildv2.JobStatus_JobStatusCancelled), reason
	rec.UpdatedAt = time.Unix(0, e.at).Unix()
	return e.event(&buildv2.JobEvent{Kind: &buildv2.JobEvent_Cancelled{Cancelled: &buildv2.JobCancelled{Reason: reason}}}, rec)
}

// retried is a caller reopening the job.
func (e *entry) retried() stated {
	rec := e.h.rec()
	rec.Status, rec.Reason = int32(buildv2.JobStatus_JobStatusOpen), ""
	return e.event(&buildv2.JobEvent{Kind: &buildv2.JobEvent_Retried{Retried: &buildv2.JobRetried{}}}, rec)
}

// loggedEvent is an attempt's output, recorded after the attempt ended otherwise.
func (e *entry) loggedEvent(p produced) stated {
	return e.event(&buildv2.JobEvent{Kind: &buildv2.JobEvent_Logged{Logged: &buildv2.JobLogged{
		Attempt: p.attempt, Log: p.logCID,
	}}}, e.h.rec().with(p))
}

// open lists this job as outstanding for its repository, under this entry's
// instant, so a later drop cannot take a row a later reopening wrote.
func (s stated) open() stated {
	s.ops.Set(openRow(s.rec.RepositoryKey, s.h.id(), s.at, s.h.s.journal.writer), nil)
	return s
}

// dropOpen takes the job off its repository's outstanding list: the open rows of
// the events this section read, and none of an opening it did not see.
func (s stated) dropOpen() stated {
	s.entry.dropRead(s.h.rec().RepositoryKey)
	return s
}

// dropRead deletes the open rows written beside events this section read.
func (e *entry) dropRead(repositoryKey string) {
	prefix := openRowsOf(repositoryKey, e.h.id())
	keys, err := e.h.s.records.List(context.Background(), prefix)
	if err != nil {
		e.err = errUnavailable("listing the open rows of job %s: %v", e.h.id(), err)
		return
	}
	for _, key := range keys {
		name := strings.TrimPrefix(key, prefix)
		if _, read := e.h.rec().seen[name]; read {
			e.ops.Delete(openRowNamed(repositoryKey, e.h.id(), name))
		}
	}
}

// inProject is how a caller FINDS this job. It is written once, by create, in the
// same delta as the event it indexes.
func (s stated) inProject() stated {
	s.ops.Set(projectRow(s.rec.ProjectID, s.h.id()), nil)
	return s
}

// retained is the retention row, at the instant this entry says the job finished.
func (s stated) retained() stated {
	s.ops.Set(terminalRow(time.Unix(s.rec.UpdatedAt, 0), s.h.id()), nil)
	return s
}

// dropRetained removes the retention row a previous finish wrote, keyed off the
// record this section read rather than the one being written.
func (e *entry) dropRetained() *entry {
	e.ops.Delete(terminalRow(time.Unix(e.h.rec().UpdatedAt, 0), e.h.id()))
	return e
}

// artifacts is what this job's success built, as live rows beside its settle: what
// a deployment runs is the newest applied success's, so a later build is a newer
// row rather than a rewrite.
func (s stated) artifacts(p produced) stated {
	if !s.h.found() {
		s.err = errInternal("job %s: artifacts decided on a record this section did not find", s.h.id())
		return s
	}
	if s.h.rec().ProjectID == "" {
		return s
	}
	for ref, cid := range p.assets {
		value, err := liveValue(cid)
		if err != nil {
			s.err = errInternal("job %s: %v", s.h.id(), err)
			return s
		}
		s.ops.Set(liveRow(s.h.rec().ProjectID, ref.environment, ref.resource, s.at, s.h.id(), s.h.s.journal.writer), value)
	}
	return s
}

// anchor is the "before" of the next build of this ref there: a row per success,
// keyed by repository, ref and environment, the newest applied one of which is
// read. All parts come off the record this section read.
func (s stated) anchor() stated {
	if !s.h.found() {
		s.err = errInternal("job %s: a ref anchor decided on a record this section did not find", s.h.id())
		return s
	}
	rec := s.h.rec()
	s.ops.Set(anchorRow(rec.RepositoryKey, rec.Ref, rec.Environment, s.at, s.h.id(), s.h.s.journal.writer), []byte(rec.CommitSHA))
	return s
}

// dropClosedOpen removes an ended job's open rows under a repository, for mend:
// the rows of the events its read saw, so a reopening it did not see keeps its row.
func (e *entry) dropClosedOpen(repositoryKey string) *entry {
	e.dropRead(repositoryKey)
	return e
}

// dropStaleRetained removes a retention row its job no longer stands behind (gone,
// reopened, or ended again since), named by the second the listing says.
func (e *entry) dropStaleRetained(unix int64) *entry {
	e.ops.Delete(terminalRow(time.Unix(unix, 0), e.h.id()))
	return e
}

// erase removes this job: every event, and every row that names it.
func (e *entry) erase() *entry {
	rec := e.h.rec()
	ctx := context.Background()
	prefix := eventPrefix(e.h.id())
	events, err := e.h.s.records.List(ctx, prefix)
	if err != nil {
		e.err = errUnavailable("listing the events of job %s: %v", e.h.id(), err)
		return e
	}
	for _, key := range events {
		at, writer, err := parseEventName(strings.TrimPrefix(key, prefix))
		if err != nil {
			e.err = errInternal("job %s: %v", e.h.id(), err)
			return e
		}
		e.ops.Delete(eventKey(e.h.id(), at, writer))
	}
	e.ops.Delete(projectRow(rec.ProjectID, e.h.id()))
	e.ops.Delete(terminalRow(time.Unix(rec.UpdatedAt, 0), e.h.id()))
	e.dropRead(rec.RepositoryKey)

	for asset := range rec.Assets {
		environment, resource, ok := splitAssetKey(asset)
		if !ok {
			continue
		}
		rows := liveRows(rec.ProjectID, environment, resource)
		keys, err := e.h.s.records.List(ctx, rows)
		if err != nil {
			e.err = errUnavailable("listing what %s's %s ran: %v", environment, resource, err)
			return e
		}
		for _, key := range keys {
			if at, id, writer, err := parseBeside(rows, key); err == nil && id == e.h.id() {
				e.ops.Delete(liveRow(rec.ProjectID, environment, resource, at, e.h.id(), writer))
			}
		}
	}
	if rec.Environment != "" {
		rows := anchorRows(rec.RepositoryKey, rec.Ref, rec.Environment)
		keys, err := e.h.s.records.List(ctx, rows)
		if err != nil {
			e.err = errUnavailable("listing the anchors of job %s: %v", e.h.id(), err)
			return e
		}
		for _, key := range keys {
			if at, id, writer, err := parseBeside(rows, key); err == nil && id == e.h.id() {
				e.ops.Delete(anchorRow(rec.RepositoryKey, rec.Ref, rec.Environment, at, e.h.id(), writer))
			}
		}
	}
	return e
}

// dropOrphanedOpen removes the open rows of a job with no events, for mend: rows
// whose event is older than before. A younger row may be one whose delta has
// arrived ahead of the created event it follows, and is left for a later pass.
func (e *entry) dropOrphanedOpen(repositoryKey string, before time.Time) *entry {
	prefix := openRowsOf(repositoryKey, e.h.id())
	keys, err := e.h.s.records.List(context.Background(), prefix)
	if err != nil {
		e.err = errUnavailable("listing the open rows of job %s: %v", e.h.id(), err)
		return e
	}
	for _, key := range keys {
		if at, err := openRowAt(key); err != nil || at < before.UnixNano() {
			e.ops.Delete(openRowNamed(repositoryKey, e.h.id(), strings.TrimPrefix(key, prefix)))
		}
	}
	return e
}

// empty is an entry that decided no row, which commit would refuse.
func (e *entry) empty() bool { return e.err == nil && e.ops.Len() == 0 }

// commit applies the entry as one delta, inside the section.
func (e *entry) commit(what string) error {
	if e.err != nil {
		return e.err
	}
	if err := e.h.under(func() error {
		ctx, stop := context.WithTimeout(context.Background(), e.h.s.cfg.Queue.ApplyTimeout)
		defer stop()
		return e.ops.Apply(ctx, e.h.s.records)
	}); err != nil {
		return refused(err, "%s job %s", what, e.h.id())
	}
	return nil
}

// push puts this job's item in the queue.
func (h *held) push() error {
	if err := h.under(func() error { return h.s.work.Push(h.id(), nil) }); err != nil {
		return err
	}
	// A JOB TO HAND OUT wakes the loop, so a free runner gets it now.
	h.s.runners.wake.ring()
	return nil
}

// ack settles a delivery of this job, and only this job.
func (h *held) ack(q *queue.Queue, d queue.Delivery) error {
	return h.under(func() error {
		if d.ID != h.id() {
			return errInternal("job %s: acking delivery %s, which is another job's", h.id(), d.ID)
		}
		if err := q.Ack(d); err != nil {
			return fmt.Errorf("ack %s: %w", d.ID, err)
		}
		return nil
	})
}

// withdraw takes this job's item out of the queue, if it is there and unclaimed.
func (h *held) withdraw() error {
	return h.under(func() error {
		err := h.s.work.Remove(h.id())
		if err == nil || errors.Is(err, queue.ErrNotFound) || errors.Is(err, queue.ErrClaimed) {
			return nil
		}
		return errUnavailable("withdrawing job %s from the queue: %v", h.id(), err)
	})
}

// register records this node's cancel for the run it is starting.
func (h *held) register(stop context.CancelFunc) error {
	return h.under(func() error {
		h.s.runningMu.Lock()
		defer h.s.runningMu.Unlock()
		h.s.running[h.id()] = stop
		return nil
	})
}

// stopRun ends this node's run of this job, if it has one.
func (h *held) stopRun() error {
	return h.under(func() error {
		h.s.runningMu.Lock()
		end, ok := h.s.running[h.id()]
		h.s.runningMu.Unlock()
		if ok {
			end()
		}
		return nil
	})
}

// ends is the shared terminal entry: the event, off the outstanding list, on the
// retention one.
func (h *held) ends(status buildv2.JobStatus, reason string, p produced) stated {
	var s stated
	if status == buildv2.JobStatus_JobStatusCancelled {
		s = h.writes().cancelled(reason)
	} else {
		s = h.writes().settled(status, reason, p)
	}
	return s.dropOpen().retained()
}

func (h *held) finish(status buildv2.JobStatus, reason string) error {
	return h.ended(h.ends(status, reason, produced{}).commit("finishing"))
}

func (h *held) fail(reason string, p produced) error {
	return h.ended(h.ends(buildv2.JobStatus_JobStatusFailed, reason, p).commit("finishing"))
}

func (h *held) succeed(p produced) error {
	return h.ended(h.ends(buildv2.JobStatus_JobStatusSuccess, "", p).artifacts(p).anchor().commit("finishing"))
}

func (h *held) planned(children []string) error {
	return h.ended(h.ends(buildv2.JobStatus_JobStatusSuccess, "", produced{children: children}).commit("finishing"))
}

// ended rings this node's runs once an entry ending the job has committed, which is
// what a follow waiting for the job's next run is waiting for too.
func (h *held) ended(err error) error {
	if err == nil {
		h.s.runs.ring()
	}
	return err
}

// logged files a run's output on a job that ended some other way.
func (h *held) logged(p produced) error {
	if p.logCID == "" && len(p.assets) == 0 {
		return nil
	}
	return h.writes().loggedEvent(p).commit("recording the output of")
}

// requeue puts back the item of a job that is still waiting to run.
func (h *held) requeue() error {
	rec := h.rec()
	if rec.terminal() || rec.status() == buildv2.JobStatus_JobStatusClaimed {
		return nil
	}
	if err := h.push(); err != nil {
		return refused(err, "queue push")
	}
	return nil
}

// orphanReason is why a job whose record never reached its builder ended.
func orphanReason(tries int) string {
	return strings.TrimSpace(fmt.Sprintf("the job's record had not reached this builder after %d reads; retry it", tries))
}
