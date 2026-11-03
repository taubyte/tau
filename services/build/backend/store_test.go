package backend

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
)

func TestEveryPrefixEndsInASlashAndACrossJobReadIsImpossible(t *testing.T) {
	ctx := t.Context()
	db, err := kvdbmock.New().New(nil, "build", 0)
	require.NoError(t, err)

	// Two ids where one encodes to a prefix of the other. seg() is base64url,
	// so this is exactly what "AAA" and "AAAB" do.
	short, long := "AAA", "AAAB"
	require.True(t, strings.HasPrefix(seg(long), seg(short)))

	require.NoError(t, db.Put(ctx, eventKey(short, 1, "w"), []byte("short")))
	require.NoError(t, db.Put(ctx, eventKey(long, 1, "w"), []byte("long")))

	for _, prefix := range []string{
		eventPrefix(short),
		projectRows("p"),
		openRoot(),
		openRows("github.com/a/b"),
		openRowsOf("github.com/a/b", short),
		anchorRows("github.com/a/b", testRef, testEnvironment),
		liveRows("p", testEnvironment, "website/home"),
		terminalRows(),
	} {
		require.True(t, strings.HasSuffix(prefix, "/"), "prefix %q must end in a slash", prefix)
	}

	keys, err := db.List(ctx, eventPrefix(short))
	require.NoError(t, err)
	require.Len(t, keys, 1, "a job's prefix must reach only its own events")
	keys, err = db.List(ctx, eventPrefix(long))
	require.NoError(t, err)
	require.Len(t, keys, 1)

	// And the failure this guards, spelled out: without the trailing slash the
	// same listing reaches both.
	keys, err = db.List(ctx, strings.TrimSuffix(eventPrefix(short), "/"+eventSegment+"/"))
	require.NoError(t, err)
	require.Len(t, keys, 2, "this is what a missing trailing slash costs")
}

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

func TestAHostileRefCannotReachAnotherJobsRows(t *testing.T) {
	victim := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	hostileRef := "refs/heads/../../../job/" + seg(victim) + "/event"

	key := anchorRow("github.com/a/b", hostileRef, testEnvironment, 1, "id", "w")
	require.NotContains(t, strings.TrimPrefix(key, "/"+rootSegment+"/"+anchorSegment+"/"),
		"..", "a ref must not survive into a key as path syntax")
	require.False(t, strings.HasPrefix(key, eventPrefix(victim)))
}

func TestUnsegRejectsAMalformedSegment(t *testing.T) {
	_, err := unseg("!!!not base64!!!")
	require.Error(t, err)

	_, err = lastSegmentOf(projectRows("p") + "!!!")
	require.Error(t, err)
}

func TestTheLastSegmentOfARowIsItsJob(t *testing.T) {
	id := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	for _, key := range []string{
		projectRow("proj", id),
		terminalRow(time.Unix(1_700_000_000, 0), id),
	} {
		got, err := lastSegmentOf(key)
		require.NoError(t, err)
		require.Equal(t, id, got)
	}
}

func TestARowBesideAnEventNamesThatEvent(t *testing.T) {
	id := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	for prefix, key := range map[string]string{
		anchorRows("github.com/a/b", testRef, testEnvironment): anchorRow("github.com/a/b", testRef, testEnvironment, 7, id, "a/writer"),
		liveRows("proj", testEnvironment, "website/home"):      liveRow("proj", testEnvironment, "website/home", 7, id, "a/writer"),
	} {
		at, got, writer, err := parseBeside(prefix, key)
		require.NoError(t, err)
		require.Equal(t, int64(7), at)
		require.Equal(t, id, got)
		require.Equal(t, "a/writer", writer)
	}
	for _, key := range []string{
		liveRows("p", "e", "r") + "!!!",
		liveRows("p", "e", "r") + stamp(1) + "/!!!/" + seg("w"),
		liveRows("p", "e", "r") + stamp(1) + "/" + seg("id") + "/!!!",
		liveRows("p", "e", "r") + "x/" + seg("id") + "/" + seg("w"),
		anchorRows("r", "f", "e") + besideName(1, "id", "w"),
	} {
		_, _, _, err := parseBeside(liveRows("p", "e", "r"), key)
		require.Error(t, err, "key %q", key)
	}
}

func TestAnOpenRowRoundTrips(t *testing.T) {
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
			key := openRow(repositoryKey, id, 7, "writer/with/slashes")
			gotRepo, gotID, err := parseOpenRow(key)
			require.NoError(t, err)
			require.Equal(t, repositoryKey, gotRepo)
			require.Equal(t, id, gotID)
			at, err := openRowAt(key)
			require.NoError(t, err)
			require.Equal(t, int64(7), at)
			require.Equal(t, key, openRow(gotRepo, gotID, at, "writer/with/slashes"),
				"mend deletes what it rebuilds, so the two must be one key")
			require.Equal(t, openRowsOf(gotRepo, gotID)+eventName(7, "writer/with/slashes"), key,
				"and the row is named by the event it was written beside")
		}
	}
}

func TestParseOpenRowRejectsWhatIsNotOne(t *testing.T) {
	for _, key := range []string{
		"",
		projectRow("p", "id"),
		openRoot(),
		openRows("github.com/a/b") + "!!!",
		openRoot() + "!!!/" + seg("id") + "/" + eventName(1, "w"),
		openRoot() + seg("github.com/a/b") + "/!!!/" + eventName(1, "w"),
		openRow("github.com/a/b", "id", 1, "w") + "/extra",
	} {
		_, _, err := parseOpenRow(key)
		require.Error(t, err, "key %q", key)
	}
	_, err := openRowAt("nothing")
	require.Error(t, err)
}

func TestTheLogsOfARecordSortInAttemptOrder(t *testing.T) {
	rec := jobRecord{}.with(produced{attempt: 2, logCID: "cid-2"})
	rec = rec.with(produced{attempt: 10, logCID: "cid-10"})

	keys := names(rec.Logs)
	slices.Sort(keys)
	require.Equal(t, []string{stamp(2), stamp(10)}, keys)
	require.Equal(t, map[string]string{stamp(2): "cid-2", stamp(10): "cid-10"}, rec.Logs)
}

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

func TestTerminalIsExactlyTheThreeEndStates(t *testing.T) {
	for status, want := range map[buildv2.JobStatus]bool{
		buildv2.JobStatus_JobStatusOpen:      false,
		buildv2.JobStatus_JobStatusClaimed:   false,
		buildv2.JobStatus_JobStatusSuccess:   true,
		buildv2.JobStatus_JobStatusFailed:    true,
		buildv2.JobStatus_JobStatusCancelled: true,
	} {
		rec := jobRecord{Status: int32(status)}
		require.Equal(t, want, rec.terminal(), "status %v", status)
		require.Equal(t, status, rec.status())
	}
}

func TestBuildsRowsAndTheQueuesRowsCannotOverlap(t *testing.T) {
	require.True(t, strings.HasPrefix(eventPrefix("x"), "/"+rootSegment+"/"))
	require.False(t, strings.HasPrefix(eventPrefix("x"), "queue/"))
	require.False(t, strings.HasPrefix(openRoot(), "queue/"))
}
