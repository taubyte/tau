package messaging

import (
	"github.com/taubyte/tau/pkg/schema/basic"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

func Open(seer *seer.Seer, name string, application string) (Messaging, error) {
	messaging := &messaging{
		seer:        seer,
		name:        name,
		application: application,
	}

	var err error
	messaging.Resource, err = basic.New(seer, messaging, name)
	if err != nil {
		return nil, err
	}

	return messaging, nil
}
