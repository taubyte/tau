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
)

// Lets a composer add a service this package does not name, registered by name with
// WithService before the config is checked, so a misspelled name is still refused.
// An added service gets the instance's facilities through Host, all closed with the
// instance before the built-in services.

// Host is what an instance gives each service it runs; see core/service.
type Host = service.Host

// Vault is what the cloud's secrets service answers.
type Vault = service.Vault

// WithService adds a service this instance can run when its config names it.
func WithService(s service.Service) Option {
	return func(o *options) error {
		switch {
		case s == nil:
			return errors.New("node: a nil service")
		case s.Name() == "":
			return errors.New("node: an added service needs a name")
		case slices.Contains(builtIn(), s.Name()):
			return fmt.Errorf("node: %q is a built-in service", s.Name())
		}
		for _, have := range o.extra {
			if have.Name() == s.Name() {
				return fmt.Errorf("node: %q is added twice", s.Name())
			}
		}
		o.extra = append(o.extra, s)
		return nil
	}
}

// builtIn is every name this package starts itself.
func builtIn() []string { return append(append([]string(nil), All...), BuildFrontend, CM) }

// startAdded starts every added service the config names, in the order they
// were added.
func (n *Instance) startAdded(ctx context.Context) error {
	for _, s := range n.opts.extra {
		if !n.cfg.runs(s.Name()) {
			continue
		}
		c, err := s.Start(ctx, &added{node: n, name: s.Name()})
		if err != nil {
			return fmt.Errorf("node: %s: %w", s.Name(), err)
		}
		if c != nil {
			n.addCloser(c)
		}
	}
	return nil
}

// added is one service's view of the node.
type added struct {
	node *Instance
	name string
}

func (a *added) Name() string             { return a.name }
func (a *added) Peer() peer.Node          { return a.node.Node }
func (a *added) Root() string             { return a.node.cfg.Root }
func (a *added) Local(service string) any { return a.node.local(service) }
func (a *added) Provide(handle any)       { a.node.provide(a.name, handle) }
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

// provide records what service offers the others on this instance.
func (n *Instance) provide(service string, handle any) {
	n.localsMu.Lock()
	defer n.localsMu.Unlock()
	if n.locals == nil {
		n.locals = map[string]any{}
	}
	n.locals[service] = handle
}

// local is what service provided on this instance, nil when it runs none.
func (n *Instance) local(service string) any {
	n.localsMu.Lock()
	defer n.localsMu.Unlock()
	return n.locals[service]
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
