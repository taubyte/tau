// Package dream makes the worker available inside a dream universe.
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
	"fmt"
	"io"
	"sync"

	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	iface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	dreamv2 "github.com/taubyte/tau/dream/v2"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
	registerService "github.com/taubyte/tau/services/register"
	registerDream "github.com/taubyte/tau/services/register/dream"
	"github.com/taubyte/tau/services/worker"
)

// Protocol is what a universe calls this service.
const Protocol = "worker"

// runtimes are the tiers a universe's workers run on.
//
// IT IS A PACKAGE-LEVEL HOOK AND THAT IS NOT AN ACCIDENT. dream's registry
// hands a service a universe and a yaml-shaped config, and a compute tier is
// neither — it is a live object holding a connection to a container daemon. A
// composer that has one sets it before starting a universe; a test that does
// not, does not.
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

func init() {
	if err := dreamv2.Register(Protocol, create); err != nil {
		panic(err)
	}
}

func create(ctx context.Context, h host.Host, _ peer.Node, u *dream.Universe, _ *iface.ServiceConfig) (io.Closer, error) {
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
		worker.WithRegister(&universeRegister{host: h, universe: u}),
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

// universeRegister finds a register node in the universe and reads from it.
//
// IT LOOKS THE PEER UP PER CALL RATHER THAN AT CONSTRUCTION, because dream
// starts every service concurrently and a worker built before the register
// would have nothing to dial. That is also how a fleet works: which register
// node answers is a routing question, not a wiring one.
type universeRegister struct {
	host     host.Host
	universe *dream.Universe

	mu     sync.Mutex
	client *registerService.Client
	at     libp2ppeer.ID
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
	pids, err := r.universe.GetServicePids(registerDream.Protocol)
	if err != nil || len(pids) == 0 {
		return nil, fmt.Errorf("worker/dream: no register in this universe: %w", err)
	}

	id, err := libp2ppeer.Decode(pids[0])
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client != nil && r.at == id {
		return r.client, nil
	}
	if r.client != nil {
		r.client.Close()
	}

	c, err := registerService.NewClient(r.host, id)
	if err != nil {
		return nil, err
	}
	r.client, r.at = c, id
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
