// Package dream runs the worker on a peer of an in-process mesh. It proves the
// lookup over real streams and a real register. It cannot prove the tier without a
// container backend: the pool is built from the runtimes set with Runtimes, and a
// cloud supplying none stops at the tier (proven in pkg/invoke/container).
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

type tierList struct {
	sync.Mutex
	list []invoke.Runtime
}

// runtimes are the tiers a cloud's workers run on, package-level because a tier
// is a live object shared by every worker a test starts.
var runtimes tierList

// Runtimes sets the tiers, and returns what it replaced so a test can put it back.
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
		svc, err := create(m.Context(), node.Host(), registerAt)
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

	// pkg/invoke refuses a pool with no tiers, so a cloud that supplied none gets a
	// single tier that starts nothing and says so.
	if len(tiers) == 0 {
		tiers = []invoke.Runtime{absent{}}
	}

	pool, err := invoke.NewPool(invoke.WithRuntime(tiers...))
	if err != nil {
		return nil, err
	}

	svc, err := worker.New(h, worker.Config{},
		worker.WithRegister(&cloudRegister{host: h, at: at}),
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

// Close stops the worker, then the pool: worker.Service never closes its pool, and
// this pool was built here.
func (c closer) Close() error {
	err := c.svc.Close()
	c.pool.Close()
	return err
}

// cloudRegister reads from the register on one named peer, dialling on first use
// so a worker can be up before its register answers.
type cloudRegister struct {
	host host.Host
	at   libp2ppeer.ID

	mu     sync.Mutex
	client *registerService.Client
}

func (r *cloudRegister) Resolve(ctx context.Context, project, environment string) (register.Pointer, error) {
	c, err := r.dial()
	if err != nil {
		return register.Pointer{}, err
	}
	return c.Resolve(ctx, project, environment)
}

func (r *cloudRegister) Object(ctx context.Context, id register.ObjectID) ([]byte, error) {
	c, err := r.dial()
	if err != nil {
		return nil, err
	}
	return c.Object(ctx, id)
}

func (r *cloudRegister) dial() (*registerService.Client, error) {
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

// absent exists only so a pool can be built. Its name matches no tier in the
// vocabulary, so the pool answers ErrNoRuntime naming the tier the config asked for.
type absent struct{}

func (absent) Name() string { return "none" }

func (absent) Start(context.Context, invoke.Spec) (invoke.Instance, error) {
	return nil, invoke.ErrNoRuntime
}
