package smartops

import (
	"github.com/taubyte/go-seer"
	"github.com/taubyte/tau/pkg/schema/basic"
)

func Open(seer *seer.Seer, name, application string) (SmartOps, error) {
	smartops := &smartOps{
		seer:        seer,
		name:        name,
		application: application,
	}

	var err error
	smartops.Resource, err = basic.New(seer, smartops, name)
	if err != nil {
		return nil, err
	}

	return smartops, nil
}
