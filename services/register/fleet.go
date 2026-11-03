package register

import (
	"context"

	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	pkgregister "github.com/taubyte/tau/pkg/register"
)

// Fleet is the slice of a register that reads the cloud's own document.
type Fleet interface {
	ResolveFleet(ctx context.Context) (pkgregister.Pointer, error)
	Object(ctx context.Context, id pkgregister.ObjectID) ([]byte, error)
}

var (
	_ Fleet = (*pkgregister.Register)(nil)
	_ Fleet = (*Client)(nil)
)

// DialFleet is a client of the register at id, as the fleet's reader.
func DialFleet(h host.Host, id libp2ppeer.ID) (Fleet, error) { return NewClient(h, id) }
