package hosted

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/services/worker"
)

func TestAnAssetStoreIsEnoughToRunCode(t *testing.T) {
	// No container backend, no tier handed over — and a worker still starts, with
	// every tier that needs only the asset store.
	n, _, _ := makeNode(t, Config{Services: []string{Stats, Worker}}, withoutTiers())
	require.NotNil(t, n.Worker())

	// Both: a wasm component and a js function are what the smallest possible cloud
	// runs, and neither needs a daemon. A site's files are its gateway's to serve.
	require.ElementsMatch(t, []string{"wasm", "warp"}, tierNames(n))
}

func TestTheArtifactLookupIsCachedForTheDeployBudget(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	n, _, _ := makeNode(t,
		Config{Services: []string{Stats, Worker}, Worker: worker.Config{PointerTTL: time.Minute}},
		WithClock(func() time.Time { return now }))

	a := &artifacts{node: n.Service}
	a.remember(builtKey{project: "shop", environment: "prod", resource: "res"}, "bafyone")

	cid, err := a.Artifact(t.Context(), "shop", "prod", "function", "res")
	require.NoError(t, err)
	require.Equal(t, "bafyone", cid)

	// Past the budget the records are read again, and they hold nothing for it,
	// rather than something stale being served forever.
	now = now.Add(2 * time.Minute)
	_, err = a.Artifact(t.Context(), "shop", "prod", "function", "res")
	require.ErrorIs(t, err, ErrNotBuilt)
}

func TestAFailedArtifactLookupIsNotRemembered(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Stats, Worker}})

	a := n.built
	_, err := a.Artifact(t.Context(), "shop", "prod", "function", "res")
	require.ErrorIs(t, err, ErrNotBuilt)
	_, held := a.seen.Load(builtKey{project: "shop", environment: "prod", resource: "res"})
	require.False(t, held)
}

func TestTheDeployBudgetHasAnEffectiveValue(t *testing.T) {
	require.Equal(t, worker.DefaultPointerTTL, worker.Config{}.Pointer())
	require.Equal(t, worker.DefaultPointerTTL, worker.Config{PointerTTL: -1}.Pointer())
	require.Equal(t, time.Minute, worker.Config{PointerTTL: time.Minute}.Pointer())
}

func TestTheWorkerResolvesArtifactsThroughWhatItIsGivenElseBuildsRecords(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Stats, Worker}})
	require.Same(t, n.built, n.Artifacts(), "plm's records on its node")
	require.Equal(t, n.built, n.PLMArtifacts())

	with, _, _ := makeNode(t, Config{Services: []string{Stats, Worker}}, prebuilt())
	require.Equal(t, everythingBuilt{}, with.Artifacts(), "the lookup it was given")
	require.Same(t, with.built, with.PLMArtifacts(), "and the records still, to fall back on")
}

func TestAnInjectedTierWins(t *testing.T) {
	injected := &tier{name: "wasm"}
	n, _, _ := makeNode(t, Config{Services: []string{Stats, Worker}}, WithRuntimes(injected))

	// One wasm, and it is the injected one; js is still built, because nothing
	// else answers for it.
	require.ElementsMatch(t, []string{"oci", "wasm", "warp"}, tierNames(n))
	var wasm []invoke.Runtime
	for _, rt := range n.Tiers() {
		if rt.Name() == "wasm" {
			wasm = append(wasm, rt)
		}
	}
	require.Equal(t, []invoke.Runtime{injected}, wasm)
}

func TestAWorkloadsOutputIsAssembledIntoLines(t *testing.T) {
	var lines []string
	w := logLine(func(line string) { lines = append(lines, line) })

	// One line arriving in three writes is one line.
	for _, part := range []string{"star", "ting the ", "handler\n"} {
		n, err := w.Write([]byte(part))
		require.NoError(t, err)
		require.Equal(t, len(part), n, "a writer reports what it took, not what it emitted")
	}
	require.Equal(t, []string{"starting the handler"}, lines)

	// Two lines in one write are two.
	lines = nil
	_, err := w.Write([]byte("one\ntwo\n"))
	require.NoError(t, err)
	require.Equal(t, []string{"one", "two"}, lines)

	// A carriage return is the line ending's, not the message's.
	lines = nil
	_, err = w.Write([]byte("windows\r\n"))
	require.NoError(t, err)
	require.Equal(t, []string{"windows"}, lines)

	// A blank line is not a log entry, and an unterminated one waits rather
	// than being emitted half-written.
	lines = nil
	_, err = w.Write([]byte("\n\nstill typ"))
	require.NoError(t, err)
	require.Empty(t, lines)

	_, err = w.Write([]byte("ing\n"))
	require.NoError(t, err)
	require.Equal(t, []string{"still typing"}, lines)
}

func TestAWorkloadsStreamsAreNamedByTheWorkload(t *testing.T) {
	stdout, stderr := workloadLog(invoke.Spec{Name: "shop/prod/function/checkout"})
	require.NotNil(t, stdout)
	require.NotNil(t, stderr)
	require.NotSame(t, stdout, stderr)

	// Writing to either is safe and reports what it took.
	n, err := stdout.Write([]byte("hello\n"))
	require.NoError(t, err)
	require.Equal(t, 6, n)

	n, err = stderr.Write([]byte("something broke\n"))
	require.NoError(t, err)
	require.Equal(t, 16, n)
}
