package register

import (
	"context"
	"time"

	"connectrpc.com/connect"

	pkgregister "github.com/taubyte/tau/pkg/register"

	registerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/register/v2"
)

// projects.go is the project table's wire surface: pkg/register's projects.go
// over four calls, each a decode, a call and the shared code table.
//
// AUTHORIZATION IS NOT HERE either. Whether the claimant may write to the
// repositories it names is the build frontend's to establish before it calls
// Link; this service records what an authorized caller asked for. That is the
// same OSS trust model Publish states.

func (h *handler) Link(ctx context.Context, req *connect.Request[registerv2.LinkRequest]) (*connect.Response[registerv2.ProjectRecord], error) {
	rec, err := h.s.reg.LinkProject(ctx, pkgregister.Link{
		Project:  req.Msg.GetProject(),
		Config:   req.Msg.GetConfigRepository(),
		Code:     req.Msg.GetCodeRepositories(),
		Claimant: req.Msg.GetClaimant(),
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
		Project: l.Project, ConfigRepository: l.Config, CodeRepositories: l.Code, Claimant: l.Claimant,
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
