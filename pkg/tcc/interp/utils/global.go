package utils

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type global struct {
	container string
	wrapped   transform.Transformer[object.Refrence]
}

// Global wraps a transformer so it runs at the project scope and then at each
// instance of the nested container group (the per-app scope). container is the
// container group's config key, derived from the schema by the caller — an empty
// key means the schema has no container, so only the project scope is walked.
func Global(container string, wrapped transform.Transformer[object.Refrence]) transform.Transformer[object.Refrence] {
	return &global{container: container, wrapped: wrapped}
}

func (g *global) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	// the project scope itself
	ct = ct.Fork(o)
	o, err := g.wrapped.Process(ct, o)
	if err != nil {
		return nil, fmt.Errorf("processing global object failed with %w", err)
	}

	// No container group -> nothing beyond the project scope to walk.
	if g.container == "" {
		return o, nil
	}
	if err := g.scopes(ct, o); err != nil {
		return nil, err
	}
	return o, nil
}

// scopes walks the container's instances, processing each ONCE and then descending into
// any it holds itself.
//
// v1's applications could not hold applications, so one level was the whole tree. v2's
// namespaces nest, and a resource in a namespace inside a namespace was never visited —
// the forward pass reaches it and this did not, which is one of the ways a v2 project
// compiled and could not be written back.
func (g *global) scopes(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) error {
	oapps, err := o.Child(g.container).Object()
	if err != nil {
		if err == object.ErrNotExist {
			return nil
		}
		return fmt.Errorf("fetching %s failed with %w", g.container, err)
	}

	for _, app := range oapps.Children() {
		sel := oapps.Child(app)
		oapp, _ := sel.Object()
		inner := ct.Fork(oapp)
		oapp, err := g.wrapped.Process(inner, oapp)
		if err != nil {
			return fmt.Errorf("processing scope %s failed with %w", app, err)
		}
		// THE CHAIN, not the parent again. Descending with the OUTER context gave a
		// resource two scopes deep the path of a resource one scope deep — the name
		// index is keyed by the scopes above a resource, so `shop/ship`'s queues were
		// looked up under `ship` and answered "not indexed" for a reference to a
		// sibling sitting right beside them.
		if err := g.scopes(inner, oapp); err != nil {
			return fmt.Errorf("inside %s: %w", app, err)
		}
		sel.Add(oapp)
	}
	return nil
}
