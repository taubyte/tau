package node

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"

	"github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/services/discovery"
)

// extend.go lets a composer add a service this package does not name.
//
// The built-in services are a closed set on purpose — see config.go on why a
// misspelled name must be refused rather than ignored. An added service keeps
// that property: it is registered BY NAME with WithService before the config
// is checked, so a config naming it is valid and a config naming anything else
// still is not.
//
// WHAT IT IS HANDED IS THE NODE'S, NOT ITS OWN. A service that minted its own
// host, its own listener or its own database factory would be a second node in
// the process; Host is the narrow slice of this one an added service may use,
// and everything it opens through Host is closed with the node, before the
// built-in services it may still be calling.

// Service is a service added to a node by name.
type Service struct {
	// Name is what an operator writes in Config.Services, and what this node
	// reports to discovery while it runs the service.
	Name string

	// Start brings the service up. It runs after every built-in service has
	// started, so whatever it reaches through Host is already answering. The
	// closer it returns is closed first when the node closes.
	Start func(ctx context.Context, h Host) (io.Closer, error)
}

// Host is what a node gives a service added with WithService.
type Host interface {
	// Name is the name the service was added under.
	Name() string

	// Peer is the node's libp2p host.
	Peer() host.Host

	// PubSub is the node's gossip, or nil when it was composed without one.
	PubSub() discovery.PubSub

	// Database opens one of the service's own replicated databases. Its path
	// is under the service's name, so no added service can open a built-in
	// one by naming it. It is closed with the node.
	Database(name string) (kvdb.KVDB, error)

	// Listen serves handler to other nodes over p2p/rpc on id. The listener
	// is closed with the node.
	Listen(id protocol.ID, handler http.Handler) error

	// Handle serves handler on the node's public listener for every request
	// pattern matches, in net/http.ServeMux syntax — "mail.example.com/" or
	// "/v1/things/". Whatever no added pattern matches is the gateway's.
	Handle(pattern string, handler http.Handler) error

	// Peers is every node discovery currently says runs service, this one
	// included when it does. Nil when the node has no discovery view.
	Peers(service string) []libp2ppeer.ID

	// Secrets is the cloud's vault: this node's own when it runs one,
	// otherwise whichever node discovery names.
	Secrets() Vault

	// Root is the node's own directory, or "" when it was composed without one.
	Root() string

	// Logger is a logger named for the service.
	Logger() log.StandardLogger

	// Config decodes the service's entry in Config.Added into into, the way
	// the node's own file decodes: by yaml. With no entry it leaves into as
	// it was, so whatever into held is the default.
	Config(into any) error
}

// Vault is what the cloud's secrets service answers, from whichever node runs it.
type Vault interface {
	Secret(ctx context.Context, project, environment, secretID string) (string, error)
	Set(ctx context.Context, project, environment, name string, value []byte) error
	Delete(ctx context.Context, project, environment, name string) error
	List(ctx context.Context, project, environment string) ([]string, error)
}

var errNoServiceStart = errors.New("node: an added service needs a Start")

// WithService adds a service this node can run when its config names it.
func WithService(s Service) Option {
	return func(o *options) error {
		switch {
		case s.Name == "":
			return errors.New("node: an added service needs a name")
		case s.Start == nil:
			return errNoServiceStart
		case slices.Contains(builtIn(), s.Name):
			return fmt.Errorf("node: %q is a built-in service", s.Name)
		}
		for _, have := range o.extra {
			if have.Name == s.Name {
				return fmt.Errorf("node: %q is added twice", s.Name)
			}
		}
		o.extra = append(o.extra, s)
		return nil
	}
}

// builtIn is every name this package starts itself.
func builtIn() []string { return append(append([]string(nil), All...), BuildFrontend, Globals) }

// startAdded starts every added service the config names, in the order they
// were added.
func (n *Node) startAdded(ctx context.Context) error {
	for _, s := range n.opts.extra {
		if !n.cfg.runs(s.Name) {
			continue
		}
		c, err := s.Start(ctx, &added{node: n, name: s.Name})
		if err != nil {
			return fmt.Errorf("node: %s: %w", s.Name, err)
		}
		if c != nil {
			n.addCloser(c)
		}
	}
	return nil
}

// added is one service's view of the node.
type added struct {
	node *Node
	name string
}

func (a *added) Name() string             { return a.name }
func (a *added) Peer() host.Host          { return a.node.opts.host }
func (a *added) PubSub() discovery.PubSub { return a.node.opts.pubsub }
func (a *added) Root() string             { return a.node.opts.root }
func (a *added) Logger() log.StandardLogger {
	return log.Logger("tau.node." + a.name)
}

func (a *added) Database(name string) (kvdb.KVDB, error) {
	if a.node.opts.databases == nil {
		return nil, ErrNoDatabases
	}
	path := a.name
	if name != "" {
		path += "/" + name
	}
	db, err := a.node.opts.databases.New(a.Logger(), path, rebroadcastSeconds)
	if err != nil {
		return nil, err
	}
	a.node.addCloser(closerFunc(db.Close))
	return db, nil
}

func (a *added) Listen(id protocol.ID, handler http.Handler) error {
	server, err := rpc.Listen(context.Background(), a.node.opts.host, id, handler)
	if err != nil {
		return err
	}
	a.node.addCloser(server)
	return nil
}

func (a *added) Handle(pattern string, handler http.Handler) error {
	return a.node.public.handle(pattern, handler)
}

func (a *added) Peers(service string) []libp2ppeer.ID {
	return (&fromDiscovery{node: a.node, name: service}).Known()
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
