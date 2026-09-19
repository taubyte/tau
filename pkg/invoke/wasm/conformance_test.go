package wasm

import (
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// conformance_test holds this tier's capability register to the vocabulary.
//
// # Why the register needs holding at all
//
// hostComponents() is this tier's answer to "which kinds and types do I serve",
// written by hand. The composer hands it a grant for EVERY kind on purpose, so
// that a tier can see what it cannot do — and a kind with no row was dropped
// here without a word until unserved() landed beside this.
//
// A ROW OR AN ADMISSION, and nothing in between. A capability that is not
// served yet is fine; a capability nobody noticed stopped being served is not.
// So the vocabulary is walked and every name must be one or the other.
func TestEveryCapabilityIsServedOrAdmittedUnserved(t *testing.T) {
	rows := map[string]bool{}
	for _, c := range hostComponents() {
		rows[c.name] = true
	}

	// unserved is what this tier knowingly has no host interface for, each with
	// the reason. An entry here is a decision; a name missing from both this and
	// the rows is the drift this test exists to catch.
	unserved := map[string]string{
		"volume":   "refused rather than warned - ErrNoMounts. A component is given no filesystem, and starting one without a mount it declared loses what it writes.",
		"function": "a guest invoking another workload has no world yet; what invokes across workloads today is the platform, through a queue delivery or a machine transition.",
		"worker":   "same as function: nothing here calls another workload.",
		"sandbox":  "exec/read/write against a sandbox is not a host interface this tier has.",
		"mcp":      "answered by services/worker rather than a tier - an mcp runs no engine, so there is nothing here to import.",
		"agent":    "an agent lowers to a loop and a machine; the loop is a function and what drives it is the platform, not a guest call.",
	}

	var missing []string
	for _, kind := range schema.Kinds() {
		if rows[kind] || unserved[kind] != "" {
			continue
		}
		// A TYPED KIND IS SERVED BY ITS TYPES. `database` and `sync` have no row
		// of their own because a guest meets a kv, not a database.
		switch kind {
		case "database", "sync":
			continue
		}
		missing = append(missing, kind)
	}

	for _, model := range schema.SyncTypes() {
		if !rows[model] && unserved[model] == "" {
			missing = append(missing, "sync/"+model)
		}
	}
	for model := range schema.DatabaseEngines() {
		if !rows[model] && unserved[model] == "" {
			missing = append(missing, "database/"+model)
		}
	}

	sort.Strings(missing)
	require.Empty(t, missing,
		"these are declared by the vocabulary and this tier neither serves them nor admits it does not - a guest declaring one gets no host function, no error at start, and no log line")
}

// TestACAPABILITYTHISTIERCANNOTSERVEISREPORTED, which is the other half: the
// register above says what is knowingly unserved, and this says an operator
// hears about it when a workload actually declares one.
func TestAnUnservedGrantIsWarnedAbout(t *testing.T) {
	var said []string
	r := &Runtime{warn: func(format string, args ...any) {
		said = append(said, fmt.Sprintf(format, args...))
	}}

	r.unserved(invoke.Spec{
		Name: "shop/prod/function/checkout",
		Reaches: []invoke.Grant{
			{Kind: "sandbox", Label: "scratch", Ops: []string{"exec"}},
			{Kind: "cache", Label: "orders", Ops: []string{"get"}},
		},
	})

	require.Len(t, said, 1)
	require.Contains(t, said[0], "sandbox", "the capability with no host interface is named")
	require.NotContains(t, said[0], "cache", "a capability this tier does serve is not reported")
	require.Contains(t, said[0], "shop/prod/function/checkout", "and which workload declared it")
}

// TestAMOUNTISREFUSEDRATHERTHANWARNEDABOUT — it is the one unserved capability
// that loses data instead of failing, so it must not be softened into a line in
// a log that a workload then starts past.
func TestAMountIsNotWarnedAboutBecauseItIsRefused(t *testing.T) {
	var said []string
	r := &Runtime{warn: func(format string, args ...any) {
		said = append(said, fmt.Sprintf(format, args...))
	}}

	r.unserved(invoke.Spec{
		Name:    "shop/prod/function/checkout",
		Reaches: []invoke.Grant{{Kind: invoke.MountKind, Label: "scratch", Ops: []string{"mount-rw"}}},
	})
	require.Empty(t, said)
}
