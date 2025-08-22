package application

import (
	"github.com/taubyte/tau/pkg/schema/basic"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

// Open opens the application at root/applications/<name>, returns Application and error
func Open(seer *seer.Seer, name string) (Application, error) {
	app := &application{
		seer: seer,
		name: name,
	}

	var err error
	app.Resource, err = basic.New(seer, app, name)
	if err != nil {
		return nil, err
	}

	app.Resource.Root = app.Root
	app.Resource.Config = app.Config

	return app, nil
}
