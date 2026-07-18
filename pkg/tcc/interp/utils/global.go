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
	// global
	ct = ct.Fork(o)
	o, err := g.wrapped.Process(ct, o)
	if err != nil {
		return nil, fmt.Errorf("processing global object failed with %w", err)
	}

	// No container group -> nothing beyond the project scope to walk.
	if g.container == "" {
		return o, nil
	}

	// apps
	oapps, err := o.Child(g.container).Object()
	if err != nil {
		if err == object.ErrNotExist {
			return o, nil
		}
		return nil, fmt.Errorf("fetching %s failed with %w", g.container, err)
	}

	for _, app := range oapps.Children() {
		sel := oapps.Child(app)
		oapp, _ := sel.Object()
		oapp, err := g.wrapped.Process(ct.Fork(oapp), oapp)
		if err != nil {
			return nil, fmt.Errorf("processing application %s failed with %w", app, err)
		}
		sel.Add(oapp)
	}

	return o, nil
}
