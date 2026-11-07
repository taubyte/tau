package register

import (
	"context"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/rpc"
	pkgregister "github.com/taubyte/tau/pkg/register"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// Reader is the slice of a register a node reads what it serves through.
type Reader interface {
	Fleet
	ResolveView(ctx context.Context, project, environment, destination string) (pkgregister.ViewPointer, error)
	ResolveHost(ctx context.Context, fqdn string) (project, environment string, err error)
}

var (
	_ Reader = (*server)(nil)
	_ Reader = (*Client)(nil)
)

// DialReader is a client of the register at id, as a reader.
func DialReader(h host.Host, id libp2ppeer.ID) (Reader, error) { return NewClient(h, id) }

// Views is the register as the services of an instance read it: the register beside
// them, else the register nodes discovery names, and every deployment as the view
// placed in the instance's region, so nothing placed elsewhere reaches it.
//
// A REGISTER ELSEWHERE IS READ THROUGH WHAT THIS NODE KEEPS. A name, a view and the
// fleet's document are kept until a register watching them says they changed (see
// kept.go and watcher.go), so a request reads none of them over the wire, a name
// nobody claims included. A register beside them is read directly: it holds every
// answer already.
type Views struct {
	h      service.Host
	reach  *rpc.Reacher[Reader]
	region string

	w     *watcher
	hosts *kept[hostRead, hostAnswer]
	views *kept[viewRead, pkgregister.ViewPointer]
	fleet *kept[fleetRead, pkgregister.Pointer]
}

var (
	_ pkgregister.Registry = (*Views)(nil)
	_ Fleet                = (*Views)(nil)
)

// ViewsOn is the register as services on h read it. A cloud with no regions is read
// as schema.DefaultRegion, the one destination of a build with none.
func ViewsOn(h service.Host) *Views {
	return viewsOn(h, discovered{h}, defaultWatcherLimits, defaultKeptLimits)
}

func viewsOn(h service.Host, from registers, wl watcherLimits, kl keptLimits) *Views {
	region := h.Region()
	if region == "" {
		region = schema.DefaultRegion
	}
	w := newWatcher(h.Peer().Host(), from, wl)
	return &Views{
		h: h, reach: service.Reach(h, Protocol, DialReader), region: region, w: w,
		hosts: newKept[hostRead, hostAnswer](w, kl, time.Now),
		views: newKept[viewRead, pkgregister.ViewPointer](w, kl, time.Now),
		fleet: newKept[fleetRead, pkgregister.Pointer](w, kl, time.Now),
	}
}

// local is the register on this instance, when one runs here: whatever reads as one,
// as Reach finds it.
func (v *Views) local() (Reader, bool) { return service.Get[Reader](v.h) }

// Resolve is the view of project's environment placed in this instance's region.
func (v *Views) Resolve(ctx context.Context, project, environment string) (pkgregister.Pointer, error) {
	if s, ok := v.local(); ok {
		return pkgregister.InRegion(s, v.region).Resolve(ctx, project, environment)
	}
	p, err := v.views.get(ctx, viewRead{project: project, environment: environment, destination: v.region})
	if err != nil {
		return pkgregister.Pointer{}, err
	}
	return pkgregister.Pointer{Object: p.Object, Revision: p.Revision}, nil
}

// Routing is what this instance's region reaches elsewhere in the revision of
// project's environment it serves now.
func (v *Views) Routing(ctx context.Context, project, environment string) (pkgregister.Routing, error) {
	var (
		p   pkgregister.ViewPointer
		err error
	)
	if s, ok := v.local(); ok {
		p, err = s.ResolveView(ctx, project, environment, v.region)
	} else {
		p, err = v.views.get(ctx, viewRead{project: project, environment: environment, destination: v.region})
	}
	if err != nil {
		return pkgregister.Routing{}, err
	}
	manifest, err := v.Object(ctx, p.Manifest)
	if err != nil {
		return pkgregister.Routing{}, err
	}
	return pkgregister.RoutingOf(manifest, v.region)
}

// Object is never kept here: an object is immutable, and a cache of compiled
// projects keeps what it parsed from one.
func (v *Views) Object(ctx context.Context, id pkgregister.ObjectID) ([]byte, error) {
	r, err := v.reach.Now(ctx)
	if err != nil {
		return nil, err
	}
	return r.Object(ctx, id)
}

// ResolveHost is the deployment a domain names.
func (v *Views) ResolveHost(ctx context.Context, fqdn string) (string, string, error) {
	if s, ok := v.local(); ok {
		return s.ResolveHost(ctx, fqdn)
	}
	a, err := v.hosts.get(ctx, hostRead{fqdn: pkgregister.CanonicalHost(fqdn)})
	return a.project, a.environment, err
}

// ResolveFleet is the cloud's own document.
func (v *Views) ResolveFleet(ctx context.Context) (pkgregister.Pointer, error) {
	if s, ok := v.local(); ok {
		return s.ResolveFleet(ctx)
	}
	return v.fleet.get(ctx, fleetRead{})
}

// Close releases the streams to registers elsewhere, and what was kept through them.
func (v *Views) Close() error {
	v.w.close()
	return v.reach.Close()
}
