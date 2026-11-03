// Package services is where a name an operator writes in an instance's service list
// meets the service it runs.
package services

import (
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/services/consensus"
	"github.com/taubyte/tau/services/database"
	"github.com/taubyte/tau/services/discovery"
	"github.com/taubyte/tau/services/primitives"
	"github.com/taubyte/tau/services/register"
	"github.com/taubyte/tau/services/secrets"
	"github.com/taubyte/tau/services/storage"
)

// shipped is one service tau ships: its name, its package's New, and the services it
// needs started beside it.
type shipped struct {
	name  string
	new   func() service.Service
	needs []string
}

// catalog is every service tau ships, IN THE ORDER AN INSTANCE STARTS THEM: what a
// service reaches beside it when it starts comes before it.
func catalog() []shipped {
	return []shipped{
		{consensus.Descriptor.Name, func() service.Service { return consensus.New() }, nil},
		{discovery.Descriptor.Name, func() service.Service { return discovery.New() }, nil},
		{register.Descriptor.Name, func() service.Service { return register.New() }, nil},
		{secrets.Descriptor.Name, func() service.Service { return secrets.New() }, nil},
		{primitives.Descriptor.Name, func() service.Service { return primitives.New() }, []string{consensus.Descriptor.Name}},
		{database.Descriptor.Name, func() service.Service { return database.New() }, []string{consensus.Descriptor.Name}},
		{storage.Descriptor.Name, func() service.Service { return storage.New() }, nil},
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

// Needs is what the service named needs started beside it, and what those need, in
// the order an instance starts them.
func Needs(name string) []string {
	want := map[string]bool{}
	var walk func(string)
	walk = func(name string) {
		for _, s := range catalog() {
			if s.name != name {
				continue
			}
			for _, need := range s.needs {
				if !want[need] {
					want[need] = true
					walk(need)
				}
			}
		}
	}
	walk(name)
	var out []string
	for _, s := range catalog() {
		if want[s.name] {
			out = append(out, s.name)
		}
	}
	return out
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
