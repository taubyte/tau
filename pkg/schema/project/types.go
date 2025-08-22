package project

import (
	"github.com/taubyte/tau/pkg/schema/application"
	"github.com/taubyte/tau/pkg/schema/basic"
	"github.com/taubyte/tau/pkg/schema/databases"
	"github.com/taubyte/tau/pkg/schema/domains"
	"github.com/taubyte/tau/pkg/schema/functions"
	"github.com/taubyte/tau/pkg/schema/libraries"
	"github.com/taubyte/tau/pkg/schema/messaging"
	"github.com/taubyte/tau/pkg/schema/pretty"
	"github.com/taubyte/tau/pkg/schema/services"
	"github.com/taubyte/tau/pkg/schema/smartops"
	"github.com/taubyte/tau/pkg/schema/storages"
	"github.com/taubyte/tau/pkg/schema/website"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

type Project interface {
	Get() Getter
	Set(sync bool, ops ...basic.Op) (err error)
	Delete(attributes ...string) (err error)
	Prettify(p pretty.Prettier) map[string]interface{}
	ResourceMethods() []pretty.PrettyResourceIface

	Application(name string) (application.Application, error)
	Database(name string, application string) (databases.Database, error)
	Domain(name string, application string) (domains.Domain, error)
	Function(name string, application string) (functions.Function, error)
	Library(name string, application string) (libraries.Library, error)
	Messaging(name string, application string) (messaging.Messaging, error)
	Service(name string, application string) (services.Service, error)
	SmartOps(name string, application string) (smartops.SmartOps, error)
	Storage(name string, application string) (storages.Storage, error)
	Website(name string, application string) (website.Website, error)
}

type project struct {
	*basic.Resource
	seer *seer.Seer
}

type Getter interface {
	basic.Getter
	Applications() []string
	Tags() []string
	Email() string
	Services(string) (local []string, global []string)
	Libraries(string) (local []string, global []string)
	Websites(string) (local []string, global []string)
	Messaging(string) (local []string, global []string)
	Databases(string) (local []string, global []string)
	Storages(string) (local []string, global []string)
	Domains(string) (local []string, global []string)
	SmartOps(string) (local []string, global []string)
	Functions(string) (local []string, global []string)
}
