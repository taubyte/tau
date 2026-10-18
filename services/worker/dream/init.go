// Package dream runs the worker on a peer of an in-process mesh.
//
// # What a universe can and cannot prove about a worker
//
// IT CAN PROVE THE LOOKUP, which is what this service is. A request naming a
// resource crosses a real libp2p stream, the worker dials a real register on
// another node, reads a really-compiled project out of it and composes a spec
// from what the author wrote. Every hop there is the production one.
//
// IT CANNOT PROVE THE TIER unless the machine running the universe has a
// container backend. Starting a container needs docker or containerd, and a
// universe is a process — so the pool here is built from the runtimes the
// composer supplies with Runtimes, and a universe that supplies none reaches
// the tier and stops there. That is the honest boundary: the tier is proven in
// pkg/invoke/container's own tests, against real backends, and repeating it
// here would be repeating it badly.
package dream

import (
	"context"
	"io"
	"sync"

	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/p2p/mesh"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
	registerService "github.com/taubyte/tau/services/register"
	"github.com/taubyte/tau/services/worker"
)

// runtimes are the tiers a universe's workers run on.
//
// IT IS A PACKAGE-LEVEL HOOK AND THAT IS NOT AN ACCIDENT. A compute tier is a
// live object holding a connection to a container daemon, shared by every
// worker a test starts. A composer that has one sets it before starting a
// universe; a test that does not, does not.
var runtimes struct {
	sync.Mutex
	list []invoke.Runtime
}

// Runtimes sets the tiers workers in this process will run on. It replaces
// whatever was set before, and returns what it replaced so a test can put it
// back.
func Runtimes(rts ...invoke.Runtime) []invoke.Runtime {
	runtimes.Lock()
	defer runtimes.Unlock()
	was := runtimes.list
	runtimes.list = rts
	return was
}

// Start runs a worker on a new peer of m, reading projects from the register on
// the peer named, and answers the peer. The worker is closed with the mesh,
// before its peer.
func Start(m *mesh.Mesh, registerAt libp2ppeer.ID) (peer.Node, error) {
	return m.Add(func(node peer.Node) error {
		svc, err := create(m.Context(), node.Peer(), registerAt)
		if err != nil {
			return err
		}
		m.OnClose(svc)
		return nil
	})
}

func create(ctx context.Context, h host.Host, at libp2ppeer.ID) (io.Closer, error) {
	runtimes.Lock()
	tiers := append([]invoke.Runtime(nil), runtimes.list...)
	runtimes.Unlock()

	// A POOL WITH NO TIERS IS STILL A POOL. pkg/invoke refuses to build one, so
	// a universe that supplied none gets a pool holding a single tier that
	// starts nothing and says so — which is what makes "the lookup worked and
	// the tier was missing" a distinguishable answer rather than a
	// construction failure at boot.
	if len(tiers) == 0 {
		tiers = []invoke.Runtime{absent{}}
	}

	pool, err := invoke.NewPool(invoke.WithRuntime(tiers...))
	if err != nil {
		return nil, err
	}

	svc, err := worker.New(h, worker.Config{},
		worker.WithRegister(&universeRegister{host: h, at: at}),
		worker.WithPool(pool))
	if err != nil {
		pool.Close()
		return nil, err
	}
	if err := svc.Start(ctx); err != nil {
		svc.Close()
		pool.Close()
		return nil, err
	}
	return closer{svc: svc, pool: pool}, nil
}

type closer struct {
	svc  *worker.Service
	pool *invoke.Pool
}

// Close stops the worker, then the pool it was given.
//
// THE POOL IS CLOSED HERE BECAUSE IT WAS BUILT HERE. worker.Service takes a
// pool and never closes it — a pool is per-node and shared with everything else
// the node runs — so this is the one place that owns this particular one.
func (c closer) Close() error {
	err := c.svc.Close()
	c.pool.Close()
	return err
}

// universeRegister reads from the register on one named peer.
//
// IT DIALS ON FIRST USE RATHER THAN AT CONSTRUCTION, so a worker can be up
// before its register answers; the client is kept once it is made.
type universeRegister struct {
	host host.Host
	at   libp2ppeer.ID

	mu     sync.Mutex
	client *registerService.Client
}

func (r *universeRegister) Resolve(ctx context.Context, project, environment string) (register.Pointer, error) {
	c, err := r.dial()
	if err != nil {
		return register.Pointer{}, err
	}
	return c.Resolve(ctx, project, environment)
}

func (r *universeRegister) Object(ctx context.Context, id register.ObjectID) ([]byte, error) {
	c, err := r.dial()
	if err != nil {
		return nil, err
	}
	return c.Object(ctx, id)
}

func (r *universeRegister) dial() (*registerService.Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client != nil {
		return r.client, nil
	}

	c, err := registerService.NewClient(r.host, r.at)
	if err != nil {
		return nil, err
	}
	r.client = c
	return c, nil
}

// absent exists only so that a pool can be built at all: pkg/invoke refuses one
// with no tiers, which is right for a node and wrong for a universe that is
// testing the lookup rather than the running.
//
// ITS NAME MATCHES NO TIER IN THE VOCABULARY, so it is never selected and the
// pool answers ErrNoRuntime naming the tier the config actually asked for —
// which is a better answer than a placebo tier that pretended to start
// something.
type absent struct{}

func (absent) Name() string { return "none" }

func (absent) Start(context.Context, invoke.Spec) (invoke.Instance, error) {
	return nil, invoke.ErrNoRuntime
}
