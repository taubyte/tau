//go:build dreaming

package dream_test

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	nodeDream "github.com/taubyte/tau/instance/dream"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
)

// `taubyte:log/records` end to end from compiled yaml: `type: log` is never written,
// only materialised from the block name, and it decides whether the tier hands the
// grant to Logs or Queues (pkg/invoke/wasm/log.go).

// logLine is one record as the fixture renders it — `cursor:at:data`, records
// joined by newlines (pkg/invoke/wasm/fixtures/cacher/src/lib.rs:596-618).
type logLine struct {
	cursor string
	at     int64
	data   string
}

// logLinesOf parses what a read answered. Cursors are opaque, so they are carried
// through and compared with what each append was told, never spelled.
func logLinesOf(t *testing.T, body []byte) []logLine {
	t.Helper()

	if len(body) == 0 {
		return nil
	}

	raw := strings.Split(string(body), "\n")
	out := make([]logLine, 0, len(raw))
	for _, line := range raw {
		parts := strings.SplitN(line, ":", 3)
		require.Len(t, parts, 3, "a record came back rendered as %q", line)

		at, err := strconv.ParseInt(parts[1], 10, 64)
		require.NoError(t, err, "the clock on %q is not a unix second", line)

		out = append(out, logLine{cursor: parts[0], at: at, data: parts[2]})
	}
	return out
}

// onLimited is onCarrying with a page size, sent as the fixture's header, so the
// log's paging arm is reachable.
func (r *running) onLimited(ctx context.Context, at specs.Address, path string, limit int) (*invoke.Response, error) {
	return r.node.Worker().Invoke(ctx, at, &invoke.Request{
		Method:  http.MethodPost,
		Path:    path,
		Headers: map[string][]string{"x-taubyte-limit": {strconv.Itoa(limit)}},
	})
}

func TestRecordsReadBackInTheOrderTheyWereAppended(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	r := deploy(t, "with/sync/log", "audit.example.com", cacherWASM(t))

	// The log's clock is the log's: a record's `at` must be at or after this.
	since := time.Now().Unix()

	written := []string{"one", "two", "three"}
	cursors := make([]string, 0, len(written))

	// The first append is retried: until this node leads the log's raft group it
	// answers ErrNotWritable. Budget: formingBudget.
	var last string
	require.Eventually(t, func() bool {
		res, err := r.onCarrying(t.Context(), target(), http.MethodPost, "/append/audit/", written[0])
		if err != nil {
			last = err.Error()
			return false
		}
		last = string(res.Body)
		if res.Status != 200 {
			return false
		}
		cursors = append(cursors, string(res.Body))
		return true
	}, formingBudget, time.Second, "the guest could never append anything: %s", &lastOf{&last})

	// The rest are not retried: the group now has a leader.
	for _, record := range written[1:] {
		res, err := r.onCarrying(t.Context(), target(), http.MethodPost, "/append/audit/", record)
		require.NoError(t, err)
		require.Equal(t, 200, res.Status, "appending %q: %s", record, res.Body)
		cursors = append(cursors, string(res.Body))
	}

	// No two records share a position.
	require.Len(t, cursors, len(written))
	held := map[string]struct{}{}
	for _, cursor := range cursors {
		held[cursor] = struct{}{}
	}
	require.Len(t, held, len(written), "two records were given the same position: %v", cursors)

	// READ FROM THE START, which for this fixture is an empty key.
	from, err := r.onCarrying(t.Context(), target(), http.MethodPost, "/read/audit/", "")
	require.NoError(t, err)
	require.Equal(t, 200, from.Status, "reading the log: %s", from.Body)

	// Order, payload and position checked together per record.
	got := logLinesOf(t, from.Body)
	require.Len(t, got, len(written), "the log did not answer with every record: %s", from.Body)
	for i, want := range written {
		require.Equal(t, want, got[i].data, "the record at position %d", i)
		require.Equal(t, cursors[i], got[i].cursor, "the cursor %q was appended at is not the one it reads back under", want)
		require.GreaterOrEqual(t, got[i].at, since, "the record %q is stamped before the test started", want)
		require.LessOrEqual(t, got[i].at, time.Now().Unix(), "the record %q is stamped in the future", want)
	}

	// A cursor is a position: reading after the first answers the records that follow
	// it.
	after, err := r.onCarrying(t.Context(), target(), http.MethodPost, "/read/audit/"+cursors[0], "")
	require.NoError(t, err)
	require.Equal(t, 200, after.Status, "reading after the first record: %s", after.Body)

	tail := logLinesOf(t, after.Body)
	require.Len(t, tail, len(written)-1, "reading after the first record answered: %s", after.Body)
	for i, want := range written[1:] {
		require.Equal(t, want, tail[i].data)
		require.Equal(t, cursors[i+1], tail[i].cursor)
	}

	// Nothing rewrites what is there: a fourth record lands at its own position and the
	// first three read back identically.
	fourth, err := r.onCarrying(t.Context(), target(), http.MethodPost, "/append/audit/", "four")
	require.NoError(t, err)
	require.Equal(t, 200, fourth.Status, "appending a fourth record: %s", fourth.Body)
	require.NotContains(t, cursors, string(fourth.Body), "the fourth record took a position that was already taken")

	again, err := r.onCarrying(t.Context(), target(), http.MethodPost, "/read/audit/", "")
	require.NoError(t, err)
	require.Equal(t, 200, again.Status, "reading the log again: %s", again.Body)

	grown := logLinesOf(t, again.Body)
	require.Len(t, grown, len(written)+1, "the fourth record did not extend the log: %s", again.Body)
	require.Equal(t, got, grown[:len(written)], "appending changed records that were already written")
	require.Equal(t, "four", grown[len(written)].data)

	// A limit is honoured, taking the oldest from the cursor forward.
	paged, err := r.onLimited(t.Context(), target(), "/read/audit/", 2)
	require.NoError(t, err)
	require.Equal(t, 200, paged.Status, "reading a page: %s", paged.Body)

	page := logLinesOf(t, paged.Body)
	require.Len(t, page, 2, "a limit of two answered: %s", paged.Body)
	require.Equal(t, got[:2], page, "a page is not the front of the log")

	// An ungranted log is refused by name (`no-such-log`), and the instance survives.
	refused, err := r.onCarrying(t.Context(), target(), http.MethodPost, "/append/somebody-elses/", "ship it")
	require.NoError(t, err, "the instance died rather than refusing")
	require.Equal(t, 403, refused.Status)
	require.Equal(t, "no-such-log:somebody-elses", string(refused.Body))

	alive, err := r.onCarrying(t.Context(), target(), http.MethodPost, "/read/audit/", "")
	require.NoError(t, err)
	require.Equal(t, 200, alive.Status, "the guest could not be asked anything after the refusal: %s", alive.Body)
	require.Len(t, logLinesOf(t, alive.Body), len(written)+1, "the refused append wrote something")
}

func TestARecordAppendedOnOneNodeIsReadOnAnother(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	cloud := deployFleet(t, 2, "with/sync/log", "audit.example.com", cacherWASM(t))

	// A loop rather than require.Eventually, since the result (which node took the
	// append, and its cursor) must come back on this goroutine. Budget: formingBudget.
	var reader *running
	var cursor, last string
	for deadline := time.Now().Add(formingBudget); time.Now().Before(deadline); {
		for i, r := range cloud {
			res, err := r.onCarrying(t.Context(), target(), http.MethodPost, "/append/audit/", "crosses")
			if err != nil {
				last = err.Error()
				continue
			}
			last = string(res.Body)
			if res.Status == 200 {
				cursor, reader = string(res.Body), cloud[1-i]
				break
			}
		}
		if reader != nil {
			break
		}
		time.Sleep(time.Second)
	}
	require.NotNil(t, reader, "no node in the fleet would take an append: %s", last)

	// Read on the node that never took it, through its own guest, tier, cluster and
	// store; eventually, as the record crosses in a CRDT.
	require.Eventually(t, func() bool {
		res, err := reader.onCarrying(t.Context(), target(), http.MethodPost, "/read/audit/", "")
		return err == nil && res.Status == 200 && strings.Contains(string(res.Body), ":crosses")
	}, 60*time.Second, 500*time.Millisecond, "a record never crossed the swarm")

	// And it crossed whole, paired with its append's cursor.
	res, err := reader.onCarrying(t.Context(), target(), http.MethodPost, "/read/audit/", "")
	require.NoError(t, err)
	require.Equal(t, 200, res.Status, "reading the log on the node that never took the append: %s", res.Body)

	got := logLinesOf(t, res.Body)
	require.Len(t, got, 1, "the second node answered with: %s", res.Body)
	require.Equal(t, "crosses", got[0].data)
	require.Equal(t, cursor, got[0].cursor, "the record reads back under a position its append was never told")
}
