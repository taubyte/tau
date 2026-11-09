package projects

import (
	"context"

	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

// Protocol is auth's, which serves the table beside its users.
var Protocol protocol.ID = rpc.Protocol(authv2connect.ProjectServiceName)

// Projects is the project table as its readers and writers hold it: a Table on
// the node that keeps it, a client anywhere else.
type Projects interface {
	LinkProject(ctx context.Context, l Link) (ProjectRecord, error)
	Project(ctx context.Context, id string) (ProjectRecord, error)
	Repository(ctx context.Context, repository string) (RepositoryRecord, error)
	ProjectsOf(ctx context.Context, claimant string) ([]ProjectRecord, error)
	ListProjects(ctx context.Context) ([]ProjectRecord, error)
	GrantRole(ctx context.Context, project, grantee string, role Role, by string) (Grant, error)
	Revoke(ctx context.Context, project, id, by string) error
	Grants(ctx context.Context, project string) ([]Grant, error)
	RoleOf(ctx context.Context, project, user string) (Role, error)
}

var (
	_ Projects = (*Table)(nil)
	_ Projects = at{}
	_ Projects = (*Client)(nil)
)

// Keeper is a service that keeps the table: auth.
type Keeper interface {
	Projects() Projects
}

// Client is the table as a service reaches it: the one auth keeps beside it when
// auth runs on its node, else one on a node discovery names, waited for while none
// answers yet.
type Client struct {
	reach *rpc.Reacher[Projects]
}

// On is the table as services on h reach it.
func On(h service.Host) *Client {
	return &Client{reach: service.ReachAs(h, Protocol, Keeper.Projects, Dial)}
}

// Close releases the stream to auth on another node.
func (c *Client) Close() error { return c.reach.Close() }

// do runs fn on the table, retrying while no node answers.
func do[T any](ctx context.Context, c *Client, fn func(Projects) (T, error)) (T, error) {
	var out T
	err := c.reach.Do(ctx, func(p Projects) error {
		var err error
		out, err = fn(p)
		return err
	})
	return out, err
}

func (c *Client) LinkProject(ctx context.Context, l Link) (ProjectRecord, error) {
	return do(ctx, c, func(p Projects) (ProjectRecord, error) { return p.LinkProject(ctx, l) })
}

func (c *Client) Project(ctx context.Context, id string) (ProjectRecord, error) {
	return do(ctx, c, func(p Projects) (ProjectRecord, error) { return p.Project(ctx, id) })
}

func (c *Client) Repository(ctx context.Context, repository string) (RepositoryRecord, error) {
	return do(ctx, c, func(p Projects) (RepositoryRecord, error) { return p.Repository(ctx, repository) })
}

func (c *Client) ProjectsOf(ctx context.Context, claimant string) ([]ProjectRecord, error) {
	return do(ctx, c, func(p Projects) ([]ProjectRecord, error) { return p.ProjectsOf(ctx, claimant) })
}

func (c *Client) ListProjects(ctx context.Context) ([]ProjectRecord, error) {
	return do(ctx, c, func(p Projects) ([]ProjectRecord, error) { return p.ListProjects(ctx) })
}

func (c *Client) GrantRole(ctx context.Context, project, grantee string, role Role, by string) (Grant, error) {
	return do(ctx, c, func(p Projects) (Grant, error) { return p.GrantRole(ctx, project, grantee, role, by) })
}

func (c *Client) Revoke(ctx context.Context, project, id, by string) error {
	return c.reach.Do(ctx, func(p Projects) error { return p.Revoke(ctx, project, id, by) })
}

func (c *Client) Grants(ctx context.Context, project string) ([]Grant, error) {
	return do(ctx, c, func(p Projects) ([]Grant, error) { return p.Grants(ctx, project) })
}

func (c *Client) RoleOf(ctx context.Context, project, user string) (Role, error) {
	return do(ctx, c, func(p Projects) (Role, error) { return p.RoleOf(ctx, project, user) })
}
