package functions

import (
	"github.com/taubyte/tau/pkg/tcc/internal/parity/schema/basic"
	seer "github.com/taubyte/tau/pkg/tcc/internal/parity/yaseer"
)

func Open(seer *seer.Seer, name, application string) (Function, error) {
	function := &function{
		seer:        seer,
		name:        name,
		application: application,
	}

	var err error
	function.Resource, err = basic.New(seer, function, name)
	if err != nil {
		return nil, err
	}

	return function, nil
}
