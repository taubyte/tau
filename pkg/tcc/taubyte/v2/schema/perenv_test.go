package schema_test

import (
	"slices"
	"sort"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/engine"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

func TestWhatMayBeWrittenPerEnvironment(t *testing.T) {
	want := []string{
		"agent/egress", "agent/limits/concurrent", "agent/limits/memory",
		"agent/limits/steps", "agent/limits/timeout", "agent/memory/size",
		"agent/model/name", "agent/model/namespace", "agent/value",
		"service/egress", "service/execution/cpu", "service/execution/memory",
		"service/limits/concurrency",
		"service/limits/max_instances", "service/value",
		"bus/instances/limit",
		"cache/instances/limit", "cache/size", "cache/ttl",
		"database/document/database", "database/document/host",
		"database/document/size", "database/document/tls",
		"database/kv/size",
		"database/sql/database", "database/sql/host",
		"database/sql/size", "database/sql/tls",
		"database/instances/limit", "database/vector/size",
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
		"sync/queue/retention/age",
		"sync/queue/retention/size",
		"sync/log/retention/age",
		"sync/log/retention/size",
		"sandbox/egress", "sandbox/execution/cpu", "sandbox/execution/memory",
		"sandbox/lifetime/idle", "sandbox/lifetime/max",
		"sandbox/value",
		"storage/instances/limit", "storage/lifecycle/expire", "storage/size", "storage/streaming/ttl",
		"stream/host", "stream/sasl", "stream/tls",
		"volume/checkpoint/interval", "volume/checkpoint/rpo_budget", "volume/quota",
		"worker/egress", "worker/execution/cpu",
		"worker/execution/memory", "worker/execution/timeout",
		"worker/value",
		// A PACKAGE is where an artifact differs per deployment: where its build left
		// it, an image's tag, a value its build reads. Its steps are one list, the
		// same in every environment (a list element cannot vary).
		"package/output", "package/source/image", "package/value",
		"workflow/concurrency", "workflow/timeout",

		// WHAT A NAME KEEPS OF AN ANSWER: preview may keep nothing.
		"gateway/route/cache/ttl",

		// THE BRANCH A GIT SOURCE READS, on every kind that can take one.
		// `environments[].branches` already binds branches to a deployment, and
		// that rule stopped at the CONFIG repository: a resource whose content
		// lives in its own repo had one branch for every environment, so a
		// staging deployment read whatever production reads. The environment is
		// the axis this schema varies on, so it is the axis this varies on too.
		"agent/source/branch",
		"package/source/branch",
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
