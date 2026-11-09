package instance

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/discovery"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/rpc"
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
		case slices.Contains(services.Names(), name):
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

// serviceNames is every name serviceNamed answers, in the order they start: the
// catalog's, then what this package's tests added.
func (o options) serviceNames() []string {
	out := services.Names()
	added := make([]string, 0, len(o.extra))
	for name := range o.extra {
		added = append(added, name)
	}
	slices.Sort(added)
	return append(out, added...)
}

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
func (a *added) Region() string             { return a.node.cfg.Region }
func (a *added) Now() time.Time             { return time.Now() }

// HostOf is this instance as the service named sees it: its peer, its finder, the
// services running beside it, and that service's section.
func (n *Instance) HostOf(name string) service.Host { return &added{node: n, name: name} }

func (a *added) Config(into any) error { return a.node.cfg.Section(a.name, into) }

func (a *added) Find(id protocol.ID) discovery.Resolver       { return a.node.served.Find(id) }
func (a *added) Local(id protocol.ID) (service.Service, bool) { return a.node.served.Local(id) }
func (a *added) Serve(id protocol.ID, s service.Service)      { a.node.served.Serve(id, s) }

func (a *added) Retry() rpc.Retry { return a.node.served.Retry() }

// Serve says this node serves id with s, ahead of any service it runs for id: how a
// test's composer gives a node a mocked version of a service.
func (n *Instance) Serve(id protocol.ID, s service.Service) { n.served.Serve(id, s) }

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
