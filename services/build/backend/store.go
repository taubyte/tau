package backend

import (
	"encoding/base64"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
)

// store.go is the key layout — the only file in this package that builds a
// key. Every other file asks it for a row or a listing rather than
// concatenating a path itself, so there is exactly one place that has to agree
// with itself about the shape of a key.
//
// ── THE GRAMMAR ──────────────────────────────────────────────────────────
//
//	build/job/<seg(id)>/record                       the job itself
//	build/index/project/<seg(pid)>/<seg(id)>          how a caller FINDS a job
//	build/index/open/<seg(repokey)>/<seg(id)>         non-terminal jobs
//	build/index/ref/<seg(repokey)>/<seg(ref)>/last    -> the last sha finished here
//	build/index/terminal/<%020d unix>/<seg(id)>       retention scan
//
// FIVE KEYS, AND ONE OF THEM IS THE JOB. There were seven, and the two that
// went were the LOG AND ASSET ROWS OF AN ATTEMPT — one replicated entry per log
// line of every build, for content no read of a record decides and no node has
// to agree with another about. A log is a FILE now (services/build/livelog),
// followed while it is written and handed to the asset store when the build
// ends, and what raft holds about it is the cid on the record. The writer that
// wrote those rows was the one writer in this package outside every section,
// and three of the last four escapes this package found came through it.
//
// A segment is either a LITERAL spelled out in this file, an ID passed through
// seg(), or a fixed-width decimal. seg() is unpadded base64url (RFC 4648 §5),
// whose alphabet is A-Z a-z 0-9 - _ — so an encoded id NEVER contains "/" and
// is never "." or "..". Every boundary in every key is therefore at a fixed
// segment index, whatever the id was and however deep it reads. It is auth's
// segment(), carried rather than re-derived: it lost a repository to a raw
// splice once (services/auth/store.go tells that story in full), and the
// inputs here are the same shape — a repository key is VARIABLE DEPTH, because
// a GitLab subgroup nests (`gitlab.com/acme/team/widget` has owner
// `acme/team`), and a git ref contains "/" by construction
// ("refs/heads/feature/x").
//
// WHICH ATTACK THAT CLOSES, concretely. Spliced raw, a push to the ref
// `heads/x/../../../job/<other id>/record` under a repository key an attacker
// can register would land its rows inside another job's prefix, and a job id
// is derived from (repository, ref, sha) — all three attacker-chosen for a
// repository they own. Encoded, "/" and ".." cannot survive into a key at all,
// and the property belongs to the ENCODING rather than to the inputs, so it
// holds for the ids this package never generates and only receives.
//
// ── AND EVERY PREFIX ENDS IN "/" ─────────────────────────────────────────
//
// The one rule the whole layout rests on. raft's Keys is a plain
// strings.HasPrefix over the key (pkg/raft/fsm.go, and MockCluster's is the
// same), and there is no ListRegEx here, so auth's second, independent guard —
// an anchored `^prefix[^/]+suffix$` — is unavailable and the encoding plus the
// trailing slash carry everything. Keys("build/job/AAA") returns rows of
// "build/job/AAAB" too; Keys("build/job/AAA/") does not. A missing slash is a
// silent cross-job read rather than a compile error, so store_test.go carries
// the weight auth's encoding and anchoring shared.
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

	// stampWidth zero-pads a decimal so that byte order is chronological
	// order and attempt 2 sorts after attempt 10 never happens (root
	// AGENTS.md rule 7). It is pkg/queue's width, for the same reason.
	stampWidth = 20
)

// seg encodes one id as one key segment. See the grammar above.
func seg(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id))
}

// unseg reverses seg for a key this package wrote. A segment that does not
// decode is a fault in whatever wrote it, so it is reported rather than
// skipped: skipping would hide a job from a listing that is meant to be
// complete.
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

// jobPrefix is everything one job owns — which is one row today, and is still
// a prefix rather than the record's key spelled directly: what the shape says
// is that the record is a row OF a job, so a second kind of row would be a
// sibling under it rather than a second top-level spelling of a job's identity.
func jobPrefix(id string) string {
	return rootSegment + "/" + jobSegment + "/" + seg(id) + "/"
}

func jobRecordKey(id string) string { return jobPrefix(id) + recordSegment }

// ── the indexes ──────────────────────────────────────────────────────────
//
// Every index row is SET MEMBERSHIP: the id is the last segment and the value
// is empty. One fact, one place — storing the id in the value too would be the
// same fact spelled twice, and the two spellings would be free to disagree.

func projectIndexPrefix(projectID string) string {
	return rootSegment + "/" + indexSegment + "/" + projectSegment + "/" + seg(projectID) + "/"
}

func projectIndexKey(projectID, id string) string {
	return projectIndexPrefix(projectID) + seg(id)
}

// assetKey is how one built artifact is named on a record and in the index.
//
// IT IS COMPOSED HERE AND NOT BY THE RUNNER, because the half of it that a
// reader has to reproduce is a grammar rather than a string: a worker asking
// "what does this resource run in production" has a project, a deployment and a
// resource id, and it has to arrive at the same key the build wrote. A runner
// that composed it would be a runner deciding a key shape two other packages
// depend on.
//
// A BUILD STANZA IS PER-DEPLOYMENT, so the deployment is in the key: one
// resource can have a different artifact in preview than in production, and a
// key that was only the resource id would have preview's build silently
// overwrite production's.
func assetKey(environment, resourceID string) string {
	return environment + "/" + resourceID
}

// artifactKey is the index row: what a project's deployment currently runs for
// one resource.
//
// THE PROJECT HALF IS THE JOB'S OWN and the rest is seg-escaped, so a runner
// choosing an environment or a resource id cannot compose a key outside its
// project's subtree — the same containment openIndexKey relies on.
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

// refLastKey holds the last commit sha this service FINISHED for a ref. It is
// the anchor build.proto requires the backend to compute for itself rather than
// take from the webhook: "before is whatever commit sha this backend itself
// last processed for this exact (repository_key, ref)".
func refLastKey(repositoryKey, ref string) string {
	return rootSegment + "/" + indexSegment + "/" + refSegment + "/" +
		seg(repositoryKey) + "/" + seg(ref) + "/" + lastSegment
}

// terminalIndexKey is the retention scan's row: zero-padded unix seconds first,
// so a listing is oldest-first and a retention pass can stop at a cutoff.
// Nothing sweeps it yet — the rows ship so the scan is possible, because a
// record is also what makes a replayed webhook idempotent, and deciding how
// long that must hold is a security question rather than a storage one.
func terminalIndexKey(at time.Time, id string) string {
	return rootSegment + "/" + indexSegment + "/" + terminalSegment + "/" +
		stamp(at.Unix()) + "/" + seg(id)
}

// parseOpenIndexKey reads BOTH halves of an open-index row back out of it.
//
// reconcile walks the index by prefix, so what it holds is a key rather than
// the two things that built one — and an entry that deletes a key it was handed
// says nothing about what it deletes. Splitting it here is what lets mend spell
// its delete openIndexKey(repositoryKey, id), which is the spelling
// TestARecordAndItsOpenRowMoveInOneEntry reads and the one
// TestEveryKeyAnEntryWritesIsBuiltWhereTheEntryIs requires.
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

// ── the rows ─────────────────────────────────────────────────────────────
//
// THERE IS NO set() AND NO del() IN THIS PACKAGE ANY MORE, and that is round
// ten. They were the only two expressions that made a row, they returned a
// row.Op, and a []row.Op was an ordinary slice — so the rows an entry had
// composed could be handed to the store with one more spliced in beside them,
// and the guard on that door read the outermost callee's NAME:
//
//	store.Batch(append(row.Ops(e.ops), forged), t)   -> "append", caught
//	store.Batch(row.Ops(append(e.ops, forged)), t)   -> "Ops",    green
//
// A row is now MADE INTO the entry it belongs to — row.Entry.Set and
// row.Entry.Delete, on a struct whose slice is unexported in another package —
// and row.Entry.Apply hands them to a store WITHOUT TAKING THEM. So there is no
// value of raft's batch type anywhere in this package: nothing to append to,
// nothing to splice into, and no second spelling of the same act. row/row.go
// states the whole of what a caller is left with.
//
// The key builders above are still called from exactly the places that compose
// the row, which is the half a type cannot say — population_test.go enumerates
// it from the other side.

// ── the record ───────────────────────────────────────────────────────────

// jobRecord is what build/job/<id>/record holds: the WHOLE job, including the
// content ids of what its runs produced.
//
// THE TWO MAPS WERE ROWS OF THEIR OWN and are fields now, which is the opposite
// of what root AGENTS.md rule 1 asks for and is right here for the reason the
// rule is about: a map re-serialized on each write is a read-modify-write, and
// what makes that a defect is TWO WRITERS. There was a second writer — the
// sink, outside every section by design, writing a row per log line as the
// build went — and there is not one now. Every write of this record is made by
// the body that read it, inside the section that took the lock (section.go), so
// a merge into these maps is serialized by the same thing that serializes the
// status beside it. What used to be one replicated entry per log line is one
// cid on the entry that settles the job.
//
// cbor with integer keys, which is what pkg/raft stores its own values in and
// what pkg/queue's own record uses, for the reason stated there: this never
// crosses a wire and no other language reads it. v2's "protobuf, never cbor"
// rule is about STREAMS; a stored record is not one, and the proto Job is a
// READ shape assembled from this and nothing else.
type jobRecord struct {
	ID            string `cbor:"1,keyasint"`
	Status        int32  `cbor:"2,keyasint"`
	RepositoryKey string `cbor:"3,keyasint"`
	Ref           string `cbor:"4,keyasint"`
	CommitSHA     string `cbor:"5,keyasint"`
	ProjectID     string `cbor:"6,keyasint"`
	// CreatedAt is unix seconds, and is what the proto's Job.timestamp
	// carries.
	CreatedAt int64 `cbor:"7,keyasint"`
	// Attempt counts the deliveries this record has seen. It is the record's
	// own counter rather than the queue Delivery's, because a Retry after an
	// Ack is a genuinely NEW queue item whose attempts start again, and this
	// counter has to keep going up or a retry would overwrite the previous
	// run's log: it is what a run is handed (Work.Attempt) and what that run's
	// output is filed under in Logs below.
	Attempt uint32 `cbor:"8,keyasint"`
	// UpdatedAt is unix seconds at the last transition. reconcile's age gate
	// reads it.
	UpdatedAt int64 `cbor:"9,keyasint"`
	// ClaimedUntil is unix nanoseconds, set while Claimed.
	ClaimedUntil int64 `cbor:"10,keyasint,omitempty"`
	// Holder is the peer id running this job, set while Claimed. It is
	// diagnostic: nothing routes on it, because a cancel is a record write
	// that the holder itself notices.
	Holder string `cbor:"11,keyasint,omitempty"`
	// Reason is why a job reached Failed. The proto's Job has no field for it,
	// so it is stored and not served — it is what distinguishes a runner's own
	// error from "attempts exhausted" when someone reads the store.
	Reason string `cbor:"12,keyasint,omitempty"`
	// Logs is the cid of each attempt's log, keyed by the attempt number of the
	// RUN THAT WROTE IT — which is carried by what the run produced rather than
	// read off this record, because this record is read again after the build
	// and what it says by then is another transition's business.
	//
	// KEYED BY THE ATTEMPT, and that is the v1 defect this does not carry over:
	// v1 keys its log map by time.Now().Format(time.RFC3339) (patrick's
	// Job.SetLog), which is a wall clock as a map key — it collides inside one
	// second, it sorts lexically rather than causally, and it says nothing
	// about WHICH RUN produced the entry it names. An attempt number is the
	// causal order, it is this record's own counter, and a retry cannot
	// overwrite the previous run's log because the counter only goes up.
	//
	// It is zero-padded (stamp) for root AGENTS.md rule 7: attempt 10 must not
	// sort before attempt 2 in a reader that sorts the keys it was handed.
	Logs map[string]string `cbor:"13,keyasint,omitempty"`
	// Assets is the cid of the artifact built for each resource id, which is
	// what v1's Job.SetCid holds and what its /download/{jobId}/{resourceId}
	// route reads back. A later attempt's artifact replaces an earlier one:
	// there is one current build of a resource, not a history of them.
	Assets map[string]string `cbor:"14,keyasint,omitempty"`
}

// with is this record plus what a run produced. The maps are COPIED rather than
// added to, because the record it is called on is the one the section READ and
// its maps are the holding's: appending through them would edit what the
// section believes it read, in place, before anything was committed.
func (r jobRecord) with(p produced) jobRecord {
	if p.logCID != "" {
		r.Logs = merged(r.Logs, map[string]string{stamp(int64(p.attempt)): p.logCID})
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

func (r jobRecord) encode() ([]byte, error) { return cbor.Marshal(r) }

// decodeJobRecord reads one record back, AND CHECKS THAT IT IS THE RECORD THE
// KEY NAMES.
//
// It took the key for an error message alone until the round that made a write
// name the job it was decided on, and the omission was the same shape one
// direction over: a row that decodes cleanly and says it is some other job was
// handed to the caller as this job's record, and every transition then decided
// on it — begin composed its entry's key from rec.ID, so a record saying it
// belonged elsewhere would have been WRITTEN elsewhere. It cannot be now, and
// the read it came from is refused here instead of quietly renaming it.
//
// BOTH THE KEY AND THE ID ARE PASSED, because they are two different things:
// the key is where this came from and belongs in the message, and the id is
// what it has to agree with. The one caller has both already. An empty id
// cannot match a job id (errors.go: 64 hex characters), so "no id at all"
// is the same failure and needs no separate branch.
func decodeJobRecord(key, id string, raw []byte) (jobRecord, error) {
	var r jobRecord
	if err := cbor.Unmarshal(raw, &r); err != nil {
		return jobRecord{}, fmt.Errorf("corrupt job record at %q: %w", key, err)
	}
	if r.ID != id {
		return jobRecord{}, fmt.Errorf("corrupt job record at %q: it says it is job %q", key, r.ID)
	}
	return r, nil
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
