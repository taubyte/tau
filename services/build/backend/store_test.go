package backend

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	buildv1 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v1"
	"github.com/taubyte/tau/pkg/raft"
)

// THE PREFIX RULE, AGAINST A REAL STORE. raft's Keys is a plain HasPrefix, so a
// prefix that does not end in "/" reads rows of every job whose id happens to
// start with the same characters. There is no ListRegEx here to catch it a
// second time, so this is the whole guard.
func TestEveryPrefixEndsInASlashAndACrossJobReadIsImpossible(t *testing.T) {
	store := raft.NewMockCluster()

	// Two ids where one encodes to a prefix of the other. seg() is base64url,
	// so this is exactly what "AAA" and "AAAB" do.
	short, long := "AAA", "AAAB"
	require.True(t, strings.HasPrefix(seg(long), seg(short)))

	require.NoError(t, store.Set(jobRecordKey(short), []byte("short"), time.Second))
	require.NoError(t, store.Set(jobRecordKey(long), []byte("long"), time.Second))

	for _, prefix := range []string{
		jobPrefix(short),
		projectIndexPrefix("p"),
		openIndexPrefix("github.com/a/b"),
		openIndexRoot(),
	} {
		require.True(t, strings.HasSuffix(prefix, "/"), "prefix %q must end in a slash", prefix)
	}

	require.Len(t, store.Keys(jobPrefix(short)), 1, "a job's prefix must reach only its own rows")
	require.Len(t, store.Keys(jobPrefix(long)), 1)

	// And the failure this guards, spelled out: without the trailing slash the
	// same listing reaches both.
	require.Len(t, store.Keys(strings.TrimSuffix(jobPrefix(short), "/")), 2,
		"this is what a missing trailing slash costs")
}

// seg() is what makes the grammar hold for ids this package never generates.
// A ref contains "/" by construction and a repository key is variable depth.
func TestNoInputCanForgeASegmentBoundary(t *testing.T) {
	for _, hostile := range []string{
		"refs/heads/x", "../../etc", "..", ".", "", "a/b/c/d",
		"gitlab.com/acme/team/widget", "%2f", "a//b", "\x00", "🙂/x",
	} {
		encoded := seg(hostile)
		require.NotContains(t, encoded, "/", "seg(%q) leaked a separator", hostile)
		require.NotEqual(t, ".", encoded)
		require.NotEqual(t, "..", encoded)

		back, err := unseg(encoded)
		require.NoError(t, err)
		require.Equal(t, hostile, back, "seg must be injective")
	}
}

// A ref crafted to climb out of its own job's prefix cannot: the whole ref is
// one segment, so there is nowhere to climb to.
func TestAHostileRefCannotReachAnotherJobsRows(t *testing.T) {
	victim := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	hostileRef := "refs/heads/../../../job/" + seg(victim) + "/attempt"

	key := refLastKey("github.com/a/b", hostileRef)
	require.NotContains(t, strings.TrimPrefix(key, rootSegment+"/"+indexSegment+"/"+refSegment+"/"),
		"..", "a ref must not survive into a key as path syntax")
	require.False(t, strings.HasPrefix(key, jobPrefix(victim)))
}

func TestUnsegRejectsAMalformedSegment(t *testing.T) {
	_, err := unseg("!!!not base64!!!")
	require.Error(t, err)

	_, err = idFromIndexKey("no-slash-here!!!")
	require.Error(t, err)

	_, err = idFromIndexKey(projectIndexPrefix("p") + "!!!")
	require.Error(t, err)
}

func TestIDFromIndexKeyReadsTheIDOffTheEnd(t *testing.T) {
	id := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	for _, key := range []string{
		projectIndexKey("proj", id),
		openIndexKey("github.com/a/b", id),
		terminalIndexKey(time.Unix(1_700_000_000, 0), id),
	} {
		got, err := idFromIndexKey(key)
		require.NoError(t, err)
		require.Equal(t, id, got)
	}
}

// mend DELETES A KEY IT REBUILDS, so the round trip has to be the identity or
// the repair deletes a row nobody asked it to. A repository key is variable
// depth and a job id is opaque; both survive because seg() puts neither a "/"
// nor a "." in a segment.
func TestParseOpenIndexKeyRoundTrips(t *testing.T) {
	for _, repositoryKey := range []string{
		"github.com/a/b",
		"gitlab.com/acme/team/widget",
		"github.com/a/b-c_d",
	} {
		for _, id := range []string{
			"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			"a/b/../c",
			"",
		} {
			key := openIndexKey(repositoryKey, id)
			gotRepo, gotID, err := parseOpenIndexKey(key)
			require.NoError(t, err)
			require.Equal(t, repositoryKey, gotRepo)
			require.Equal(t, id, gotID)
			require.Equal(t, key, openIndexKey(gotRepo, gotID),
				"mend deletes what it rebuilds, so the two must be one key")
		}
	}
}

func TestParseOpenIndexKeyRejectsWhatIsNotOne(t *testing.T) {
	for _, key := range []string{
		"",
		projectIndexKey("p", "id"),
		openIndexRoot(),
		openIndexPrefix("github.com/a/b") + "!!!",
		openIndexRoot() + "!!!/" + seg("id"),
		openIndexKey("github.com/a/b", "id") + "/extra",
	} {
		_, _, err := parseOpenIndexKey(key)
		require.Error(t, err, "key %q", key)
	}
}

// Byte order is the only sort order (root AGENTS.md rule 7), so an attempt is
// zero-padded wherever it is a key: attempt 2 must sort before attempt 10 in a
// reader that sorts the log map it was handed.
func TestTheLogsOfARecordSortInAttemptOrder(t *testing.T) {
	rec := jobRecord{}.with(produced{attempt: 2, logCID: "cid-2"})
	rec = rec.with(produced{attempt: 10, logCID: "cid-10"})

	keys := names(rec.Logs)
	slices.Sort(keys)
	require.Equal(t, []string{stamp(2), stamp(10)}, keys)
	require.Equal(t, map[string]string{stamp(2): "cid-2", stamp(10): "cid-10"}, rec.Logs)
}

// A RECORD'S MAPS ARE COPIED RATHER THAN ADDED TO. The record a section read
// carries the holding's maps, and a transition that appended through them would
// edit what the section believes it read, in place, before anything was
// committed — and a rolled-back or refused entry would leave the edit behind.
func TestWithLeavesTheRecordItWasBuiltFromAlone(t *testing.T) {
	before := jobRecord{
		Attempt: 1,
		Logs:    map[string]string{stamp(1): "cid-log-1"},
		Assets:  map[string]string{"prod/website/home": "cid-a-1"},
	}

	after := before.with(produced{
		attempt: 1,
		logCID:  "cid-log-2",
		assets:  map[assetRef]string{{environment: "prod", resource: "website/home"}: "cid-a-2"},
	})

	require.Equal(t, map[string]string{stamp(1): "cid-log-1"}, before.Logs, "the record read is untouched")
	require.Equal(t, map[string]string{"prod/website/home": "cid-a-1"}, before.Assets)
	require.Equal(t, map[string]string{stamp(1): "cid-log-2"}, after.Logs,
		"and a second run of one attempt replaces that attempt's log")
	// THE RECORD'S KEY IS THE ONE A READER COMPOSES: deployment then resource,
	// so a worker asking what a resource runs in production arrives at the same
	// string the build wrote.
	require.Equal(t, map[string]string{"prod/website/home": "cid-a-2"}, after.Assets)

	// A run that produced nothing changes nothing, so a transition with no
	// output does not have to say so.
	require.Equal(t, before, before.with(produced{}))
}

func TestStampClampsBelowZeroAndIsFixedWidth(t *testing.T) {
	require.Equal(t, strings.Repeat("0", stampWidth), stamp(-1))
	require.Len(t, stamp(1_700_000_000), stampWidth)
	require.Less(t, stamp(2), stamp(10))
}

func TestJobRecordRoundTrips(t *testing.T) {
	rec := jobRecord{
		ID:            "id",
		Status:        int32(buildv1.JobStatus_JobStatusClaimed),
		RepositoryKey: "github.com/a/b",
		Ref:           "refs/heads/main",
		CommitSHA:     strings.Repeat("a", 40),
		ProjectID:     "proj",
		CreatedAt:     10,
		Attempt:       2,
		UpdatedAt:     20,
		ClaimedUntil:  30,
		Holder:        "peer",
		Reason:        "why",
	}

	raw, err := rec.encode()
	require.NoError(t, err)

	got, err := decodeJobRecord(jobRecordKey(rec.ID), rec.ID, raw)
	require.NoError(t, err)
	require.Equal(t, rec, got)
}

// A DECODED RECORD MUST BE THE RECORD ITS KEY NAMES, and until the round that
// made every write name the job it was decided on, nothing compared the two:
// the key was taken for the error message alone.
//
// A row that decodes cleanly and says it belongs to somebody else was handed
// back as this job's record, and every transition then decided on it — begin
// composed its entry's key from rec.ID, so a record claiming another job would
// have been written under another job's key, in a section entered on this one.
func TestADecodedRecordMustBeTheOneItsKeyNames(t *testing.T) {
	const id = "0123456789012345678901234567890123456789abc"

	neighbour, err := jobRecord{ID: "a completely different job"}.encode()
	require.NoError(t, err)
	_, err = decodeJobRecord(jobRecordKey(id), id, neighbour)
	require.ErrorContains(t, err, "it says it is job")
	require.ErrorContains(t, err, "a completely different job")

	// A record with no id at all is the same failure and needs no branch of its
	// own: a job id is never empty (errors.go).
	anonymous, err := jobRecord{}.encode()
	require.NoError(t, err)
	_, err = decodeJobRecord(jobRecordKey(id), id, anonymous)
	require.ErrorContains(t, err, "it says it is job")

	_, err = decodeJobRecord(jobRecordKey(id), id, []byte("not cbor at all"))
	require.ErrorContains(t, err, "corrupt")
}

func TestTerminalIsExactlyTheThreeEndStates(t *testing.T) {
	for status, want := range map[buildv1.JobStatus]bool{
		buildv1.JobStatus_JobStatusOpen:      false,
		buildv1.JobStatus_JobStatusClaimed:   false,
		buildv1.JobStatus_JobStatusSuccess:   true,
		buildv1.JobStatus_JobStatusFailed:    true,
		buildv1.JobStatus_JobStatusCancelled: true,
	} {
		rec := jobRecord{Status: int32(status)}
		require.Equal(t, want, rec.terminal(), "status %v", status)
		require.Equal(t, status, rec.status())
	}
}

// The queue's own keyspace is "queue/<escaped name>/", so build's rows and the
// queue's rows share a store without sharing a prefix. Two prefixes that could
// overlap would put a job record where a queue scan reads it.
func TestBuildsRowsAndTheQueuesRowsCannotOverlap(t *testing.T) {
	require.True(t, strings.HasPrefix(jobPrefix("x"), rootSegment+"/"))
	require.False(t, strings.HasPrefix(jobPrefix("x"), "queue/"))
	require.False(t, strings.HasPrefix(openIndexRoot(), "queue/"))
}
