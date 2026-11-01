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

// Global runs a transformer at the project scope, then at each instance of every
// container group; containers come from the schema, empty meaning project scope
// only. A list, since a lowering can synthesize a second kind of scope.
func Global(containers []string, wrapped transform.Transformer[object.Refrence]) transform.Transformer[object.Refrence] {
	return &global{containers: containers, wrapped: wrapped}
}

func (g *global) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
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

// scopes walks each container's instances once, then descends into any scopes
// they hold, of any kind: scopes nest.
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
			// The chain, not the parent: the name index is keyed by every scope above a
			// resource.
			if err := g.scopes(inner, oapp); err != nil {
				return fmt.Errorf("inside %s: %w", app, err)
			}
			sel.Add(oapp)
		}
	}
	return nil
}
