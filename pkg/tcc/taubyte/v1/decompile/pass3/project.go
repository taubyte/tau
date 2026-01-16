package pass3

import (
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type project struct{}

func Project() transform.Transformer[object.Refrence] {
	return &project{}
}

func (p *project) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	// pass1 deletes tags for compat, we don't need to restore it
	// as it's optional and was deleted for compatibility reasons
	return o, nil
}
