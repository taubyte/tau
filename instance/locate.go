package instance

import (
	"context"
	"errors"
	"sort"

	storageService "github.com/taubyte/tau/services/storage"

	"github.com/ipfs/go-log/v2"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/rpc"
	pkgregister "github.com/taubyte/tau/pkg/register"
	registerService "github.com/taubyte/tau/services/register"
)

// How one service on this node finds another: use it directly if this node runs it,
// otherwise dial a node the finder names (find.go). The rule is p2p/rpc's rpc.Reach;
// this package supplies who to call (Node.routeTo).

var logger = log.Logger("tau.v2.node")

// rebroadcastSeconds is how often the CRDT under the register re-announces its
// heads. Five is what every other kvdb in this repo uses; differing would be a
// number to explain.
const rebroadcastSeconds = 5

// ErrNoRegister is a node that needs a register and can find none.
var ErrNoRegister = errors.New("node: no register is reachable")

// registerReader is the read surface both halves offer. Publishing is not on
// it: nothing this composition wires is allowed to deploy.
type registerReader interface {
	Resolve(ctx context.Context, project, environment string) (pkgregister.Pointer, error)
	ResolveView(ctx context.Context, project, environment, destination string) (pkgregister.ViewPointer, error)
	Object(ctx context.Context, id pkgregister.ObjectID) ([]byte, error)
	ResolveHost(ctx context.Context, fqdn string) (project, environment string, err error)
}

// registerSeam is the register as the reach hands it back: this node's own, or a
// stream to a node the finder names. It is the union its callers need; each is
// handed a narrower view, and only a deploy's carries Publish.
type registerSeam interface {
	registerReader
	Environments(ctx context.Context, project string) ([]string, error)
	Publish(ctx context.Context, p pkgregister.Publication) (pkgregister.ObjectID, error)
	Manifest(ctx context.Context, project, environment string) (pkgregister.Manifest, pkgregister.ObjectID, error)

	// The project table (projects.go): read by plm and the builder both, written by
	// the frontend's link.
	LinkProject(ctx context.Context, l pkgregister.Link) (pkgregister.ProjectRecord, error)
	Project(ctx context.Context, id string) (pkgregister.ProjectRecord, error)
	Repository(ctx context.Context, repository string) (pkgregister.RepositoryRecord, error)
	ProjectsOf(ctx context.Context, claimant string) ([]pkgregister.ProjectRecord, error)
	ListProjects(ctx context.Context) ([]pkgregister.ProjectRecord, error)
	GrantRole(ctx context.Context, project, grantee string, role pkgregister.Role, by string) (pkgregister.Grant, error)
	Revoke(ctx context.Context, project, id, by string) error
	Grants(ctx context.Context, project string) ([]pkgregister.Grant, error)
	RoleOf(ctx context.Context, project, user string) (pkgregister.Role, error)
}

// routeTo is the pick half of every reach: a node serving id, as the finder finds
// it. What an empty answer means is the reach's.
func (n *Instance) routeTo(id protocol.ID, absent error) func(context.Context) (libp2ppeer.ID, error) {
	return func(context.Context) (libp2ppeer.ID, error) {
		known := n.finder.Find(id).Known()
		if len(known) == 0 {
			return "", absent
		}
		return known[0], nil
	}
}

// openReach wires every locator this node has, BEFORE anything that uses one.
//
// They are built here and not lazily because each is a value other objects are
// handed; what they read is lazy instead — every one of the three closures
// below reads a field of the node at CALL time, so a service opened after this
// is still found by a reach built before it.
func (n *Instance) openReach() {
	n.views = registerService.ViewsOn(&added{node: n})
	n.store = storageService.StoreOn(&added{node: n})
	n.registers = rpc.Reach(n.Host(), Register, n.cfg.Clients.Retry,
		n.routeTo(registerService.Protocol, ErrNoRegister),
		func() (registerSeam, bool) {
			// A LOCAL REGISTER IS NOT A CACHE, IT IS THE ABSENCE OF A HOP.
			if n.register == nil || n.register.Local() == nil {
				return nil, false
			}
			return n.register.Local(), true
		},
		func(id libp2ppeer.ID) (registerSeam, error) {
			c, err := registerService.NewClient(n.Host(), id)
			if err != nil {
				return nil, err
			}
			return c, nil
		})

	n.openPrimitivesReach()
}

// closeReach releases every stream the locators hold. A local service is never
// closed here — it belongs to the node that runs it, and is in n.closers.
//
// It is tolerant of a half-built node because New closes one: a construction
// that failed before openReach ran has nothing to give back.
func (n *Instance) closeReach() {
	if n.registers != nil {
		n.registers.Close()
	}
	if n.views != nil {
		n.views.Close()
	}
	if n.store != nil {
		n.store.Close()
	}
	if n.sends != nil {
		n.sends.Close()
	}
}

// closerFunc adapts a plain stop function to io.Closer, for the things this
// package opens that do not have one — a kvdb and a pool both close without
// answering.
type closerFunc func()

func (f closerFunc) Close() error { f(); return nil }

// starter is what Start walks. It is an explicit list rather than a loop over
// an interface slice because a typed nil in an interface is not a nil
// interface, and the loop would call Start on a service this node does not run.
type starter interface {
	Start(context.Context) error
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
