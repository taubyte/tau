package register

import (
	"context"

	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/rpc"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

// Table is the register as the build service reads and writes it: the project table a
// link writes and a push is admitted by, and the publish a deploy makes with the
// reads it makes around it.
type Table interface {
	LinkProject(ctx context.Context, l pkgregister.Link) (pkgregister.ProjectRecord, error)
	Project(ctx context.Context, id string) (pkgregister.ProjectRecord, error)
	Repository(ctx context.Context, repository string) (pkgregister.RepositoryRecord, error)
	ProjectsOf(ctx context.Context, claimant string) ([]pkgregister.ProjectRecord, error)
	ListProjects(ctx context.Context) ([]pkgregister.ProjectRecord, error)
	GrantRole(ctx context.Context, project, grantee string, role pkgregister.Role, by string) (pkgregister.Grant, error)
	Revoke(ctx context.Context, project, id, by string) error
	Grants(ctx context.Context, project string) ([]pkgregister.Grant, error)
	RoleOf(ctx context.Context, project, user string) (pkgregister.Role, error)

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
