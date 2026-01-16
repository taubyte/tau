package pass1

import (
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type project struct{}

func Project() transform.Transformer[object.Refrence] {
	return &project{}
}

func (p *project) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	o.Delete("tags") // TODO: delete. compat with old config-compiler
	return o, nil
}
