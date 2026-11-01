package register

import (
	"context"
	"time"

	"connectrpc.com/connect"

	pkgregister "github.com/taubyte/tau/pkg/register"

	registerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/register/v2"
)

// Authorization is not here: whether the claimant may write to the repositories it
// names is the build frontend's to establish before it calls Link.

func (h *handler) Link(ctx context.Context, req *connect.Request[registerv2.LinkRequest]) (*connect.Response[registerv2.ProjectRecord], error) {
	rec, err := h.s.reg.LinkProject(ctx, pkgregister.Link{
		Project:  req.Msg.GetProject(),
		Config:   req.Msg.GetConfigRepository(),
		Code:     req.Msg.GetCodeRepositories(),
		Claimant: req.Msg.GetClaimant(),
		Replace:  req.Msg.GetReplace(),
	})
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(recordToWire(rec)), nil
}

func (h *handler) Project(ctx context.Context, req *connect.Request[registerv2.ProjectRequest]) (*connect.Response[registerv2.ProjectRecord], error) {
	rec, err := h.s.reg.Project(ctx, req.Msg.GetProject())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(recordToWire(rec)), nil
}

func (h *handler) Repository(ctx context.Context, req *connect.Request[registerv2.RepositoryRequest]) (*connect.Response[registerv2.RepositoryRecord], error) {
	rec, err := h.s.reg.Repository(ctx, req.Msg.GetRepository())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&registerv2.RepositoryRecord{
		Project: rec.Project, Repository: rec.Repository, Configures: rec.Configures,
	}), nil
}

func (h *handler) ProjectsOf(ctx context.Context, req *connect.Request[registerv2.ClaimantRequest]) (*connect.Response[registerv2.ProjectRecords], error) {
	recs, err := h.s.reg.ProjectsOf(ctx, req.Msg.GetClaimant())
	if err != nil {
		return nil, wire(err)
	}
	out := &registerv2.ProjectRecords{}
	for _, rec := range recs {
		out.Projects = append(out.Projects, recordToWire(rec))
	}
	return connect.NewResponse(out), nil
}

func recordToWire(rec pkgregister.ProjectRecord) *registerv2.ProjectRecord {
	return &registerv2.ProjectRecord{
		Id:               rec.ID,
		ConfigRepository: rec.Config,
		CodeRepositories: rec.Code,
		Claimant:         rec.Claimant,
		CreatedAt:        rec.CreatedAt.UnixNano(),
	}
}

func recordFromWire(rec *registerv2.ProjectRecord) pkgregister.ProjectRecord {
	return pkgregister.ProjectRecord{
		ID:        rec.GetId(),
		Config:    rec.GetConfigRepository(),
		Code:      rec.GetCodeRepositories(),
		Claimant:  rec.GetClaimant(),
		CreatedAt: time.Unix(0, rec.GetCreatedAt()).UTC(),
	}
}

// LinkProject claims a project id over the wire.
func (c *Client) LinkProject(ctx context.Context, l pkgregister.Link) (pkgregister.ProjectRecord, error) {
	res, err := c.svc.Link(ctx, connect.NewRequest(&registerv2.LinkRequest{
		Project: l.Project, ConfigRepository: l.Config, CodeRepositories: l.Code, Claimant: l.Claimant, Replace: l.Replace,
	}))
	if err != nil {
		return pkgregister.ProjectRecord{}, unwire(err)
	}
	return recordFromWire(res.Msg), nil
}

// Project is one linked project.
func (c *Client) Project(ctx context.Context, id string) (pkgregister.ProjectRecord, error) {
	res, err := c.svc.Project(ctx, connect.NewRequest(&registerv2.ProjectRequest{Project: id}))
	if err != nil {
		return pkgregister.ProjectRecord{}, unwire(err)
	}
	return recordFromWire(res.Msg), nil
}

// Repository is which project owns a repository.
func (c *Client) Repository(ctx context.Context, repository string) (pkgregister.RepositoryRecord, error) {
	res, err := c.svc.Repository(ctx, connect.NewRequest(&registerv2.RepositoryRequest{Repository: repository}))
	if err != nil {
		return pkgregister.RepositoryRecord{}, unwire(err)
	}
	return pkgregister.RepositoryRecord{
		Project: res.Msg.GetProject(), Repository: res.Msg.GetRepository(), Configures: res.Msg.GetConfigures(),
	}, nil
}

// ProjectsOf is one claimant's projects.
func (c *Client) ProjectsOf(ctx context.Context, claimant string) ([]pkgregister.ProjectRecord, error) {
	res, err := c.svc.ProjectsOf(ctx, connect.NewRequest(&registerv2.ClaimantRequest{Claimant: claimant}))
	if err != nil {
		return nil, unwire(err)
	}
	var out []pkgregister.ProjectRecord
	for _, rec := range res.Msg.GetProjects() {
		out = append(out, recordFromWire(rec))
	}
	return out, nil
}

func (h *handler) ListProjects(ctx context.Context, _ *connect.Request[registerv2.ListProjectsRequest]) (*connect.Response[registerv2.ProjectRecords], error) {
	recs, err := h.s.reg.ListProjects(ctx)
	if err != nil {
		return nil, wire(err)
	}
	out := &registerv2.ProjectRecords{}
	for _, rec := range recs {
		out.Projects = append(out.Projects, recordToWire(rec))
	}
	return connect.NewResponse(out), nil
}

// ListProjects is every linked project.
func (c *Client) ListProjects(ctx context.Context) ([]pkgregister.ProjectRecord, error) {
	res, err := c.svc.ListProjects(ctx, connect.NewRequest(&registerv2.ListProjectsRequest{}))
	if err != nil {
		return nil, unwire(err)
	}
	var out []pkgregister.ProjectRecord
	for _, rec := range res.Msg.GetProjects() {
		out = append(out, recordFromWire(rec))
	}
	return out, nil
}

func (h *handler) Grant(ctx context.Context, req *connect.Request[registerv2.GrantRequest]) (*connect.Response[registerv2.GrantRecord], error) {
	g, err := h.s.reg.GrantRole(ctx, req.Msg.GetProject(), req.Msg.GetGrantee(), pkgregister.Role(req.Msg.GetRole()), req.Msg.GetBy())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(grantToWire(g)), nil
}

func (h *handler) Revoke(ctx context.Context, req *connect.Request[registerv2.RevokeRequest]) (*connect.Response[registerv2.RevokeResponse], error) {
	if err := h.s.reg.Revoke(ctx, req.Msg.GetProject(), req.Msg.GetGrantId(), req.Msg.GetBy()); err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&registerv2.RevokeResponse{}), nil
}

func (h *handler) Grants(ctx context.Context, req *connect.Request[registerv2.ProjectRequest]) (*connect.Response[registerv2.GrantRecords], error) {
	grants, err := h.s.reg.Grants(ctx, req.Msg.GetProject())
	if err != nil {
		return nil, wire(err)
	}
	out := &registerv2.GrantRecords{}
	for _, g := range grants {
		out.Grants = append(out.Grants, grantToWire(g))
	}
	return connect.NewResponse(out), nil
}

func (h *handler) Role(ctx context.Context, req *connect.Request[registerv2.RoleRequest]) (*connect.Response[registerv2.RoleAnswer], error) {
	role, err := h.s.reg.RoleOf(ctx, req.Msg.GetProject(), req.Msg.GetUser())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&registerv2.RoleAnswer{Role: string(role)}), nil
}

func grantToWire(g pkgregister.Grant) *registerv2.GrantRecord {
	return &registerv2.GrantRecord{Id: g.ID, Project: g.Project, Grantee: g.Grantee, Role: string(g.Role), By: g.By, At: g.At.UnixNano()}
}

func grantFromWire(g *registerv2.GrantRecord) pkgregister.Grant {
	return pkgregister.Grant{
		ID: g.GetId(), Project: g.GetProject(), Grantee: g.GetGrantee(), Role: pkgregister.Role(g.GetRole()),
		By: g.GetBy(), At: time.Unix(0, g.GetAt()).UTC(),
	}
}

// GrantRole gives a tau user a role on a project over the wire.
func (c *Client) GrantRole(ctx context.Context, project, grantee string, role pkgregister.Role, by string) (pkgregister.Grant, error) {
	res, err := c.svc.Grant(ctx, connect.NewRequest(&registerv2.GrantRequest{Project: project, Grantee: grantee, Role: string(role), By: by}))
	if err != nil {
		return pkgregister.Grant{}, unwire(err)
	}
	return grantFromWire(res.Msg), nil
}

// Revoke withdraws a grant over the wire.
func (c *Client) Revoke(ctx context.Context, project, id, by string) error {
	_, err := c.svc.Revoke(ctx, connect.NewRequest(&registerv2.RevokeRequest{Project: project, GrantId: id, By: by}))
	return unwire(err)
}

// Grants is a project's live grants.
func (c *Client) Grants(ctx context.Context, project string) ([]pkgregister.Grant, error) {
	res, err := c.svc.Grants(ctx, connect.NewRequest(&registerv2.ProjectRequest{Project: project}))
	if err != nil {
		return nil, unwire(err)
	}
	var out []pkgregister.Grant
	for _, g := range res.Msg.GetGrants() {
		out = append(out, grantFromWire(g))
	}
	return out, nil
}

// RoleOf is one user's role on a project.
func (c *Client) RoleOf(ctx context.Context, project, user string) (pkgregister.Role, error) {
	res, err := c.svc.Role(ctx, connect.NewRequest(&registerv2.RoleRequest{Project: project, User: user}))
	if err != nil {
		return pkgregister.RoleNone, unwire(err)
	}
	return pkgregister.Role(res.Msg.GetRole()), nil
}
