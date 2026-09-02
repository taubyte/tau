package backend

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/raft"
	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
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
		attemptPrefix(short),
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

// Byte order is the only sort order (root AGENTS.md rule 7), so the attempt
// number is zero-padded: attempt 2 must sort before attempt 10.
func TestAttemptRowsSortInAttemptOrder(t *testing.T) {
	id := "job"
	keys := []string{
		attemptAssetKey(id, 10, "r"),
		attemptLogKey(id, 2, time.Unix(0, 0)),
		attemptAssetKey(id, 2, "r"),
	}
	slices.Sort(keys)

	require.Equal(t, []string{
		attemptAssetKey(id, 2, "r"),
		attemptLogKey(id, 2, time.Unix(0, 0)),
		attemptAssetKey(id, 10, "r"),
	}, keys)
}

func TestParseAttemptKey(t *testing.T) {
	id := "job"

	row, err := parseAttemptKey(id, attemptAssetKey(id, 3, "website/home"))
	require.NoError(t, err)
	require.Equal(t, attemptRow{attempt: 3, kind: assetSegment, key: "website/home"}, row)

	at := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	row, err = parseAttemptKey(id, attemptLogKey(id, 1, at))
	require.NoError(t, err)
	require.Equal(t, uint32(1), row.attempt)
	require.Equal(t, logSegment, row.kind)
	require.Equal(t, at.Format(time.RFC3339Nano), row.key)

	for _, bad := range []string{
		"somewhere/else",
		attemptPrefix(id) + "0001/log/AAAA",            // attempt not fixed width
		attemptPrefix(id) + stamp(1) + "/other/AAAA",   // unknown row kind
		attemptPrefix(id) + stamp(1) + "/log/!!!",      // undecodable map key
		attemptPrefix(id) + stamp(1) + "/log/AA/extra", // too many segments
		attemptPrefix(id) + "notanumberatallnotanumb/log/AA",
	} {
		_, err := parseAttemptKey(id, bad)
		require.Error(t, err, "parseAttemptKey(%q) must fail", bad)
	}
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

	got, err := decodeJobRecord("k", raw)
	require.NoError(t, err)
	require.Equal(t, rec, got)
}

func TestADecodedRecordMustHaveAnID(t *testing.T) {
	raw, err := jobRecord{}.encode()
	require.NoError(t, err)

	_, err = decodeJobRecord("build/job/x/record", raw)
	require.ErrorContains(t, err, "no id")

	_, err = decodeJobRecord("build/job/x/record", []byte("not cbor at all"))
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
