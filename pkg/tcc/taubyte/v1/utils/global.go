package utils

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type global struct {
	wrapped transform.Transformer[object.Refrence]
}

func Global(wrapped transform.Transformer[object.Refrence]) transform.Transformer[object.Refrence] {
	return &global{wrapped: wrapped}
}

func (g *global) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	// global
	ct = ct.Fork(o)
	o, err := g.wrapped.Process(ct, o)
	if err != nil {
		return nil, fmt.Errorf("processing global object failed with %w", err)
	}

	// apps
	oapps, err := o.Child("applications").Object()
	if err != nil {
		if err == object.ErrNotExist {
			return o, nil
		}
		return nil, fmt.Errorf("fetching applications failed with %w", err)
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
