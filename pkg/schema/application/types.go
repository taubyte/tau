package application

import (
	"github.com/taubyte/tau/pkg/schema/basic"
	"github.com/taubyte/tau/pkg/schema/pretty"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

type Application interface {
	Get() Getter
	Set(sync bool, ops ...basic.Op) (err error)
	Delete(attributes ...string) (err error)
	Prettify(p pretty.Prettier, resources []pretty.PrettyResourceIface) map[string]interface{}
}

// Application represents the config at root/applications/<name>/config.yaml
type application struct {
	*basic.Resource
	seer *seer.Seer
	name string
}

// Getter is an abstraction for getting values from an application config
type Getter interface {
	basic.Getter
	Tags() []string

	// TODO add methods to get resources from an Application
	// project will need to pass an interface to application for getting
	// the information about an application's resources
}
