package wasm_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// log_test drives `taubyte:log/records` through a REAL COMPONENT: a record
// crosses as bytes, comes back with the cursor the host issued, and the cursor
// goes back in to read what follows.

// memLog is a Logs over a slice. It is deliberately dense and ordered, because
// what the ABI has to carry is a cursor a caller hands back — not a number.
type memLog struct {
	mu      sync.Mutex
	records map[string][]wasm.Record
	err     error
}

func newLog() *memLog { return &memLog{records: map[string][]wasm.Record{}} }

func (m *memLog) Append(_ context.Context, grant invoke.Grant, data []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return "", m.err
	}
	at := len(m.records[grant.Handle])
	cursor := fmt.Sprintf("c%d", at)
	m.records[grant.Handle] = append(m.records[grant.Handle], wasm.Record{
		Cursor: cursor, At: 1_700_000_000 + int64(at), Data: data,
	})
	return cursor, nil
}

func (m *memLog) Read(_ context.Context, grant invoke.Grant, after string, limit uint32) ([]wasm.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return nil, m.err
	}
	held := m.records[grant.Handle]

	from := 0
	if after != "" {
		var at int
		if _, err := fmt.Sscanf(after, "c%d", &at); err != nil {
			return nil, fmt.Errorf("%w: %s", wasm.ErrBadCursor, after)
		}
		from = at + 1
	}
	if from > len(held) {
		return nil, nil
	}
	out := held[from:]
	if limit > 0 && int(limit) < len(out) {
		out = out[:limit]
	}
	return out, nil
}

func logGrant(label, handle string, ops ...string) []invoke.Grant {
	return []invoke.Grant{{
		Kind: "sync", Label: label, Handle: handle, Ops: ops,
		Settings: map[string]string{"type": "log"},
	}}
}

// logging is a tier with one log and a workload granted both verbs.
func logging(t *testing.T, ops ...string) (invoke.Instance, *memLog) {
	t.Helper()

	if len(ops) == 0 {
		ops = []string{"append", "read"}
	}
	backing := newLog()
	instance := start(t, tier(t, cacher(t), wasm.WithLogs(backing)), invoke.Spec{
		Name: "fn", Reaches: logGrant("audit", "shop/prod/QmAudit", ops...),
	})
	return instance, backing
}

// TestARecordGoesOnTheEndAndComesBackInOrder — the whole claim, through
// generated bindings, with the cursor the host issued going back in.
func TestALogAppendsAndReadsForward(t *testing.T) {
	instance, _ := logging(t)

	var first string
	for _, line := range []string{"one", "two", "three"} {
		res := call(t, instance, "/append/audit/", []byte(line))
		require.Equal(t, 200, res.Status, string(res.Body))
		require.NotEmpty(t, res.Body, "an append answers the cursor it landed at")
		if first == "" {
			first = string(res.Body)
		}
	}

	// From the beginning: every record, oldest first.
	all := call(t, instance, "/read/audit/", nil)
	require.Equal(t, 200, all.Status, string(all.Body))
	lines := strings.Split(string(all.Body), "\n")
	require.Len(t, lines, 3)
	require.True(t, strings.HasSuffix(lines[0], ":one"), "got %q", lines[0])
	require.True(t, strings.HasSuffix(lines[2], ":three"), "got %q", lines[2])

	// AND A CURSOR READS WHAT FOLLOWS IT, not what it names — which is what
	// makes a reader able to resume without seeing a record twice.
	after := call(t, instance, "/read/audit/"+first, nil)
	require.Equal(t, 200, after.Status, string(after.Body))
	require.NotContains(t, string(after.Body), ":one")
	require.Contains(t, string(after.Body), ":two")
}

// TestREADINGCONSUMESNOTHING, which is the whole difference from a queue: two
// reads from the beginning answer the same records.
func TestReadingALogConsumesNothing(t *testing.T) {
	instance, _ := logging(t)
	require.Equal(t, 200, call(t, instance, "/append/audit/", []byte("kept")).Status)

	first := call(t, instance, "/read/audit/", nil)
	second := call(t, instance, "/read/audit/", nil)
	require.Equal(t, string(first.Body), string(second.Body))
	require.Contains(t, string(first.Body), ":kept")
}

// TestALIMITBoundsThePage, so a reader of a long log is not handed all of it.
func TestALogPageIsBounded(t *testing.T) {
	instance, _ := logging(t)
	for _, line := range []string{"a", "b", "c", "d"} {
		require.Equal(t, 200, call(t, instance, "/append/audit/", []byte(line)).Status)
	}

	res := callWith(t, instance, "/read/audit/", nil, map[string]string{"x-taubyte-limit": "2"})
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Len(t, strings.Split(string(res.Body), "\n"), 2)
}

// ── the grant is the authorization ───────────────────────────────────────────

// TestAPPENDIsAWriteAndREADIsARead, checked separately and before the backing
// is touched.
func TestALogGrantWithoutAppendCannotWrite(t *testing.T) {
	instance, backing := logging(t, "read")

	res := call(t, instance, "/append/audit/", []byte("x"))
	require.Equal(t, 403, res.Status)
	require.Equal(t, "denied:audit cannot append", string(res.Body))

	backing.mu.Lock()
	defer backing.mu.Unlock()
	require.Empty(t, backing.records, "and nothing was written to find that out")
}

func TestAnUndeclaredLogIsRefusedByName(t *testing.T) {
	instance, _ := logging(t)

	res := call(t, instance, "/append/somebody-elses/", []byte("x"))
	require.Equal(t, 403, res.Status)
	require.Equal(t, "no-such-log:somebody-elses", string(res.Body))
}

// TestAQUEUEGrantIsNotALogGrant. `sync` is one kind with two types, and the
// type selects the interface — so a queue's grant is invisible here, exactly as
// a kv database's is to the sql interface.
func TestAQueueGrantIsNotVisibleToTheLogInterface(t *testing.T) {
	// The tier is given both backings, because a queue grant with no queue
	// behind it is refused at Start — which would prove the wrong thing.
	instance := start(t,
		tier(t, cacher(t), wasm.WithLogs(newLog()), wasm.WithQueues(&work{})),
		invoke.Spec{
			Name: "fn",
			Reaches: []invoke.Grant{{
				Kind: "sync", Label: "jobs", Handle: "h", Ops: []string{"append", "read"},
				Settings: map[string]string{"type": "queue"},
			}},
		})

	res := call(t, instance, "/append/jobs/", []byte("x"))
	require.Equal(t, 403, res.Status)
	require.Equal(t, "no-such-log:jobs", string(res.Body))
}

// TestANodeWithNothingToAppendToRefusesTheWorkload, the shape every capability
// keeps: a granted capability with no backing is a composition fault.
func TestALogGrantNeedsABacking(t *testing.T) {
	_, err := tier(t, cacher(t)).Start(t.Context(), invoke.Spec{
		Name: "fn", Artifact: "bafy",
		Reaches: logGrant("audit", "h", "append"),
	})
	require.ErrorIs(t, err, wasm.ErrNoLogs)
}

// TestTHEIMPORTIsRegisteredEvenWithNoGrant. An unprovided import is a TRAP that
// takes the instance with it.
func TestTheLogImportExistsWithoutAGrant(t *testing.T) {
	instance := start(t, tier(t, cacher(t)), invoke.Spec{Name: "fn"})

	res := call(t, instance, "/append/anything/", []byte("x"))
	require.Equal(t, 403, res.Status)
	require.Equal(t, "no-such-log:anything", string(res.Body))
}

// TestABADCursorIsItsOwnArm, so a caller starts over rather than retrying a
// position this log can never answer.
func TestABadCursorIsNotAnOutage(t *testing.T) {
	instance, _ := logging(t)

	res := call(t, instance, "/read/audit/not-a-cursor", nil)
	require.Equal(t, 403, res.Status)
	require.True(t, strings.HasPrefix(string(res.Body), "bad-cursor:"), "got %q", res.Body)
}
