package instance

import (
	"context"
	"errors"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/cm"
)

// Wires the link between the regions of a cloud: the end a deployment hands a node
// (WithRegionLink), what the cm of its region answers a carried call with, and how
// the gateway reaches a handler only other regions run (services/cm's Regions).

// ErrNoPlacement is a carried call on a node that places no request on a worker.
var ErrNoPlacement = errors.New("node: this node places no request on a worker")

// WithRegionLink is this node's end of the link between its cloud's regions, which
// the cm beside it carries calls over. Without it this node's cm links no region.
func WithRegionLink(l cm.Link) Option {
	return func(o *options) error {
		if l == nil {
			return errors.New("node: nil region link")
		}
		o.link = l
		return nil
	}
}

// regionOrigins is what this node's region answers a call carried to it with: the
// register as the node reads it, and its workers.
type regionOrigins struct{ n *Instance }

var _ cm.Origins = regionOrigins{}

func (o regionOrigins) Resolve(ctx context.Context, project, environment string) (register.Pointer, error) {
	return o.n.views.Resolve(ctx, project, environment)
}

func (o regionOrigins) Object(ctx context.Context, id register.ObjectID) ([]byte, error) {
	return o.n.views.Object(ctx, id)
}

func (o regionOrigins) Routing(ctx context.Context, project, environment string) (register.Routing, error) {
	return o.n.views.Routing(ctx, project, environment)
}

func (o regionOrigins) Invoke(ctx context.Context, at specs.Address, req *invoke.Request) (*invoke.Response, error) {
	if o.n.workers == nil {
		return nil, ErrNoPlacement
	}
	return o.n.workers.Invoke(ctx, at, req)
}
