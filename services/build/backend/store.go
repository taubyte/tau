package backend

import (
	"encoding/base64"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/taubyte/tau/pkg/proto/codec"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
)

// store.go is the key layout, the only file here that builds a key:
//
//	build/job/<seg(id)>/record                       the job itself
//	build/index/project/<seg(pid)>/<seg(id)>          how a caller finds a job
//	build/index/open/<seg(repokey)>/<seg(id)>         non-terminal jobs
//	build/index/ref/<seg(repokey)>/<seg(ref)>/last    -> the last sha finished here
//	build/index/terminal/<%020d unix>/<seg(id)>       retention scan
//
// A segment is a literal, an id through seg(), or a fixed-width decimal. seg() is
// unpadded base64url, so an encoded id never contains "/" and is never "." or
// "..", and every boundary sits at a fixed index. Repository keys and refs contain
// "/" (GitLab subgroups, refs/heads/feature/x); spliced raw, an attacker's ref
// could land rows in another job's prefix.
//
// Every prefix ends in "/": raft's Keys is a plain HasPrefix and there is no
// anchored listing, so Keys("build/job/AAA") would also return "build/job/AAAB"
// rows.
const (
	rootSegment     = "build"
	jobSegment      = "job"
	recordSegment   = "record"
	indexSegment    = "index"
	projectSegment  = "project"
	openSegment     = "open"
	refSegment      = "ref"
	artifactSegment = "artifact"
	lastSegment     = "last"
	terminalSegment = "terminal"

	// stampWidth zero-pads decimals so byte order is numeric order (AGENTS.md rule 7).
	stampWidth = 20
)

// seg encodes one id as one key segment. See the grammar above.
func seg(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id))
}

// unseg reverses seg; an undecodable segment is reported, since skipping would
// hide a job from a listing meant to be complete.
func unseg(s string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("malformed key segment %q: %w", s, err)
	}
	return string(raw), nil
}

func stamp(n int64) string {
	if n < 0 {
		n = 0
	}
	return fmt.Sprintf("%0*d", stampWidth, n)
}

// jobPrefix is everything one job owns, kept a prefix so another row kind would
// be a sibling under it.
func jobPrefix(id string) string {
	return rootSegment + "/" + jobSegment + "/" + seg(id) + "/"
}

func jobRecordKey(id string) string { return jobPrefix(id) + recordSegment }

// Every index row is set membership: the id is the last segment and the value is
// empty.

func projectIndexPrefix(projectID string) string {
	return rootSegment + "/" + indexSegment + "/" + projectSegment + "/" + seg(projectID) + "/"
}

func projectIndexKey(projectID, id string) string {
	return projectIndexPrefix(projectID) + seg(id)
}

// assetKey names one built artifact, composed here so a worker reproduces the key
// the build wrote. The deployment is in it, since build stanzas differ per
// deployment.
func assetKey(environment, resourceID string) string {
	return environment + "/" + resourceID
}

// artifactKey is what a project's deployment currently runs for one resource. The
// project half is the job's own and the rest seg-escaped, so a runner cannot
// reach outside its project's subtree.
func artifactKey(projectID, environment, resourceID string) string {
	return artifactPrefix(projectID) + seg(assetKey(environment, resourceID))
}

func artifactPrefix(projectID string) string {
	return rootSegment + "/" + artifactSegment + "/" + seg(projectID) + "/"
}

func openIndexPrefix(repositoryKey string) string {
	return rootSegment + "/" + indexSegment + "/" + openSegment + "/" + seg(repositoryKey) + "/"
}

func openIndexKey(repositoryKey, id string) string {
	return openIndexPrefix(repositoryKey) + seg(id)
}

// openIndexRoot is every open job of every repository — what reconcile walks.
func openIndexRoot() string {
	return rootSegment + "/" + indexSegment + "/" + openSegment + "/"
}

// refLastKey holds the last commit this service finished for a ref in one
// environment, the diff anchor it computes itself rather than taking from the
// webhook. Per environment, since each builds on its own.
func refLastKey(repositoryKey, ref, environment string) string {
	return rootSegment + "/" + indexSegment + "/" + refSegment + "/" +
		seg(repositoryKey) + "/" + seg(ref) + "/" + seg(environment) + "/" + lastSegment
}

// terminalIndexKey is the retention row, unix seconds first so a listing is
// oldest-first. Nothing sweeps it yet: how long a record must survive to keep
// replayed webhooks idempotent is a security decision.
func terminalIndexKey(at time.Time, id string) string {
	return rootSegment + "/" + indexSegment + "/" + terminalSegment + "/" +
		stamp(at.Unix()) + "/" + seg(id)
}

// parseOpenIndexKey reads both halves of an open-index row, so mend's delete is
// spelled openIndexKey(repositoryKey, id).
func parseOpenIndexKey(key string) (repositoryKey, id string, err error) {
	rest, ok := strings.CutPrefix(key, openIndexRoot())
	if !ok {
		return "", "", fmt.Errorf("key %q is not an open index row", key)
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("key %q is not an open index row", key)
	}
	if repositoryKey, err = unseg(parts[0]); err != nil {
		return "", "", err
	}
	if id, err = unseg(parts[1]); err != nil {
		return "", "", err
	}
	return repositoryKey, id, nil
}

// idFromIndexKey reads the job id off the end of an index row.
func idFromIndexKey(key string) (string, error) {
	i := strings.LastIndex(key, "/")
	if i < 0 {
		return "", fmt.Errorf("index key %q has no id segment", key)
	}
	return unseg(key[i+1:])
}

// jobRecord is what build/job/<id>/record holds: the whole job, including its
// runs' cids. The maps are fields despite AGENTS.md rule 1 because every write of
// this record is made by the body that read it, inside its section, so there is
// one writer. Stored as buildv2.JobRecord; it never crosses a wire.
type jobRecord struct {
	ID            string
	Status        int32
	RepositoryKey string
	Ref           string
	CommitSHA     string
	ProjectID     string
	// CreatedAt is unix seconds, and is what the proto's Job.timestamp
	// carries.
	CreatedAt int64
	// Attempt counts the deliveries this record has seen: the record's own counter,
	// since a Retry after an Ack starts a new queue item, and a run's log is filed
	// under it.
	Attempt uint32
	// UpdatedAt is unix seconds at the last transition. reconcile's age gate
	// reads it.
	UpdatedAt int64
	// ClaimedUntil is unix nanoseconds, set while Claimed.
	ClaimedUntil int64
	// Holder is the peer id running this job, set while Claimed. It is
	// diagnostic: nothing routes on it, because a cancel is a record write
	// that the holder itself notices.
	Holder string
	// Reason is why a job reached Failed. The proto's Job has no field for it,
	// so it is stored and not served — it is what distinguishes a runner's own
	// error from "attempts exhausted" when someone reads the store.
	Reason string
	// Logs is the cid of each attempt's log, keyed by the attempt of the run that
	// wrote it (carried by produced, not re-read from the record), zero-padded so
	// attempt 10 sorts after 2.
	Logs map[string]string
	// Assets is the cid of the artifact built per resource; a later attempt replaces
	// an earlier one.
	Assets map[string]string
	// Environment is the one deployment this job builds; empty is a plan.
	Environment string
	// Prelude is what the plan said, written first into this job's log.
	Prelude string
	// Children is the environment jobs a plan enqueued.
	Children []string
}

// with is this record plus what a run produced. The maps are copied: the record is
// the one the section read, and its maps are the holding's.
func (r jobRecord) with(p produced) jobRecord {
	if p.logCID != "" {
		r.Logs = merged(r.Logs, map[string]string{stamp(int64(p.attempt)): p.logCID})
	}
	if len(p.children) > 0 {
		r.Children = slices.Clone(p.children)
	}
	if len(p.assets) > 0 {
		named := make(map[string]string, len(p.assets))
		for ref, cid := range p.assets {
			named[assetKey(ref.environment, ref.resource)] = cid
		}
		r.Assets = merged(r.Assets, named)
	}
	return r
}

// copied is a map of its own, for the same reason merged(m, nil) is: what a run
// hands to a section must not be the map that run is still writing into.
func copied[K comparable, V any](from map[K]V) map[K]V {
	out := make(map[K]V, len(from))
	maps.Copy(out, from)
	return out
}

// merged is one map over another, into a third. merged(m, nil) is a copy, which
// is what a READ shape wants: the proto's map handed to a caller must not be
// the record's own.
func merged(into, from map[string]string) map[string]string {
	out := make(map[string]string, len(into)+len(from))
	maps.Copy(out, into)
	maps.Copy(out, from)
	return out
}

func (r jobRecord) encode() ([]byte, error) {
	m := buildv2.JobRecord{
		Id: r.ID, Status: buildv2.JobStatus(r.Status), RepositoryKey: r.RepositoryKey, Ref: r.Ref,
		CommitSha: r.CommitSHA, ProjectId: r.ProjectID, CreatedAt: r.CreatedAt, Attempt: r.Attempt,
		UpdatedAt: r.UpdatedAt, ClaimedUntil: r.ClaimedUntil, Holder: r.Holder, Reason: r.Reason,
		Logs: r.Logs, Assets: r.Assets, Environment: r.Environment, Prelude: r.Prelude, Children: r.Children,
	}
	return m.MarshalVT()
}

// decodeJobRecord reads one record back and checks it is the record the key names:
// a row saying it is another job is refused. key is for the message, id what it
// must match; an empty id never matches.
func decodeJobRecord(key, id string, raw []byte) (jobRecord, error) {
	var m buildv2.JobRecord
	if err := codec.Decode(raw, &m); err != nil {
		return jobRecord{}, fmt.Errorf("corrupt job record at %q: %w", key, err)
	}
	if m.Id != id {
		return jobRecord{}, fmt.Errorf("corrupt job record at %q: it says it is job %q", key, m.Id)
	}
	return jobRecord{
		ID: m.Id, Status: int32(m.Status), RepositoryKey: m.RepositoryKey, Ref: m.Ref,
		CommitSHA: m.CommitSha, ProjectID: m.ProjectId, CreatedAt: m.CreatedAt, Attempt: m.Attempt,
		UpdatedAt: m.UpdatedAt, ClaimedUntil: m.ClaimedUntil, Holder: m.Holder, Reason: m.Reason,
		Logs: m.Logs, Assets: m.Assets, Environment: m.Environment, Prelude: m.Prelude, Children: m.Children,
	}, nil
}

func (r jobRecord) status() buildv2.JobStatus { return buildv2.JobStatus(r.Status) }

// terminal reports whether this job is finished for good. A terminal job is
// never claimed, never re-queued by reconcile, and its open-index row is gone.
func (r jobRecord) terminal() bool { return finished(r.status()) }

// finished is terminal for a status, which is how a Job carries it.
func finished(status buildv2.JobStatus) bool {
	switch status {
	case buildv2.JobStatus_JobStatusSuccess, buildv2.JobStatus_JobStatusFailed, buildv2.JobStatus_JobStatusCancelled:
		return true
	default:
		return false
	}
}
