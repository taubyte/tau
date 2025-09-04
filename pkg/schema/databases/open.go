package databases

import (
	"github.com/taubyte/tau/pkg/schema/basic"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

func Open(seer *seer.Seer, name string, application string) (Database, error) {
	database := &database{
		seer:        seer,
		name:        name,
		application: application,
	}

	var err error
	database.Resource, err = basic.New(seer, database, name)
	if err != nil {
		return nil, err
	}

	return database, nil
}
