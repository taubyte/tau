package messaging

import (
	"github.com/taubyte/go-seer"
	"github.com/taubyte/tau/pkg/schema/basic"
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
