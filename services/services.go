// Package services is where a name an operator writes in an instance's service list
// meets the service it runs.
package services

import (
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/services/consensus"
	"github.com/taubyte/tau/services/discovery"
	"github.com/taubyte/tau/services/register"
	"github.com/taubyte/tau/services/secrets"
	"github.com/taubyte/tau/services/storage"
)

// shipped is one service tau ships: its name and its package's New.
type shipped struct {
	name string
	new  func() service.Service
}

// catalog is every service tau ships, IN THE ORDER AN INSTANCE STARTS THEM: what a
// service reaches beside it when it starts comes before it.
func catalog() []shipped {
	return []shipped{
		{consensus.Descriptor.Name, func() service.Service { return consensus.New() }},
		{discovery.Descriptor.Name, func() service.Service { return discovery.New() }},
		{register.Descriptor.Name, func() service.Service { return register.New() }},
		{secrets.Descriptor.Name, func() service.Service { return secrets.New() }},
		{storage.Descriptor.Name, func() service.Service { return storage.New() }},
	}
}

// New is the service named, unstarted.
func New(name string) (service.Service, bool) {
	for _, s := range catalog() {
		if s.name == name {
			return s.new(), true
		}
	}
	return nil, false
}

// Names is every name New answers, in the order an instance starts them.
func Names() []string {
	all := catalog()
	out := make([]string, 0, len(all))
	for _, s := range all {
		out = append(out, s.name)
	}
	return out
}
