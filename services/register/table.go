package register

import (
	"context"

	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/rpc"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

// Table is the register as the build service reads and writes it: the publish a
// deploy makes with the reads it makes around it.
type Table interface {
	Publish(ctx context.Context, p pkgregister.Publication) (pkgregister.ObjectID, error)
	Environments(ctx context.Context, project string) ([]string, error)
	Resolve(ctx context.Context, project, environment string) (pkgregister.Pointer, error)
	Object(ctx context.Context, id pkgregister.ObjectID) ([]byte, error)
	Manifest(ctx context.Context, project, environment string) (pkgregister.Manifest, pkgregister.ObjectID, error)
}

var (
	_ Table = (*pkgregister.Register)(nil)
	_ Table = (*Client)(nil)
)

// DialTable is a client of the register at id, as its table.
func DialTable(h host.Host, id libp2ppeer.ID) (Table, error) { return NewClient(h, id) }

// TableOn is the register's table as services on h reach it: the store of the register
// beside them, else a client of one discovery names.
func TableOn(h service.Host) *rpc.Reacher[Table] {
	return service.ReachAs(h, Protocol, func(s Service) Table { return s.Local() }, DialTable)
}
