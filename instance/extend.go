package instance

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"

	"github.com/ipfs/go-log/v2"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/service"
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

// Vault is what the cloud's secrets service answers.
type Vault = service.Vault

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
		n.provide(s.service)
	}
	return nil
}

// added is one service's view of the node.
type added struct {
	node *Instance
	name string
}

func (a *added) Name() string    { return a.name }
func (a *added) Peer() peer.Node { return a.node.Node }
func (a *added) Root() string    { return a.node.cfg.Root }
func (a *added) Running() []any  { return a.node.runningNow() }
func (a *added) Logger() log.StandardLogger {
	return log.Logger("tau.node." + a.name)
}

func (a *added) Database(name string) (kvdb.Instance, error) {
	path := a.name
	if name != "" {
		path += "/" + name
	}
	db, err := a.node.kvdb(path)
	if err != nil {
		return nil, err
	}
	a.node.addCloser(closerFunc(db.Close))
	return db, nil
}

func (a *added) Listen(id protocol.ID, handler http.Handler) error {
	server, err := rpc.Listen(context.Background(), a.node.Host(), id, handler)
	if err != nil {
		return err
	}
	a.node.addCloser(server)
	return nil
}

func (a *added) Handle(pattern string, handler http.Handler) error {
	return a.node.public.handle(pattern, handler)
}

func (a *added) Peers(service string) []libp2ppeer.ID { return a.node.Peers(service) }

// Peers is every node discovery currently says runs service, this one included
// when it does.
func (n *Instance) Peers(service string) []libp2ppeer.ID {
	return (&fromDiscovery{node: n, name: service}).Known()
}

func (a *added) Secrets() Vault { return a.node.Vault() }

func (a *added) Config(into any) error {
	entry, ok := a.node.cfg.Added[a.name]
	if !ok {
		return nil
	}
	// A ROUND TRIP AND NOT A CAST. From a file the entry is whatever yaml
	// decoded it to; from Go it may be the service's own struct. Either way
	// the service gets what its yaml tags say, exactly as an operator wrote it.
	raw, err := yaml.Marshal(entry)
	if err != nil {
		return fmt.Errorf("node: %s: config: %w", a.name, err)
	}
	if err := yaml.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("node: %s: config: %w", a.name, err)
	}
	return nil
}

// provide adds a running service to what the others on this instance can Get.
func (n *Instance) provide(handle any) {
	n.runningMu.Lock()
	defer n.runningMu.Unlock()
	n.running = append(n.running, handle)
}

func (n *Instance) runningNow() []any {
	n.runningMu.Lock()
	defer n.runningMu.Unlock()
	return slices.Clone(n.running)
}

// public is what added services serve on the node's public listener, in front
// of the gateway.
type public struct {
	mu    sync.RWMutex
	mux   *http.ServeMux
	count int
}

func (p *public) handle(pattern string, handler http.Handler) (err error) {
	if handler == nil {
		return errors.New("node: a nil handler")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mux == nil {
		p.mux = http.NewServeMux()
	}
	// ServeMux refuses a malformed or conflicting pattern by panicking, which
	// for a pattern a service computed at start is an error to return.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("node: pattern %q: %v", pattern, r)
		}
	}()
	p.mux.Handle(pattern, handler)
	p.count++
	return nil
}

// route is the handler an added pattern names for r, or nil.
func (p *public) route(r *http.Request) http.Handler {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.count == 0 {
		return nil
	}
	h, pattern := p.mux.Handler(r)
	if pattern == "" {
		return nil
	}
	return h
}

// front serves an added pattern when one matches and next otherwise. A nil
// next answers 404.
func (p *public) front(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := p.route(r); h != nil {
			h.ServeHTTP(w, r)
			return
		}
		if next == nil {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
