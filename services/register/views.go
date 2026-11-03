package register

import (
	"context"

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
// them, else one discovery names, and every deployment as the view placed in the
// instance's region, so nothing placed elsewhere reaches it.
type Views struct {
	reach  *rpc.Reacher[Reader]
	region string
}

var (
	_ pkgregister.Registry = (*Views)(nil)
	_ Fleet                = (*Views)(nil)
)

// ViewsOn is the register as services on h read it. A cloud with no regions is read
// as schema.DefaultRegion, the one destination of a build with none.
func ViewsOn(h service.Host) *Views {
	region := h.Region()
	if region == "" {
		region = schema.DefaultRegion
	}
	return &Views{reach: service.Reach(h, Descriptor.Name, DialReader), region: region}
}

// Resolve is the view of project's environment placed in this instance's region.
func (v *Views) Resolve(ctx context.Context, project, environment string) (pkgregister.Pointer, error) {
	r, err := v.reach.Now(ctx)
	if err != nil {
		return pkgregister.Pointer{}, err
	}
	return pkgregister.InRegion(r, v.region).Resolve(ctx, project, environment)
}

func (v *Views) Object(ctx context.Context, id pkgregister.ObjectID) ([]byte, error) {
	r, err := v.reach.Now(ctx)
	if err != nil {
		return nil, err
	}
	return r.Object(ctx, id)
}

// ResolveHost is the deployment a domain names.
func (v *Views) ResolveHost(ctx context.Context, fqdn string) (string, string, error) {
	r, err := v.reach.Now(ctx)
	if err != nil {
		return "", "", err
	}
	return r.ResolveHost(ctx, fqdn)
}

// ResolveFleet is the cloud's own document.
func (v *Views) ResolveFleet(ctx context.Context) (pkgregister.Pointer, error) {
	r, err := v.reach.Now(ctx)
	if err != nil {
		return pkgregister.Pointer{}, err
	}
	return r.ResolveFleet(ctx)
}

// Close releases the stream to a register elsewhere, if one was opened.
func (v *Views) Close() error { return v.reach.Close() }
