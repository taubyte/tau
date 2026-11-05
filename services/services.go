// Package services is where a name an operator writes in an instance's service list
// meets the service it runs.
package services

import (
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/services/build"
	"github.com/taubyte/tau/services/build/hosted"
	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/services/database"
	"github.com/taubyte/tau/services/discovery"
	"github.com/taubyte/tau/services/primitives"
	"github.com/taubyte/tau/services/register"
	"github.com/taubyte/tau/services/runner"
	"github.com/taubyte/tau/services/secrets"
	"github.com/taubyte/tau/services/storage"
)

// shipped is one service tau ships: its name, its package's New, and the services it
// needs started beside it.
type shipped struct {
	name  string
	new   func() service.Service
	needs []string

	// onlyNamed is a service an instance runs only when its list names it, never as
	// part of the every-service default.
	onlyNamed bool
}

// catalog is every service tau ships, IN THE ORDER AN INSTANCE STARTS THEM: what a
// service reaches beside it when it starts comes before it.
func catalog() []shipped {
	return []shipped{
		{name: cm.Name, new: func() service.Service { return cm.New() }},
		{name: discovery.Descriptor.Name, new: func() service.Service { return discovery.New() }},
		{name: register.Descriptor.Name, new: func() service.Service { return register.New() }},
		{name: secrets.Descriptor.Name, new: func() service.Service { return secrets.New() }},
		{name: primitives.Descriptor.Name, new: func() service.Service { return primitives.New() }, needs: []string{cm.Name}},
		{name: database.Descriptor.Name, new: func() service.Service { return database.New() }, needs: []string{cm.Name}},
		{name: storage.Descriptor.Name, new: func() service.Service { return storage.New() }},
		{name: build.Runner.Name, new: func() service.Service { return runner.New() }},
		// Build is never part of the default: one node running every service runs the
		// runner, and a node may not run both. Its queue is a raft group.
		{name: build.Build.Name, new: func() service.Service { return hosted.New() }, needs: []string{cm.Name}, onlyNamed: true},
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

// Default is every service an instance runs when its list names none, in the order
// it starts them.
func Default() []string {
	var out []string
	for _, s := range catalog() {
		if !s.onlyNamed {
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
