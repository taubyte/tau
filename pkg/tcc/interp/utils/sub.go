package utils

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type sub struct {
	wrapped transform.Transformer[object.Refrence]
	child   string
}

func Sub(wrapped transform.Transformer[object.Refrence], child string) transform.Transformer[object.Refrence] {
	return &sub{wrapped: wrapped, child: child}
}

func (g *sub) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	ct = ct.Fork(o)

	co, err := o.CreatePath(g.child)
	if err != nil {
		return nil, fmt.Errorf("creating path for child %s failed with %w", g.child, err)
	}

	_, err = g.wrapped.Process(ct.Fork(co), co)
	if err != nil {
		return nil, fmt.Errorf("processing sub-object failed with %w", err)
	}

	return o, nil
}
