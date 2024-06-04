package domains

import (
	"github.com/taubyte/go-seer"
	"github.com/taubyte/tau/pkg/schema/basic"
)

func Open(seer *seer.Seer, name, application string) (Domain, error) {
	domain := &domain{
		seer:        seer,
		name:        name,
		application: application,
	}

	var err error
	domain.Resource, err = basic.New(seer, domain, name)
	if err != nil {
		return nil, err
	}

	return domain, nil
}
