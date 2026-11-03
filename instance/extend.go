package instance

import (
	"context"
	"errors"
	"fmt"
	"slices"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/services"
)

// Lets a composer add a service this package does not name, registered by name with
// WithService before the config is checked, so a misspelled name is still refused.
// An added service gets the instance's facilities through Host, all closed with the
// instance before the built-in services.

// Host is what an instance gives each service it runs; see core/service.
type Host = service.Host

// Vault is what the cloud's secrets service answers, from whichever node runs it.
type Vault interface {
	Secret(ctx context.Context, project, environment, secretID string) (string, error)
	Set(ctx context.Context, project, environment, name string, value []byte) error
	Delete(ctx context.Context, project, environment, name string) error
	List(ctx context.Context, project, environment string) ([]string, error)
}

// withService runs s under name when the config names it, beside the services
// tau ships: what this package's own tests start.
func withService(name string, s service.Service) Option {
	return func(o *options) error {
		switch {
		case s == nil:
			return errors.New("node: a nil service")
		case name == "":
			return errors.New("node: a service needs a name")
		case slices.Contains(builtIn(), name) || slices.Contains(services.Names(), name):
			return fmt.Errorf("node: %q is a service already", name)
		}
		if _, twice := o.extra[name]; twice {
			return fmt.Errorf("node: %q is added twice", name)
		}
		if o.extra == nil {
			o.extra = map[string]service.Service{}
		}
		o.extra[name] = s
		return nil
	}
}

// named is one service this instance runs, under the name the config gave it.
type named struct {
	name    string
	service service.Service
}

// serviceNamed is the service the config's name stands for: one tau ships, or one
// this package's tests added.
func (o options) serviceNamed(name string) (service.Service, bool) {
	if s, ok := o.extra[name]; ok {
		return s, true
	}
	return services.New(name)
}

// serviceNames is every name serviceNamed answers.
func (o options) serviceNames() []string {
	out := services.Names()
	for name := range o.extra {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// builtIn is every name this package starts itself.
func builtIn() []string { return append(append([]string(nil), All...), BuildFrontend, CM) }

// startServices starts every service the config names, in its order. Each is closed
// with the instance, a failed start included.
func (n *Instance) startServices(ctx context.Context) error {
	for _, s := range n.services {
		n.addCloser(s.service)
		if err := s.service.Start(ctx, &added{node: n, name: s.name}); err != nil {
			return fmt.Errorf("node: %s: %w", s.name, err)
		}
		n.provide(s.name, s.service)
	}
	return nil
}

// added is one service's view of the instance.
type added struct {
	node *Instance
	name string
}

func (a *added) Peer() peer.Node            { return a.node.Node }
func (a *added) Root() string               { return a.node.cfg.Root }
func (a *added) Running() []service.Running { return a.node.runningNow() }
func (a *added) Cloud() string              { return a.node.cfg.Cloud }

// Peers is every node discovery currently says runs service, this one included
// when it does.
func (n *Instance) Peers(service string) []libp2ppeer.ID {
	return (&fromDiscovery{node: n, name: service}).Known()
}

func (a *added) Config(into any) error { return a.node.cfg.Section(a.name, into) }

// provide adds a running service, under its name, to what the others on this
// instance can Get.
func (n *Instance) provide(name string, handle any) {
	n.runningMu.Lock()
	defer n.runningMu.Unlock()
	n.running = append(n.running, service.Running{Name: name, Service: handle})
}

func (n *Instance) runningNow() []service.Running {
	n.runningMu.Lock()
	defer n.runningMu.Unlock()
	return slices.Clone(n.running)
}
