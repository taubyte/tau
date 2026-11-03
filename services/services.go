// Package services is where a name an operator writes in an instance's service list
// meets the service it runs.
package services

import (
	"sort"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/services/storage"
)

// shipped is every service tau ships, by name, each its package's New.
func shipped() map[string]func() service.Service {
	return map[string]func() service.Service{
		storage.Descriptor.Name: func() service.Service { return storage.New() },
	}
}

// New is the service named, unstarted.
func New(name string) (service.Service, bool) {
	newService, ok := shipped()[name]
	if !ok {
		return nil, false
	}
	return newService(), true
}

// Names is every name New answers.
func Names() []string {
	out := make([]string, 0, len(shipped()))
	for name := range shipped() {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
