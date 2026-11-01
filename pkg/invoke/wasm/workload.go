package wasm

import (
	"context"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// workload.go is the host half of `taubyte:workload/identity`: what a workload is.
// It reaches nothing, so it needs no grant. One call with one shape lets an SDK tag
// logs, traces and metrics without per-author naming.

const workloadInterface = "taubyte:workload/identity@0.1.0"

// workloadExport is `get`, since the interface is already `identity`.
const workloadExport = "get"

// WithNode is the peer this tier runs on, for `self`; "" when unset.
func WithNode(id string) Option {
	return func(r *Runtime) { r.node = id }
}

// workloadImports captures the spec, since the answer cannot change under a running
// instance; the value is still built per call rather than lending one slice.
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
