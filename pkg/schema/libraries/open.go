package libraries

import (
	"github.com/taubyte/tau/pkg/schema/basic"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

func Open(seer *seer.Seer, name string, application string) (Library, error) {
	library := &library{
		seer:        seer,
		name:        name,
		application: application,
	}

	var err error
	library.Resource, err = basic.New(seer, library, name)
	if err != nil {
		return nil, err
	}

	return library, nil
}
