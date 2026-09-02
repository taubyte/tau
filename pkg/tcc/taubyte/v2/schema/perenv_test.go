package schema_test

import (
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
//   - THE ACCESS BLOCK — `ops` is a capability. `ops_preview: [put]` is preview writing
//     where production reads.
//   - CONTRACTS — a cron's `schedule` (and `schedule_preview: "0 0 30 2 *"` is the
//     off-switch somebody would write instead of asking for one), a stream's `match`,
//     a route's `path`, a workflow's `start` and `on_error`, a zone's `records`.
//   - SHAPES — `database/vector/dimensions` and an agent's `memory/dimensions`: an index
//     built at another width is not the same index.
//   - RETRY POLICY — `max_attempts`, `retry/*`, a step's `attempts` and `backoff`. How
//     many times a thing is tried before it is given up on is the same promise in
//     every environment.
//   - WHAT IT RUNS — `source`, `execution/call`, `implements`, `instructions`. A branch
//     resolves to one environment, so that difference already has a spelling. `image`
//     is NOT in this set: an image is pulled rather than built, a coordinate like an
//     external database's host.
//   - REFERENCES — every one except `model/name`, which is marked with its namespace
//     because a cheaper model in preview is the difference people actually ask for.
func TestWhatMayBeWrittenPerEnvironment(t *testing.T) {
	want := []string{
		"agent/egress", "agent/limits/concurrent", "agent/limits/memory",
		"agent/limits/steps", "agent/limits/timeout", "agent/memory/size",
		"agent/model/name", "agent/model/namespace", "agent/value",
		"application/egress", "application/execution/cpu", "application/execution/memory",
		"application/image", "application/limits/concurrency",
		"application/limits/max_instances", "application/value",
		"bus/instances/limit",
		"cache/instances/limit", "cache/size", "cache/ttl",
		"database/document/database", "database/document/host",
		"database/document/size", "database/document/tls",
		"database/kv/size",
		"database/sql/database", "database/sql/host",
		"database/sql/size", "database/sql/tls",
		"database/instances/limit", "database/vector/size",
		"entity/egress", "entity/execution/cpu", "entity/execution/memory",
		"entity/execution/timeout", "entity/size", "entity/value",
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
		"queue/visibility_timeout",
		"sandbox/egress", "sandbox/execution/cpu", "sandbox/execution/memory",
		"sandbox/image", "sandbox/lifetime/idle", "sandbox/lifetime/max",
		"sandbox/value",
		"storage/instances/limit", "storage/size", "storage/streaming/ttl",
		"stream/host", "stream/tls",
		"volume/checkpoint/interval", "volume/checkpoint/rpo_budget", "volume/quota",
		"worker/egress", "worker/execution/cpu",
		"worker/execution/memory", "worker/execution/timeout", "worker/image",
		"worker/value",
		"workflow/concurrency", "workflow/timeout",
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
				if a.Meta["perEnv"] == true {
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
