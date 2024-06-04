package functions

import (
	"github.com/taubyte/go-seer"
	"github.com/taubyte/tau/pkg/schema/basic"
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
