package utils

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type global struct {
	containers []string
	wrapped    transform.Transformer[object.Refrence]
}

// Global wraps a transformer so it runs at the project scope and then at each
// instance of every nested container group (the per-app scope). containers are the
// container groups' config keys, derived from the schema by the caller — an empty
// list means the schema has no container, so only the project scope is walked.
//
// A LIST, because a vocabulary may have more than one KIND of scope: one an author
// writes and one a lowering synthesizes for what a construct becomes. This took a single
// key, so the second kind's resources were walked by the compile driver (which is
// structural) and by nothing else — their references resolved to nothing and their
// entries in the index were never written.
func Global(containers []string, wrapped transform.Transformer[object.Refrence]) transform.Transformer[object.Refrence] {
	return &global{containers: containers, wrapped: wrapped}
}

func (g *global) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	// the project scope itself
	ct = ct.Fork(o)
	o, err := g.wrapped.Process(ct, o)
	if err != nil {
		return nil, fmt.Errorf("processing global object failed with %w", err)
	}

	// No container group -> nothing beyond the project scope to walk.
	if len(g.containers) == 0 {
		return o, nil
	}
	if err := g.scopes(ct, o); err != nil {
		return nil, err
	}
	return o, nil
}

// scopes walks each container's instances, processing each ONCE and then descending into
// any it holds itself — of any kind, because a scope of one kind holds scopes of the
// other.
//
// v1's applications could not hold applications, so one level was the whole tree. v2's
// namespaces nest, and a resource in a namespace inside a namespace was never visited —
// the forward pass reaches it and this did not, which is one of the ways a v2 project
// compiled and could not be written back.
func (g *global) scopes(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) error {
	for _, container := range g.containers {
		oapps, err := o.Child(container).Object()
		if err != nil {
			if err == object.ErrNotExist {
				continue
			}
			return fmt.Errorf("fetching %s failed with %w", container, err)
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
	}
	return nil
}
