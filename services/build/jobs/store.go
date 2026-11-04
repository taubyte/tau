package jobs

import (
	"encoding/base64"
	"fmt"
	"maps"
	"slices"
	"strings"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
)

// store.go is how an id becomes a key segment and what a job is once folded; the
// keys themselves are events.go's.
//
// A segment is a literal, an id through seg(), or a fixed-width decimal. seg() is
// unpadded base64url, so an encoded id never contains "/" and is never "." or
// "..", and every boundary sits at a fixed index. Repository keys and refs contain
// "/" (GitLab subgroups, refs/heads/feature/x); spliced raw, an attacker's ref
// could land rows in another job's prefix.
//
// Every prefix ends in "/": a listing is a plain prefix match, so a prefix that
// stopped mid-segment would also list another id's rows.
const (
	rootSegment     = "build"
	jobSegment      = "job"
	projectSegment  = "project"
	openSegment     = "open"
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

// assetKey names one built artifact, composed here so a worker reproduces the key
// the build wrote. The deployment is in it, since build stanzas differ per
// deployment.
func assetKey(environment, resourceID string) string {
	return environment + "/" + resourceID
}

// splitAssetKey is assetKey's two parts. An environment name holds no "/".
func splitAssetKey(key string) (environment, resourceID string, ok bool) {
	return strings.Cut(key, "/")
}

// jobRecord is a job as the fold of its events makes it (events.go), including its
// runs' cids. It is never stored: what is stored is the events.
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

	// seen is every event the fold read, by eventName, and whether it applied. A
	// drop takes only rows of events it read, since a row beside one it did not see
	// is a decision it did not make; a reader counts only rows of applied ones.
	seen map[string]bool
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

func (r jobRecord) status() buildv2.JobStatus { return buildv2.JobStatus(r.Status) }

// terminal reports whether this job is finished for good. A terminal job is
// never claimed, never re-queued by reconcile, and its open rows go.
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
