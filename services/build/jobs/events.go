package jobs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	ds "github.com/ipfs/go-datastore"

	"github.com/taubyte/tau/core/kvdb"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
)

// events.go is where a job lives: one key per transition, in kvdb, never rewritten.
//
//	/build/job/<seg(id)>/event/<%020 at>/<seg(writer)>   one transition
//	/build/project/<seg(pid)>/<seg(id)>                  how a caller finds a job
//	/build/open/<seg(repokey)>/<seg(id)>/<%020 at>/<seg(writer)>
//	                                                     outstanding since that event
//	/build/anchor/<seg(repokey)>/<seg(ref)>/<seg(env)>/<%020 at>/<seg(id)>/<seg(writer)>
//	                                                     -> the sha one success built
//	/build/live/<seg(pid)>/<seg(env)>/<seg(res)>/<%020 at>/<seg(id)>/<seg(writer)>
//	                                                     -> what one success built
//	/build/terminal/<%020 unix>/<seg(id)>                retention
//
// TWO NODES NEVER WRITE ONE KEY WITH DIFFERENT CONTENT (AGENTS.md rule 1): a key
// with a value names the event it was written beside, and an event its writer and
// its instant, so a concurrent transition is a second key, not a lost one. A job is
// the fold of its events in key order, which is (at, writer), so every node holding
// the same events agrees on the job (rule 3). A row beside an event the fold did
// not apply (a success a cancel ended first) is visible and does not count: the
// anchor and live readers take the newest row whose event was applied. The keys
// start with "/" because that is how kvdb lists them back.

const (
	eventSegment  = "event"
	anchorSegment = "anchor"
	liveSegment   = "live"
)

func eventPrefix(id string) string {
	return "/" + rootSegment + "/" + jobSegment + "/" + seg(id) + "/" + eventSegment + "/"
}

func eventKey(id string, at int64, writer string) string {
	return eventPrefix(id) + eventName(at, writer)
}

// eventName is what names one event among a job's: its instant and its writer.
func eventName(at int64, writer string) string { return stamp(at) + "/" + seg(writer) }

// parseEventName reads an eventName back.
func parseEventName(name string) (at int64, writer string, err error) {
	parts := strings.Split(name, "/")
	if len(parts) != 2 {
		return 0, "", fmt.Errorf("%q is not an event name", name)
	}
	if _, err = fmt.Sscanf(parts[0], "%d", &at); err != nil {
		return 0, "", fmt.Errorf("%q: %w", name, err)
	}
	if writer, err = unseg(parts[1]); err != nil {
		return 0, "", err
	}
	return at, writer, nil
}

func projectRow(projectID, id string) string {
	return "/" + rootSegment + "/" + projectSegment + "/" + seg(projectID) + "/" + seg(id)
}

func projectRows(projectID string) string {
	return "/" + rootSegment + "/" + projectSegment + "/" + seg(projectID) + "/"
}

// openRoot is every open row of every repository, what reconcile walks.
func openRoot() string { return "/" + rootSegment + "/" + openSegment + "/" }

// openRows is a repository's outstanding jobs.
func openRows(repositoryKey string) string { return openRoot() + seg(repositoryKey) + "/" }

// openRowsOf is one job's open rows: one per opening, each named by the event that
// opened it, so a drop takes only the rows of events the dropping section read and
// never a reopening it has not seen (AGENTS.md rule 5).
func openRowsOf(repositoryKey, id string) string { return openRows(repositoryKey) + seg(id) + "/" }

func openRow(repositoryKey, id string, at int64, writer string) string {
	return openRowNamed(repositoryKey, id, eventName(at, writer))
}

// openRowNamed is the open row beside the event called name.
func openRowNamed(repositoryKey, id, name string) string {
	return openRowsOf(repositoryKey, id) + name
}

// openRowAt is the instant of the event an open row was written beside.
func openRowAt(key string) (int64, error) {
	parts := strings.Split(key, "/")
	if len(parts) < 2 {
		return 0, fmt.Errorf("%q is not an open row", key)
	}
	var at int64
	_, err := fmt.Sscanf(parts[len(parts)-2], "%d", &at)
	return at, err
}

// parseOpenRow reads the repository and the job an open row names.
func parseOpenRow(key string) (repositoryKey, id string, err error) {
	rest, ok := strings.CutPrefix(key, openRoot())
	if !ok {
		return "", "", fmt.Errorf("%q is not an open row", key)
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 4 {
		return "", "", fmt.Errorf("%q is not an open row", key)
	}
	if repositoryKey, err = unseg(parts[0]); err != nil {
		return "", "", err
	}
	if id, err = unseg(parts[1]); err != nil {
		return "", "", err
	}
	return repositoryKey, id, nil
}

func anchorRows(repositoryKey, ref, environment string) string {
	return "/" + rootSegment + "/" + anchorSegment + "/" + seg(repositoryKey) + "/" + seg(ref) + "/" + seg(environment) + "/"
}

func anchorRow(repositoryKey, ref, environment string, at int64, id, writer string) string {
	return anchorRows(repositoryKey, ref, environment) + besideName(at, id, writer)
}

func liveRows(projectID, environment, resourceID string) string {
	return "/" + rootSegment + "/" + liveSegment + "/" + seg(projectID) + "/" + seg(environment) + "/" + seg(resourceID) + "/"
}

func liveRow(projectID, environment, resourceID string, at int64, id, writer string) string {
	return liveRows(projectID, environment, resourceID) + besideName(at, id, writer)
}

// besideName is how an anchor or a live row names the event it was written beside:
// its instant first, so byte order is newest last.
func besideName(at int64, id, writer string) string {
	return stamp(at) + "/" + seg(id) + "/" + seg(writer)
}

// parseBeside reads the event a row under prefix was written beside.
func parseBeside(prefix, key string) (at int64, id, writer string, err error) {
	rest, ok := strings.CutPrefix(key, prefix)
	parts := strings.Split(rest, "/")
	if !ok || len(parts) != 3 {
		return 0, "", "", fmt.Errorf("%q is not a row under %q", key, prefix)
	}
	if _, err = fmt.Sscanf(parts[0], "%d", &at); err != nil {
		return 0, "", "", fmt.Errorf("%q: %w", key, err)
	}
	if id, err = unseg(parts[1]); err != nil {
		return 0, "", "", err
	}
	if writer, err = unseg(parts[2]); err != nil {
		return 0, "", "", err
	}
	return at, id, writer, nil
}

func terminalRow(at time.Time, id string) string {
	return terminalRows() + stamp(at.Unix()) + "/" + seg(id)
}

func terminalRows() string { return "/" + rootSegment + "/" + terminalSegment + "/" }

// parseTerminalRow reads the second a job finished and the job off its retention
// row.
func parseTerminalRow(key string) (unix int64, id string, err error) {
	rest, ok := strings.CutPrefix(key, terminalRows())
	parts := strings.Split(rest, "/")
	if !ok || len(parts) != 2 {
		return 0, "", fmt.Errorf("%q is not a retention row", key)
	}
	if _, err = fmt.Sscanf(parts[0], "%d", &unix); err != nil {
		return 0, "", fmt.Errorf("%q: %w", key, err)
	}
	if id, err = unseg(parts[1]); err != nil {
		return 0, "", err
	}
	return unix, id, nil
}

// lastSegmentOf is the id a row names in its final segment.
func lastSegmentOf(key string) (string, error) {
	return unseg(key[strings.LastIndex(key, "/")+1:])
}

// journal appends a job's events and folds them back.
type journal struct {
	db     kvdb.Instance
	writer string
	now    func() time.Time

	// mu keeps this writer's instants strictly increasing, so two of its own
	// events never share a key.
	mu   sync.Mutex
	last int64
}

func newJournal(db kvdb.Instance, writer string, now func() time.Time) *journal {
	return &journal{db: db, writer: writer, now: now}
}

// at is this writer's next instant.
func (j *journal) at() int64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	n := j.now().UnixNano()
	if n <= j.last {
		n = j.last + 1
	}
	j.last = n
	return n
}

// read is a job as this node's replica of its events says it is.
func (j *journal) read(ctx context.Context, id string) (jobRecord, bool, error) {
	evs, err := j.events(ctx, id)
	if err != nil {
		return jobRecord{}, false, err
	}
	rec, found := fold(id, evs)
	return rec, found, nil
}

// events is a job's events in key order.
func (j *journal) events(ctx context.Context, id string) ([]*buildv2.JobEvent, error) {
	keys, err := j.db.List(ctx, eventPrefix(id))
	if err != nil {
		return nil, errUnavailable("listing the events of job %s: %v", id, err)
	}
	slices.Sort(keys)
	out := make([]*buildv2.JobEvent, 0, len(keys))
	for _, key := range keys {
		raw, err := j.db.Get(ctx, key)
		if errors.Is(err, ds.ErrNotFound) {
			// Listed, then gone: retention took it between the two reads.
			continue
		}
		if err != nil {
			return nil, errUnavailable("reading an event of job %s: %v", id, err)
		}
		var ev buildv2.JobEvent
		if err := ev.UnmarshalVT(raw); err != nil {
			return nil, errInternal("event %s of job %s does not decode: %v", key, id, err)
		}
		out = append(out, &ev)
	}
	return out, nil
}

// live is the artifact the newest success built for one resource, and the job it
// came from. ok is false when nothing has.
func (j *journal) live(ctx context.Context, projectID, environment, resourceID string) (cid, id string, ok bool, err error) {
	raw, id, ok, err := j.newest(ctx, liveRows(projectID, environment, resourceID))
	if err != nil || !ok {
		return "", "", false, err
	}
	var l buildv2.JobLive
	if err := l.UnmarshalVT(raw); err != nil {
		return "", "", false, errInternal("what %s/%s's %s runs does not decode: %v", projectID, environment, resourceID, err)
	}
	return l.Cid, id, true, nil
}

// anchor is the commit the newest success of a ref built in an environment.
func (j *journal) anchor(ctx context.Context, repositoryKey, ref, environment string) (string, bool, error) {
	raw, _, ok, err := j.newest(ctx, anchorRows(repositoryKey, ref, environment))
	return string(raw), ok, err
}

// newest is the value of the newest row under prefix whose event its job's fold
// applied, and that job.
func (j *journal) newest(ctx context.Context, prefix string) (value []byte, id string, ok bool, err error) {
	keys, err := j.db.List(ctx, prefix)
	if err != nil {
		return nil, "", false, errUnavailable("listing %s: %v", prefix, err)
	}
	slices.Sort(keys)
	folded := map[string]jobRecord{}
	for i := len(keys) - 1; i >= 0; i-- {
		at, id, writer, err := parseBeside(prefix, keys[i])
		if err != nil {
			return nil, "", false, errInternal("%v", err)
		}
		rec, read := folded[id]
		if !read {
			if rec, _, err = j.read(ctx, id); err != nil {
				return nil, "", false, err
			}
			folded[id] = rec
		}
		if !rec.seen[eventName(at, writer)] {
			continue
		}
		raw, err := j.db.Get(ctx, keys[i])
		if errors.Is(err, ds.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, "", false, errUnavailable("reading %s: %v", keys[i], err)
		}
		return raw, id, true, nil
	}
	return nil, "", false, nil
}

// fold is a job as its events, in order, make it:
//
//   - the first created sets the push, and the job is Open unless it has already
//     ended (a created that arrives after another node's settle of it);
//   - claimed(a) takes an Open job, or a Claimed one at a lower attempt, to attempt a;
//   - settled(a) ends attempt a while it is Claimed: the first terminal event of an
//     attempt wins;
//   - cancelled ends any job that has not ended;
//   - retried reopens one that has;
//   - logged(a) files attempt a's log whatever the job is doing.
//
// A settle with no created before it is a job whose record never reached its
// builder (claim.go): it exists as that settle alone.
func fold(id string, evs []*buildv2.JobEvent) (jobRecord, bool) {
	rec := jobRecord{ID: id, seen: make(map[string]bool, len(evs))}
	found, pushed := false, false
	for _, ev := range evs {
		applied, moved := true, true
		switch k := ev.Kind.(type) {
		case *buildv2.JobEvent_Created:
			if pushed {
				applied = false
				break
			}
			c := k.Created
			pushed = true
			if !found {
				found = true
				rec.Status = int32(buildv2.JobStatus_JobStatusOpen)
			}
			rec.RepositoryKey, rec.Ref, rec.CommitSHA = c.RepositoryKey, c.Ref, c.CommitSha
			rec.ProjectID, rec.Environment, rec.Prelude = c.ProjectId, c.Environment, c.Prelude
			rec.CreatedAt = c.CreatedAt
		case *buildv2.JobEvent_Claimed:
			c := k.Claimed
			open := found && rec.status() == buildv2.JobStatus_JobStatusOpen
			reclaim := rec.status() == buildv2.JobStatus_JobStatusClaimed && rec.Attempt < c.Attempt
			if !open && !reclaim {
				applied = false
				break
			}
			rec.Status = int32(buildv2.JobStatus_JobStatusClaimed)
			rec.Attempt, rec.Holder, rec.ClaimedUntil = c.Attempt, c.Holder, c.ClaimedUntil
		case *buildv2.JobEvent_Settled:
			st := k.Settled
			orphan := !found
			if !orphan && (rec.status() != buildv2.JobStatus_JobStatusClaimed || rec.Attempt != st.Attempt) {
				applied = false
				break
			}
			found = true
			rec.Status = int32(st.Status)
			rec.Attempt, rec.Reason = st.Attempt, st.Reason
			rec.Holder, rec.ClaimedUntil = "", 0
			if st.Log != "" {
				rec.Logs = withEntry(rec.Logs, stamp(int64(st.Attempt)), st.Log)
			}
			for res, cid := range st.Assets {
				rec.Assets = withEntry(rec.Assets, res, cid)
			}
			if len(st.Children) > 0 {
				rec.Children = slices.Clone(st.Children)
			}
		case *buildv2.JobEvent_Logged:
			if !found {
				applied = false
				break
			}
			rec.Logs = withEntry(rec.Logs, stamp(int64(k.Logged.Attempt)), k.Logged.Log)
			// A log is not a transition, so the instant the job last moved stays.
			moved = false
		case *buildv2.JobEvent_Cancelled:
			if !found || rec.terminal() {
				applied = false
				break
			}
			rec.Status = int32(buildv2.JobStatus_JobStatusCancelled)
			rec.Reason = k.Cancelled.Reason
			rec.Holder, rec.ClaimedUntil = "", 0
		case *buildv2.JobEvent_Retried:
			if !found || !rec.terminal() {
				applied = false
				break
			}
			rec.Status = int32(buildv2.JobStatus_JobStatusOpen)
			rec.Reason = ""
		default:
			applied = false
		}
		if applied && moved {
			rec.UpdatedAt = time.Unix(0, ev.At).Unix()
		}
		rec.seen[eventName(ev.At, ev.Writer)] = applied
	}
	return rec, found
}

func withEntry(m map[string]string, key, value string) map[string]string {
	if m == nil {
		m = map[string]string{}
	}
	m[key] = value
	return m
}

// liveValue is what a live row holds.
func liveValue(cid string) ([]byte, error) {
	raw, err := (&buildv2.JobLive{Cid: cid}).MarshalVT()
	if err != nil {
		return nil, fmt.Errorf("encoding a live row: %w", err)
	}
	return raw, nil
}
