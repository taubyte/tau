package schema_test

import (
	"slices"
	"sort"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/engine"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// EVERY FIELD THAT MAY BE WRITTEN PER ENVIRONMENT, and the whole list, so adding one
// is a deliberate line in a diff rather than a side effect of declaring a field.
//
// Marked where each is declared, judged on what that key means in the resource it
// belongs to — the same word answers differently in two places, which is why this is
// not derived: a `size` is a ceiling and `dimensions` is the shape of an index, and no
// property the schema declares tells them apart.
//
// A CEILING is marked, wherever it is spelled — `size`, `max_instances`, and now a
// store's `instances/limit`. How much a preview deployment may hold is the difference
// people actually ask for, and lowering it changes what the resource may DO rather than
// what it IS: an instance that cannot be admitted in preview is still the same store.
//
// What is refused, and why, since the refusals are the argument:
//
//   - IDENTITY — `id`, `name`. Two environments of one resource are one resource.
//
//   - THE ACCESS BLOCK — `ops` is a capability. `ops_preview: [put]` is preview writing
//     where production reads.
//
//   - CONTRACTS — a cron's `schedule` (and `schedule_preview: "0 0 30 2 *"` is the
//     off-switch somebody would write instead of asking for one), a stream's `match`,
//     a route's `path`, a workflow's `start` and `on_error`, a zone's `records`.
//
//   - SHAPES — `database/vector/dimensions` and an agent's `memory/dimensions`: an index
//     built at another width is not the same index.
//
//   - RETRY POLICY — `max_attempts`, `retry/*`, a step's `attempts` and `backoff`. How
//     many times a thing is tried before it is given up on is the same promise in
//     every environment.
//
//   - WHAT IT RUNS — `source`, `execution/call`, `implements`, `instructions`. A branch
//     resolves to one environment, so that difference already has a spelling.
//     `source/image` is NOT in this set: an image is pulled rather than built, a
//     coordinate like an external database's host.
//
//   - HOW IT IS BUILT, structurally — `build/script`, `build/dockerfile`,
//     `build/target`. A different recipe is a different artifact, and the branch key
//     itself cannot carry a suffix anyway (TestABuildBranchCannotBeEnvironmentSuffixed).
//     What IS marked is what a preview deployment genuinely differs in: the toolchain
//     it builds with, the two commands, and where the product lands. That is the axis
//     every other platform spells as a per-context build override, and it arrives here
//     as `run_preview` sitting beside `run`.
//
//   - REFERENCES — every one except `model/name`, which is marked with its namespace
//     because a cheaper model in preview is the difference people actually ask for.
//
//   - PLACEMENT — a resource's own `regions` block. Its regions sit directly ON the
//     branch key (`pinned: [region-1, region-2]`) and envPath refuses a path ending in an Either,
//     because a branch is a key the document chooses rather than one the schema names:
//     there is nothing there to suffix. What IS per-environment is the SCOPE's own
//     `regions`, a plain list — `regions_preview: [region-2]` at the project places every
//     resource that says nothing, which is the lever, in one line. A resource that
//     pins itself pins itself in every environment, which is what a residency line
//     should do.
func TestWhatMayBeWrittenPerEnvironment(t *testing.T) {
	want := []string{
		"agent/egress", "agent/limits/concurrent", "agent/limits/memory",
		"agent/limits/steps", "agent/limits/timeout", "agent/memory/size",
		"agent/model/name", "agent/model/namespace", "agent/value",
		"application/egress", "application/execution/cpu", "application/execution/memory",
		"application/limits/concurrency",
		"application/limits/max_instances", "application/source/image", "application/value",
		"bus/instances/limit",
		"cache/instances/limit", "cache/size", "cache/ttl",
		"component/build/image", "component/build/install", "component/build/output",
		"component/build/run", "component/value",
		"database/document/database", "database/document/host",
		"database/document/size", "database/document/tls",
		"database/kv/size",
		"database/sql/database", "database/sql/host",
		"database/sql/size", "database/sql/tls",
		"database/instances/limit", "database/vector/size",
		"function/build/image", "function/build/install", "function/build/output",
		"function/build/run",
		"function/egress", "function/execution/cpu", "function/execution/memory",
		"function/execution/timeout", "function/value",
		"gateway/cors/allow_self", "gateway/cors/origins",
		"gateway/rate_limit/burst", "gateway/rate_limit/rpm",
		"mcp/tools/cache_ttl",
		"model/identifier", "model/inference/remote/endpoint",
		"model/parameters/max_tokens", "model/parameters/temperature",
		"model/parameters/top_p",
		"proxy/rate_limit/burst", "proxy/rate_limit/concurrent",
		"proxy/rate_limit/connections_per_minute",
		"sync/queue/visibility_timeout",
		"sync/log/retention/age",
		"sync/log/retention/size",
		"sandbox/egress", "sandbox/execution/cpu", "sandbox/execution/memory",
		"sandbox/lifetime/idle", "sandbox/lifetime/max",
		"sandbox/source/image", "sandbox/value",
		"storage/instances/limit", "storage/size", "storage/streaming/ttl",
		"stream/host", "stream/tls",
		"volume/checkpoint/interval", "volume/checkpoint/rpo_budget", "volume/quota",
		"website/build/image", "website/build/install", "website/build/output",
		"website/build/run", "website/value",
		"worker/build/image", "worker/build/install", "worker/build/output",
		"worker/build/run",
		"worker/egress", "worker/execution/cpu",
		"worker/execution/memory", "worker/execution/timeout",
		"worker/source/image", "worker/value",
		"workflow/concurrency", "workflow/timeout",

		// PLACEMENT, on every kind — one decision, not twenty-three. The BRANCH
		// takes the suffix: `pinned: [region-1]` beside `pinned_preview: [region-2]`.
		// See regionAttributes.
		"agent/regions/in",
		"application/regions/in",
		"bus/regions/in",
		"cache/regions/in",
		"channel/regions/in",
		"component/regions/in",
		"cron/regions/in",
		"database/regions/in",
		"function/regions/in",
		"gateway/regions/in",
		"mcp/regions/in",
		"model/regions/in",
		"proxy/regions/in",
		"sandbox/regions/in",
		"secret/regions/in",
		"storage/regions/in",
		"stream/regions/in",
		"sync/regions/in",
		"volume/regions/in",
		"website/regions/in",
		"worker/regions/in",
		"workflow/regions/in",
		"zone/regions/in",

		// THE BRANCH A GIT SOURCE READS, on every kind that can take one.
		// `environments[].branches` already binds branches to a deployment, and
		// that rule stopped at the CONFIG repository: a resource whose content
		// lives in its own repo had one branch for every environment, so a
		// staging deployment read whatever production reads. The environment is
		// the axis this schema varies on, so it is the axis this varies on too.
		"agent/source/branch",
		"application/source/branch",
		"component/source/branch",
		"function/source/branch",
		"sandbox/source/branch",
		"website/source/branch",
		"worker/source/branch",
	}

	seen := map[string]bool{}
	for _, g := range schema.V2Resources {
		if len(g.Children) == 0 {
			continue
		}
		kind, _ := g.Match.(string)
		var walk func(n *engine.Node)
		walk = func(n *engine.Node) {
			for _, a := range n.Attributes {
				if slices.Contains(engine.VariesBy(a), schema.EnvironmentVar) {
					seen[kind+"/"+a.Name] = true
				}
			}
			for _, c := range n.Children {
				walk(c)
			}
		}
		walk(g.Children[0])
	}
	got := make([]string, 0, len(seen))
	for k := range seen {
		got = append(got, k)
	}
	sort.Strings(got)
	sort.Strings(want)
	assert.DeepEqual(t, got, want)
}
