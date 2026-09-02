package backend

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"

	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
)

// store.go is the key layout — the only file in this package that builds a
// key. Every other file asks it for a row or a listing rather than
// concatenating a path itself, so there is exactly one place that has to agree
// with itself about the shape of a key.
//
// ── THE GRAMMAR ──────────────────────────────────────────────────────────
//
//	build/job/<seg(id)>/record                            the job itself
//	build/job/<seg(id)>/attempt/<%020d>/log/<seg(ts)>     -> cid, one row per entry
//	build/job/<seg(id)>/attempt/<%020d>/asset/<seg(rid)>  -> cid, one row per resource
//	build/index/project/<seg(pid)>/<seg(id)>              how a caller FINDS a job
//	build/index/open/<seg(repokey)>/<seg(id)>             non-terminal jobs
//	build/index/ref/<seg(repokey)>/<seg(ref)>/last        -> the last sha finished here
//	build/index/terminal/<%020d unix>/<seg(id)>           retention scan
//
// A segment is either a LITERAL spelled out in this file, an ID passed through
// seg(), or a fixed-width decimal. seg() is unpadded base64url (RFC 4648 §5),
// whose alphabet is A-Z a-z 0-9 - _ — so an encoded id NEVER contains "/" and
// is never "." or "..". Every boundary in every key is therefore at a fixed
// segment index, whatever the id was and however deep it reads. It is auth's
// segment(), carried rather than re-derived: it lost a repository to a raw
// splice once (services/v2/auth/store.go tells that story in full), and the
// inputs here are the same shape — a repository key is VARIABLE DEPTH, because
// a GitLab subgroup nests (`gitlab.com/acme/team/widget` has owner
// `acme/team`), and a git ref contains "/" by construction
// ("refs/heads/feature/x").
//
// WHICH ATTACK THAT CLOSES, concretely. Spliced raw, a push to the ref
// `heads/x/../../../job/<other id>/attempt` under a repository key an attacker
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
	attemptSegment  = "attempt"
	logSegment      = "log"
	assetSegment    = "asset"
	indexSegment    = "index"
	projectSegment  = "project"
	openSegment     = "open"
	refSegment      = "ref"
	lastSegment     = "last"
	terminalSegment = "terminal"

	// stampWidth zero-pads a decimal so that byte order is chronological
	// order and attempt 2 sorts after attempt 10 never happens (root
	// AGENTS.md rule 7). It is pkg/v2/queue's width, for the same reason.
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

// jobPrefix is everything one job owns. Every row of a job is under it, so
// nothing else has to know how many kinds of row there are.
func jobPrefix(id string) string {
	return rootSegment + "/" + jobSegment + "/" + seg(id) + "/"
}

func jobRecordKey(id string) string { return jobPrefix(id) + recordSegment }

// attemptPrefix is every log and asset row of one job, across all its
// attempts. Listed and sorted, it reads in attempt order, then asset before
// log within an attempt — which is why assembling a Job needs one listing
// rather than one per attempt.
func attemptPrefix(id string) string { return jobPrefix(id) + attemptSegment + "/" }

func attemptLogKey(id string, attempt uint32, at time.Time) string {
	return attemptPrefix(id) + stamp(int64(attempt)) + "/" + logSegment + "/" + seg(at.UTC().Format(time.RFC3339Nano))
}

func attemptAssetKey(id string, attempt uint32, resourceID string) string {
	return attemptPrefix(id) + stamp(int64(attempt)) + "/" + assetSegment + "/" + seg(resourceID)
}

// attemptRow is one parsed log or asset row.
type attemptRow struct {
	attempt uint32
	kind    string // logSegment or assetSegment
	key     string // the map key: an RFC3339 timestamp, or a resource id
}

// parseAttemptKey reads a key this package wrote under attemptPrefix(id).
func parseAttemptKey(id, key string) (attemptRow, error) {
	rest, ok := strings.CutPrefix(key, attemptPrefix(id))
	if !ok {
		return attemptRow{}, fmt.Errorf("key %q is not an attempt row of job %q", key, id)
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 3 {
		return attemptRow{}, fmt.Errorf("key %q is not an attempt row", key)
	}
	if parts[1] != logSegment && parts[1] != assetSegment {
		return attemptRow{}, fmt.Errorf("key %q names no row kind", key)
	}
	var attempt uint32
	if _, err := fmt.Sscanf(parts[0], "%d", &attempt); err != nil || len(parts[0]) != stampWidth {
		return attemptRow{}, fmt.Errorf("key %q has a malformed attempt number", key)
	}
	mapKey, err := unseg(parts[2])
	if err != nil {
		return attemptRow{}, err
	}
	return attemptRow{attempt: attempt, kind: parts[1], key: mapKey}, nil
}

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

// ── the record ───────────────────────────────────────────────────────────

// jobRecord is what build/job/<id>/record holds: the whole job except its logs
// and assets, which are rows of their own so that two writers can never
// read-modify-write one map (root AGENTS.md rule 1).
//
// cbor with integer keys, which is what pkg/raft stores its own values in and
// what pkg/v2/queue's own record uses, for the reason stated there: this never
// crosses a wire and no other language reads it. v2's "protobuf, never cbor"
// rule is about STREAMS; a stored record is not one, and the proto Job is a
// READ shape assembled from this plus the attempt rows.
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
	// Ack is a genuinely NEW queue item whose attempts start again, and the
	// attempt/ segment has to keep going up or a retry would overwrite the
	// previous run's rows.
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
// cannot match a job id (errors.go: 43 base64url characters), so "no id at all"
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

func (r jobRecord) status() buildv1.JobStatus { return buildv1.JobStatus(r.Status) }

// terminal reports whether this job is finished for good. A terminal job is
// never claimed, never re-queued by reconcile, and its open-index row is gone.
func (r jobRecord) terminal() bool {
	switch r.status() {
	case buildv1.JobStatus_JobStatusSuccess, buildv1.JobStatus_JobStatusFailed, buildv1.JobStatus_JobStatusCancelled:
		return true
	default:
		return false
	}
}
