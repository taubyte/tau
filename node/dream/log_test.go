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

	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
)

// log_test is `taubyte:log/records` end to end, and it is the FIRST thing that
// runs the kind at all: node/log.go had unit coverage over a cluster and a kvdb
// the test itself constructed, pkg/invoke/wasm had the ABI over a fake Logs, and
// between the two nothing ever asked whether a log an author DECLARES is a log a
// guest reaches. Everything in the middle — the type falling out of the block
// that was written, the settings the worker copies onto the grant, the tier
// picking the log interface out of a kind it shares with a queue — existed only
// as an argument until this file compiled the yaml.
//
// AND IT IS AUTHORED RATHER THAN TRANSCRIBED, for project_test's reason and for
// one of this kind's own: `type: log` is never written down. It is the name of
// the block, materialised by the compiler, and it is the single field that
// decides whether the tier hands this grant to Logs or to Queues
// (pkg/invoke/wasm/log.go:98). A hand-built object would have stated that field
// directly and proven nothing about the only thing that produces it.

// logLine is one record as the fixture renders it — `cursor:at:data`, records
// joined by newlines (pkg/invoke/wasm/fixtures/cacher/src/lib.rs:596-618).
type logLine struct {
	cursor string
	at     int64
	data   string
}

// logLinesOf parses what a read answered.
//
// THE CURSOR IS CARRIED THROUGH AND NEVER SPELLED. A cursor is opaque by
// contract — node/log.go base64s a position precisely so nobody assembles one —
// so a test that wrote `MA` would be asserting the encoding rather than the
// promise, and would keep passing if every record came back under the same
// cursor. What is checked below is that the cursor a record reads back under is
// the one its own append was told, which is the claim a reader actually rests on.
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

// onLimited is onCarrying with a page size, and it is here rather than beside
// its sibling because only a log needs one.
//
// `limit` IS HALF OF read's SIGNATURE (pkg/invoke/wasm/log.go:174) and every
// other request in this package sends no headers at all, so a u32 that never
// left zero would make the paging arm of node/log.go:206-209 unreachable from
// any test in the tree. The header is the fixture's own way of carrying it —
// the interface takes it as an argument — so this says the same thing the
// fixture reads.
func (r *running) onLimited(ctx context.Context, at specs.Address, path string, limit int) (*invoke.Response, error) {
	return r.node.Worker().Invoke(ctx, at, &invoke.Request{
		Method:  http.MethodPost,
		Path:    path,
		Headers: map[string][]string{"x-taubyte-limit": {strconv.Itoa(limit)}},
	})
}

// TestRecordsReadBackInTheOrderTheyWereAppended — the whole claim of the kind,
// which the vocabulary states in one line: nothing rewrites what is already
// there (definition.go:1279).
//
// ONE NODE, for queue_test's reason: what is under test here is the order raft
// assigns and the bytes the store gives back, and a single-node universe forms
// the group through the same bootstrap a fleet does. Adding nodes to THIS test
// would add placement and propagation to the list of things an ordering bug
// could hide behind. The crossing is its own test below, where a failure means
// one thing.
//
// THE HOST IS INERT. deploy names one because a register keeps its pointers by
// fqdn, but every request below goes to the worker directly — the project
// declares no zone and no gateway.
func TestRecordsReadBackInTheOrderTheyWereAppended(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	r := deploy(t, "with/sync/log", "audit.example.com", cacherWASM(t))

	// THE LOG'S CLOCK IS THE LOG'S, not the appender's (node/log.go:143), and
	// this is the floor a record's `at` has to sit above. Read before anything
	// is written so that a stamp of zero — a record whose clock never travelled
	// — fails rather than passing as "some time before now".
	since := time.Now().Unix()

	written := []string{"one", "two", "three"}
	cursors := make([]string, 0, len(written))

	// THE FIRST APPEND IS RETRIED, because a log's order is raft's and raft
	// forms in the background: node/log.go:122 refuses with ErrNotWritable
	// until this node holds the leadership, exactly as the queue's `send` is
	// told `unavailable`. That refusal IS the contract — the arm exists so a
	// caller can come back — so a test that appended once would be testing a
	// cloud that had already been running.
	//
	// THE BUDGET IS THE FORMING ITSELF, PRICED — see formingBudget.
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

	// AND THE REST ARE NOT, because by this line the group has a leader and the
	// send above proved it. An append that is refused here is a regression and
	// not a cold universe, so it is reported at once rather than waited out.
	for _, record := range written[1:] {
		res, err := r.onCarrying(t.Context(), target(), http.MethodPost, "/append/audit/", record)
		require.NoError(t, err)
		require.Equal(t, 200, res.Status, "appending %q: %s", record, res.Body)
		cursors = append(cursors, string(res.Body))
	}

	// NO TWO RECORDS LAND AT ONE POSITION. This is the whole of what raft is
	// there for — node/log.go reads the head under a barrier and re-reads it
	// after writing — and it is stated here because every assertion below pairs
	// a record with a cursor, and three identical cursors would satisfy that
	// pairing while meaning the log had one record in it.
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

	// THIS IS THE SMALLEST ASSERTION THAT FAILS IF THE LOG IS BROKEN. Order,
	// payload and position are checked as one fact per record: a dropped record
	// shortens the answer, a reordered one puts the wrong data beside a cursor,
	// and a record served from a position raft did not name reads back under a
	// cursor no append was ever told.
	got := logLinesOf(t, from.Body)
	require.Len(t, got, len(written), "the log did not answer with every record: %s", from.Body)
	for i, want := range written {
		require.Equal(t, want, got[i].data, "the record at position %d", i)
		require.Equal(t, cursors[i], got[i].cursor, "the cursor %q was appended at is not the one it reads back under", want)
		require.GreaterOrEqual(t, got[i].at, since, "the record %q is stamped before the test started", want)
		require.LessOrEqual(t, got[i].at, time.Now().Unix(), "the record %q is stamped in the future", want)
	}

	// A CURSOR IS A POSITION AND NOT A CONSTANT, which is the one thing a
	// caller that keeps its place depends on. Reading after the first record
	// answers the two that follow it and not the first — `after, not at`
	// (node/log.go:474) — so a cursor that always meant "the start" would be
	// caught here and nowhere above.
	after, err := r.onCarrying(t.Context(), target(), http.MethodPost, "/read/audit/"+cursors[0], "")
	require.NoError(t, err)
	require.Equal(t, 200, after.Status, "reading after the first record: %s", after.Body)

	tail := logLinesOf(t, after.Body)
	require.Len(t, tail, len(written)-1, "reading after the first record answered: %s", after.Body)
	for i, want := range written[1:] {
		require.Equal(t, want, tail[i].data)
		require.Equal(t, cursors[i+1], tail[i].cursor)
	}

	// AND NOTHING REWRITES WHAT IS ALREADY THERE. A fourth record lands at its
	// own position and the three before it read back byte for byte, cursors and
	// clocks included. This is the kind's entire promise and it is the one
	// claim no assertion above makes: a log that quietly reused a position, or
	// re-stamped what it already held, satisfies every line up to here.
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

	// AND A LIMIT IS HONOURED. Every read above asked for zero, which is the
	// arm meaning "the default page", so nothing yet has crossed the ABI with a
	// page size on it. Asking for two of the four answers the two OLDEST: a
	// page is taken from the cursor FORWARD, which is the direction a reader
	// catching up depends on and the opposite of what a log renders for a human.
	paged, err := r.onLimited(t.Context(), target(), "/read/audit/", 2)
	require.NoError(t, err)
	require.Equal(t, 200, paged.Status, "reading a page: %s", paged.Body)

	page := logLinesOf(t, paged.Body)
	require.Len(t, page, 2, "a limit of two answered: %s", paged.Body)
	require.Equal(t, got[:2], page, "a page is not the front of the log")

	// A LOG NOBODY GRANTED IS NOT A LOG, and the refusal is named rather than
	// generic: the tier resolves what the guest asked for against the access
	// block the project declared, so a label outside it comes back as
	// `no-such-log` and not as a denial, an outage, or an empty answer.
	//
	// THE INSTANCE HAS TO SURVIVE IT. A guest that named something it was not
	// granted made a mistake; it did not trap. If the refusal came back as a
	// killed instance the arm would be indistinguishable from a crash, so the
	// read below is part of this assertion rather than a separate one.
	refused, err := r.onCarrying(t.Context(), target(), http.MethodPost, "/append/somebody-elses/", "ship it")
	require.NoError(t, err, "the instance died rather than refusing")
	require.Equal(t, 403, refused.Status)
	require.Equal(t, "no-such-log:somebody-elses", string(refused.Body))

	alive, err := r.onCarrying(t.Context(), target(), http.MethodPost, "/read/audit/", "")
	require.NoError(t, err)
	require.Equal(t, 200, alive.Status, "the guest could not be asked anything after the refusal: %s", alive.Body)
	require.Len(t, logLinesOf(t, alive.Body), len(written)+1, "the refused append wrote something")
}

// TestARecordAppendedOnOneNodeIsReadOnAnother. A log is TWO stores and only one
// of them is raft: the head and the position index are replicated by the
// cluster, and the RECORDS go in the node's kvdb, because a log's size is
// unbounded by design and pkg/raft's FSM snapshots the whole of its map
// (node/log.go:26-37).
//
// SO THE KIND'S DESIGN IS A CLAIM ABOUT TWO MACHINES, and the test above —
// deliberately one node — is the one shape that cannot make it: on a single
// node both halves are the same process, so a log that kept its records
// somewhere that never replicates reads back perfectly. What is proven here is
// that a reader on a node that NEVER TOOK THE APPEND resolves the position
// through its own cluster and finds the bytes in its own store.
//
// THE WRITER IS DISCOVERED AND NOT ASSUMED. Appends are ordered by the node
// holding the log's leadership and refused by name anywhere else
// (node/log.go:122-124); which of the two won that election is raft's business
// and a test that picked one would be right half the time.
func TestARecordAppendedOnOneNodeIsReadOnAnother(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	cloud := deployFleet(t, 2, "with/sync/log", "audit.example.com", cacherWASM(t))

	// A LOOP AND NOT require.Eventually, because what this waits for is not a
	// condition but an ANSWER — which node took the append, and the cursor it
	// was told. Eventually runs its condition on another goroutine, so carrying
	// either of those back out of one would be a race this test introduced
	// itself.
	//
	// THE BUDGET IS THE FORMING ITSELF, PRICED — see formingBudget. Until the
	// queue namespace's group has a leader BOTH nodes refuse, exactly as the
	// single-node append is refused until its own group forms.
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

	// READ ON THE NODE THAT NEVER TOOK IT, through ITS guest — so the compiled
	// project reached it, its own tier started the component, its own cluster
	// applied the entry naming the position, and its own store held the bytes.
	// Eventually, because the record crosses in a CRDT and eventual is what
	// that store promises.
	require.Eventually(t, func() bool {
		res, err := reader.onCarrying(t.Context(), target(), http.MethodPost, "/read/audit/", "")
		return err == nil && res.Status == 200 && strings.Contains(string(res.Body), ":crosses")
	}, 60*time.Second, 500*time.Millisecond, "a record never crossed the swarm")

	// AND IT CROSSED WHOLE. The wait above is satisfied by the payload alone,
	// so the record is read once more here, on this goroutine, and paired with
	// the position its append was told: a record served under some other cursor
	// would send a reader that keeps its place back to the wrong one.
	res, err := reader.onCarrying(t.Context(), target(), http.MethodPost, "/read/audit/", "")
	require.NoError(t, err)
	require.Equal(t, 200, res.Status, "reading the log on the node that never took the append: %s", res.Body)

	got := logLinesOf(t, res.Body)
	require.Len(t, got, 1, "the second node answered with: %s", res.Body)
	require.Equal(t, "crosses", got[0].data)
	require.Equal(t, cursor, got[0].cursor, "the record reads back under a position its append was never told")
}
