package wasm

import (
	"context"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// workload.go is the host half of `taubyte:workload/identity` — what a workload is.
//
// # It is the one capability with no grant, and that is deliberate
//
// Every other host component here is authority over something a grant has to
// name: a store, a queue, a bucket, a secret. This is authority over nothing.
// It answers what the platform already put in the instance's environment, about
// the instance itself, and a workload cannot reach anything with it that it
// could not reach without it.
//
// The ambient-authority rule this cloud holds — a call that names no resource
// is declared or it is denied — is about REACH. There is none here to declare,
// so requiring an `access:` entry would be ceremony that teaches an author the
// rule means something it does not.
//
// # And it is why every workload gets the same six facts under one name
//
// It is a pile of environment variables today, effectively, and that is the
// problem: every author invents their own names, every framework's convention
// differs, and a library that wants to tag a metric with the environment it is
// running in cannot know what this deployment called it. One call with one
// shape means an SDK logs, traces and shards without the author wiring
// anything.

const workloadInterface = "taubyte:workload/identity@0.1.0"

// workloadExport is the one function. It is `get` rather than `identity` because
// the interface is already called `identity` and `identity.identity` says the
// word twice.
const workloadExport = "get"

// WithNode is the peer this tier is running on, for `self`. A tier composed
// without one answers "" rather than inventing a name — which is what a test
// runs as.
func WithNode(id string) Option {
	return func(r *Runtime) { r.node = id }
}

// workloadImports registers the one function for one instantiation.
//
// THE ANSWER CANNOT CHANGE UNDER A RUNNING INSTANCE: a spec is one revision of
// one resource, and the node is the tier's. What a guest gets on its thousandth
// call is what it got on its first — which is why the spec is captured here and
// nothing is read back out of shared state per call.
//
// THE VALUE ITSELF IS STILL BUILT PER CALL. Handing the same slice to the
// lowering twice would be lending it out, and eight allocations on a call a
// guest makes once at startup is not a cost worth taking that on.
func (r *Runtime) workloadImports(spec invoke.Spec) []component.Option {
	// A record lowers as a flat list of its fields in declaration order.
	identity := func() component.Value {
		scope := make([]component.Value, 0, len(spec.Scope))
		for _, name := range spec.Scope {
			scope = append(scope, name)
		}
		return []component.Value{
			spec.Project,
			spec.Environment,
			spec.Kind,
			spec.Label,
			scope,
			spec.Revision,
			spec.Artifact,
			r.node,
		}
	}

	tbl := component.NewTypeTable()
	fd := tbl.Func(nil, tbl.Record(
		"project", component.Prim("string"),
		"environment", component.Prim("string"),
		"kind", component.Prim("string"),
		"name", component.Prim("string"),
		"scope", tbl.List(component.Prim("string")),
		"revision", component.Prim("string"),
		"artifact", component.Prim("string"),
		"node", component.Prim("string"),
	))

	get := func(context.Context, []component.Value) ([]component.Value, error) {
		return []component.Value{identity()}, nil
	}

	return []component.Option{
		component.WithImportCustom(workloadInterface, workloadExport, get, fd, tbl.Resolver()),
	}
}
