package functions

import (
	"github.com/taubyte/tau/pkg/schema/basic"
	seer "github.com/taubyte/tau/pkg/yaseer"
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
