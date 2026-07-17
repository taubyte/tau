package project

import (
	"github.com/taubyte/tau/pkg/tcc/internal/parity/schema/application"
	"github.com/taubyte/tau/pkg/tcc/internal/parity/schema/databases"
	"github.com/taubyte/tau/pkg/tcc/internal/parity/schema/domains"
	"github.com/taubyte/tau/pkg/tcc/internal/parity/schema/functions"
	"github.com/taubyte/tau/pkg/tcc/internal/parity/schema/libraries"
	"github.com/taubyte/tau/pkg/tcc/internal/parity/schema/messaging"
	"github.com/taubyte/tau/pkg/tcc/internal/parity/schema/services"
	"github.com/taubyte/tau/pkg/tcc/internal/parity/schema/smartops"
	"github.com/taubyte/tau/pkg/tcc/internal/parity/schema/storages"
	"github.com/taubyte/tau/pkg/tcc/internal/parity/schema/website"
)

func (p *project) Application(name string) (application.Application, error) {
	return application.Open(p.seer, name)
}

func (p *project) Database(name string, application string) (databases.Database, error) {
	return databases.Open(p.seer, name, application)
}

func (p *project) Domain(name string, application string) (domains.Domain, error) {
	return domains.Open(p.seer, name, application)
}

func (p *project) Function(name string, application string) (functions.Function, error) {
	return functions.Open(p.seer, name, application)
}

func (p *project) Library(name string, application string) (libraries.Library, error) {
	return libraries.Open(p.seer, name, application)
}

func (p *project) Messaging(name string, application string) (messaging.Messaging, error) {
	return messaging.Open(p.seer, name, application)
}

func (p *project) Service(name string, application string) (services.Service, error) {
	return services.Open(p.seer, name, application)
}

func (p *project) SmartOps(name string, application string) (smartops.SmartOps, error) {
	return smartops.Open(p.seer, name, application)
}

func (p *project) Storage(name string, application string) (storages.Storage, error) {
	return storages.Open(p.seer, name, application)
}

func (p *project) Website(name string, application string) (website.Website, error) {
	return website.Open(p.seer, name, application)
}
