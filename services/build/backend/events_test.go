package backend

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/peer"
	pkgkvdb "github.com/taubyte/tau/pkg/kvdb"
	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
)

// at numbers events in the order they are listed.
func eventsAt(kinds ...func(*buildv2.JobEvent)) []*buildv2.JobEvent {
	out := make([]*buildv2.JobEvent, 0, len(kinds))
	for i, kind := range kinds {
		ev := &buildv2.JobEvent{At: int64(i+1) * int64(time.Second), Writer: "w"}
		kind(ev)
		out = append(out, ev)
	}
	return out
}

func created(env string) func(*buildv2.JobEvent) {
	return func(ev *buildv2.JobEvent) {
		ev.Kind = &buildv2.JobEvent_Created{Created: &buildv2.JobCreated{
			RepositoryKey: "github.com/acme/pay", Ref: "refs/heads/main", CommitSha: "c0ffee",
			ProjectId: "shop", Environment: env, CreatedAt: 1,
		}}
	}
}

func claimed(attempt uint32) func(*buildv2.JobEvent) {
	return func(ev *buildv2.JobEvent) {
		ev.Kind = &buildv2.JobEvent_Claimed{Claimed: &buildv2.JobClaimed{Attempt: attempt, Holder: "builder", ClaimedUntil: 99}}
	}
}

func settled(attempt uint32, status buildv2.JobStatus, reason string) func(*buildv2.JobEvent) {
	return func(ev *buildv2.JobEvent) {
		ev.Kind = &buildv2.JobEvent_Settled{Settled: &buildv2.JobSettled{
			Attempt: attempt, Status: status, Reason: reason, Log: "bafylog",
			Assets: map[string]string{"prod/res": "bafyart"},
		}}
	}
}

func cancelled(ev *buildv2.JobEvent) {
	ev.Kind = &buildv2.JobEvent_Cancelled{Cancelled: &buildv2.JobCancelled{Reason: "cancelled"}}
}

func retried(ev *buildv2.JobEvent) {
	ev.Kind = &buildv2.JobEvent_Retried{Retried: &buildv2.JobRetried{}}
}

func logged(attempt uint32) func(*buildv2.JobEvent) {
	return func(ev *buildv2.JobEvent) {
		ev.Kind = &buildv2.JobEvent_Logged{Logged: &buildv2.JobLogged{Attempt: attempt, Log: "bafylate"}}
	}
}

func TestAJobIsTheFoldOfItsEvents(t *testing.T) {
	rec, found := fold("j", eventsAt(created("prod"), claimed(1), settled(1, buildv2.JobStatus_JobStatusSuccess, "")))
	require.True(t, found)
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, rec.status())
	require.Equal(t, uint32(1), rec.Attempt)
	require.Equal(t, "prod", rec.Environment)
	require.Equal(t, "github.com/acme/pay", rec.RepositoryKey)
	require.Equal(t, map[string]string{stamp(1): "bafylog"}, rec.Logs)
	require.Equal(t, map[string]string{"prod/res": "bafyart"}, rec.Assets)
	require.Empty(t, rec.Holder, "an ended attempt has no holder")
	require.Equal(t, int64(3), rec.UpdatedAt)

	_, found = fold("j", nil)
	require.False(t, found)
}

// THE FIRST TERMINAL EVENT OF AN ATTEMPT WINS, on every node that folds the same
// events: a cancel before a settle ends the job and the settle is nothing.
func TestTheFirstEndingOfAnAttemptWins(t *testing.T) {
	rec, _ := fold("j", eventsAt(created("prod"), claimed(1), cancelled, settled(1, buildv2.JobStatus_JobStatusSuccess, "")))
	require.Equal(t, buildv2.JobStatus_JobStatusCancelled, rec.status())
	require.Empty(t, rec.Assets, "a settle after the cancel built nothing that counts")

	rec, _ = fold("j", eventsAt(created("prod"), claimed(1), settled(1, buildv2.JobStatus_JobStatusFailed, "boom"), cancelled))
	require.Equal(t, buildv2.JobStatus_JobStatusFailed, rec.status(), "a cancel of an ended job is nothing")
	require.Equal(t, "boom", rec.Reason)
}

// A RETRY REOPENS, AND A STALE ATTEMPT CANNOT END THE NEW ONE: the old attempt's
// late settle is ignored once the next attempt is claimed.
func TestARetryReopensAndAStaleAttemptCannotEndIt(t *testing.T) {
	rec, _ := fold("j", eventsAt(
		created("prod"), claimed(1), settled(1, buildv2.JobStatus_JobStatusFailed, "boom"),
		retried, claimed(2), settled(1, buildv2.JobStatus_JobStatusSuccess, ""),
	))
	require.Equal(t, buildv2.JobStatus_JobStatusClaimed, rec.status())
	require.Equal(t, uint32(2), rec.Attempt)
	require.Empty(t, rec.Reason, "a reopened job carries no old reason")

	rec, _ = fold("j", eventsAt(created("prod"), claimed(1), retried))
	require.Equal(t, buildv2.JobStatus_JobStatusClaimed, rec.status(), "a running job is not reopened")

	// A CLAIM OF A HIGHER ATTEMPT TAKES A CLAIMED JOB, one of a lower does not.
	rec, _ = fold("j", eventsAt(created("prod"), claimed(2), claimed(1)))
	require.Equal(t, uint32(2), rec.Attempt)
}

func TestWhatIsNotAJobYetIsNotFolded(t *testing.T) {
	// A claim, a cancel, a log or a retry of nothing is nothing.
	_, found := fold("j", eventsAt(claimed(1), cancelled, logged(1), retried))
	require.False(t, found)

	// A SECOND CREATED IS THE SAME PUSH ARRIVING TWICE.
	rec, _ := fold("j", eventsAt(created("prod"), created("staging")))
	require.Equal(t, "prod", rec.Environment)

	// A SETTLE WITH NO CREATED is a job whose record never reached its builder: it
	// exists as that ending alone, so a caller can find it and retry it.
	rec, found = fold("j", eventsAt(settled(1, buildv2.JobStatus_JobStatusFailed, "no record")))
	require.True(t, found)
	require.Equal(t, buildv2.JobStatus_JobStatusFailed, rec.status())
	require.Equal(t, "no record", rec.Reason)

	// AND A CREATED THAT ARRIVES AFTER IT gives the job its push without reopening
	// it, so a retry has something to build.
	rec, found = fold("j", eventsAt(settled(1, buildv2.JobStatus_JobStatusFailed, "no record"), created("prod")))
	require.True(t, found)
	require.Equal(t, buildv2.JobStatus_JobStatusFailed, rec.status())
	require.Equal(t, "prod", rec.Environment)
	require.Equal(t, "github.com/acme/pay", rec.RepositoryKey)

	// A LATE LOG IS FILED under its attempt, whatever the job is doing.
	rec, _ = fold("j", eventsAt(created("prod"), claimed(1), cancelled, logged(1)))
	require.Equal(t, map[string]string{stamp(1): "bafylate"}, rec.Logs)

	// And an event of a kind this build does not know changes nothing.
	unknown := &buildv2.JobEvent{At: 9}
	rec, _ = fold("j", append(eventsAt(created("prod")), unknown))
	require.Equal(t, int64(1), rec.UpdatedAt)
}

// frozen is a clock that never moves, so the journal must keep its own instants
// apart.
func frozen() time.Time { return time.Unix(100, 0) }

func journalOver(t *testing.T, db kvdb.Instance) *journal {
	t.Helper()
	return newJournal(db, "builder", frozen)
}

// besideRow is one key a test writes in an event's delta, named off the event's
// instant and writer.
type besideRow struct {
	key   func(at int64, writer string) string
	value []byte
}

// appendEvent writes one event of id and the rows beside it as one delta, the way a
// section's entry does.
func appendEvent(ctx context.Context, j *journal, id string, ev *buildv2.JobEvent, rows ...besideRow) (int64, error) {
	ev.At, ev.Writer = j.at(), j.writer
	raw, err := ev.MarshalVT()
	if err != nil {
		return 0, err
	}
	b, err := j.db.Batch(ctx)
	if err != nil {
		return 0, err
	}
	if err := b.Put(eventKey(id, ev.At, ev.Writer), raw); err != nil {
		return 0, err
	}
	for _, r := range rows {
		if err := b.Put(r.key(ev.At, ev.Writer), r.value); err != nil {
			return 0, err
		}
	}
	return ev.At, b.Commit()
}

func createdIn(project, environment string) *buildv2.JobEvent {
	return &buildv2.JobEvent{Kind: &buildv2.JobEvent_Created{Created: &buildv2.JobCreated{ProjectId: project, Environment: environment}}}
}

func claimedAt(attempt uint32) *buildv2.JobEvent {
	return &buildv2.JobEvent{Kind: &buildv2.JobEvent_Claimed{Claimed: &buildv2.JobClaimed{Attempt: attempt}}}
}

func succeededAt(attempt uint32) *buildv2.JobEvent {
	return &buildv2.JobEvent{Kind: &buildv2.JobEvent_Settled{Settled: &buildv2.JobSettled{Attempt: attempt, Status: buildv2.JobStatus_JobStatusSuccess}}}
}

// success is one job's successful build: what it built and from which commit.
type success struct{ job, cid, sha string }

func TestAJournalAppendsAndFoldsBack(t *testing.T) {
	for name, open := range map[string]func(t *testing.T) kvdb.Instance{
		"mock": func(t *testing.T) kvdb.Instance {
			db, err := kvdbmock.New().New(nil, "build", 0)
			require.NoError(t, err)
			return db
		},
		// THE REAL STORE lists keys back as datastore keys, which is why the
		// journal's own keys start with "/".
		"kvdb": func(t *testing.T) kvdb.Instance {
			p := peer.Mock(t.Context())
			t.Cleanup(p.Close)
			db, err := pkgkvdb.Open(p, "build")
			require.NoError(t, err)
			t.Cleanup(db.Close)
			return db
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			j := journalOver(t, open(t))

			first, err := appendEvent(ctx, j, "job", createdIn("shop", "prod"),
				besideRow{key: func(int64, string) string { return projectRow("shop", "job") }})
			require.NoError(t, err)
			second, err := appendEvent(ctx, j, "job", claimedAt(1))
			require.NoError(t, err)
			require.Greater(t, second, first, "a frozen clock still gives this writer two instants")

			rec, found, err := j.read(ctx, "job")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, buildv2.JobStatus_JobStatusClaimed, rec.status())
			require.Equal(t, map[string]bool{eventName(first, "builder"): true, eventName(second, "builder"): true}, rec.seen,
				"the fold says which events it read, and that both applied")

			rows, err := j.db.List(ctx, projectRows("shop"))
			require.NoError(t, err)
			require.Len(t, rows, 1, "the row went in the event's delta")
			id, err := lastSegmentOf(rows[0])
			require.NoError(t, err)
			require.Equal(t, "job", id)

			_, found, err = j.read(ctx, "nobody")
			require.NoError(t, err)
			require.False(t, found)

			// WHAT RUNS IS THE NEWEST APPLIED SUCCESS'S.
			_, _, ok, err := j.live(ctx, "shop", "prod", "res")
			require.NoError(t, err)
			require.False(t, ok, "nothing has built it")
			_, ok, err = j.anchor(ctx, "github.com/acme/pay", "refs/heads/main", "prod")
			require.NoError(t, err)
			require.False(t, ok)

			succeed := func(job, cid, sha string) {
				t.Helper()
				value, err := liveValue(cid)
				require.NoError(t, err)
				_, err = appendEvent(ctx, j, job, succeededAt(1),
					besideRow{key: func(at int64, writer string) string { return liveRow("shop", "prod", "res", at, job, writer) }, value: value},
					besideRow{key: func(at int64, writer string) string {
						return anchorRow("github.com/acme/pay", "refs/heads/main", "prod", at, job, writer)
					}, value: []byte(sha)})
				require.NoError(t, err)
			}
			for _, built := range []success{{"one", "bafyone", "sha-one"}, {"two", "bafytwo", "sha-two"}} {
				_, err = appendEvent(ctx, j, built.job, createdIn("shop", "prod"))
				require.NoError(t, err)
				_, err = appendEvent(ctx, j, built.job, claimedAt(1))
				require.NoError(t, err)
				succeed(built.job, built.cid, built.sha)
			}

			// A SUCCESS A CANCEL ENDED FIRST wrote its rows, newest of all, and its
			// settle is not in the fold: the rows are visible and do not count.
			_, err = appendEvent(ctx, j, "lost", createdIn("shop", "prod"))
			require.NoError(t, err)
			_, err = appendEvent(ctx, j, "lost", claimedAt(1))
			require.NoError(t, err)
			_, err = appendEvent(ctx, j, "lost", &buildv2.JobEvent{Kind: &buildv2.JobEvent_Cancelled{Cancelled: &buildv2.JobCancelled{}}})
			require.NoError(t, err)
			succeed("lost", "bafylost", "sha-lost")

			cid, from, ok, err := j.live(ctx, "shop", "prod", "res")
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, "bafytwo", cid)
			require.Equal(t, "two", from)
			sha, ok, err := j.anchor(ctx, "github.com/acme/pay", "refs/heads/main", "prod")
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, "sha-two", sha)
		})
	}
}

func TestAJournalThatCannotWriteOrReadSaysSo(t *testing.T) {
	ctx := context.Background()
	db, err := kvdbmock.New().New(nil, "build", 0)
	require.NoError(t, err)
	j := journalOver(t, db)
	require.NoError(t, db.Put(ctx, eventKey("job", 1, "w"), []byte{0xff, 0xff}))
	_, _, err = j.read(ctx, "job")
	require.Error(t, err, "an event that does not decode is not an empty job")

	// A LIVE ROW of a job whose events do not decode is not skipped as unapplied.
	require.NoError(t, db.Put(ctx, liveRow("shop", "prod", "res", 1, "job", "w"), nil))
	_, _, _, err = j.live(ctx, "shop", "prod", "res")
	require.Error(t, err)

	// And one that does not decode, beside an applied settle, is reported.
	good, err := appendEvent(ctx, j, "good", createdIn("shop", "prod"))
	require.NoError(t, err)
	_, err = appendEvent(ctx, j, "good", claimedAt(1))
	require.NoError(t, err)
	settled, err := appendEvent(ctx, j, "good", succeededAt(1),
		besideRow{key: func(at int64, writer string) string { return liveRow("shop", "prod", "other", at, "good", writer) }, value: []byte{0xff, 0xff}})
	require.NoError(t, err)
	require.Greater(t, settled, good)
	_, _, _, err = j.live(ctx, "shop", "prod", "other")
	require.Error(t, err)

	require.NoError(t, db.Put(ctx, liveRows("shop", "prod", "bad")+"!!!", nil))
	_, _, _, err = j.live(ctx, "shop", "prod", "bad")
	require.Error(t, err, "a row that names no event is not skipped either")

	db.Close()
	_, _, err = j.read(ctx, "job")
	require.Error(t, err)
	_, _, _, err = j.live(ctx, "shop", "prod", "res")
	require.Error(t, err)
}
