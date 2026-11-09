package projects

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/codec"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

// Serve mounts ProjectService over p.
func Serve(mux *http.ServeMux, p Projects) {
	mux.Handle(authv2connect.NewProjectServiceHandler(handler{p}, codec.Option()))
}

type handler struct{ p Projects }

var _ authv2connect.ProjectServiceHandler = handler{}

func (h handler) Link(ctx context.Context, req *connect.Request[authv2.LinkRequest]) (*connect.Response[authv2.ProjectRecord], error) {
	rec, err := h.p.LinkProject(ctx, Link{
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

func (h handler) Project(ctx context.Context, req *connect.Request[authv2.ProjectRequest]) (*connect.Response[authv2.ProjectRecord], error) {
	rec, err := h.p.Project(ctx, req.Msg.GetProject())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(recordToWire(rec)), nil
}

func (h handler) Repository(ctx context.Context, req *connect.Request[authv2.RepositoryRequest]) (*connect.Response[authv2.RepositoryRecord], error) {
	rec, err := h.p.Repository(ctx, req.Msg.GetRepository())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&authv2.RepositoryRecord{
		Project: rec.Project, Repository: rec.Repository, Configures: rec.Configures,
	}), nil
}

func (h handler) ProjectsOf(ctx context.Context, req *connect.Request[authv2.ClaimantRequest]) (*connect.Response[authv2.ProjectRecords], error) {
	recs, err := h.p.ProjectsOf(ctx, req.Msg.GetClaimant())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(recordsToWire(recs)), nil
}

func (h handler) ListProjects(ctx context.Context, _ *connect.Request[authv2.ListProjectsRequest]) (*connect.Response[authv2.ProjectRecords], error) {
	recs, err := h.p.ListProjects(ctx)
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(recordsToWire(recs)), nil
}

func (h handler) Grant(ctx context.Context, req *connect.Request[authv2.GrantRequest]) (*connect.Response[authv2.GrantRecord], error) {
	g, err := h.p.GrantRole(ctx, req.Msg.GetProject(), req.Msg.GetGrantee(), Role(req.Msg.GetRole()), req.Msg.GetBy())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(grantToWire(g)), nil
}

func (h handler) Revoke(ctx context.Context, req *connect.Request[authv2.RevokeRequest]) (*connect.Response[authv2.RevokeResponse], error) {
	if err := h.p.Revoke(ctx, req.Msg.GetProject(), req.Msg.GetGrantId(), req.Msg.GetBy()); err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&authv2.RevokeResponse{}), nil
}

func (h handler) Grants(ctx context.Context, req *connect.Request[authv2.ProjectRequest]) (*connect.Response[authv2.GrantRecords], error) {
	grants, err := h.p.Grants(ctx, req.Msg.GetProject())
	if err != nil {
		return nil, wire(err)
	}
	out := &authv2.GrantRecords{}
	for _, g := range grants {
		out.Grants = append(out.Grants, grantToWire(g))
	}
	return connect.NewResponse(out), nil
}

func (h handler) Role(ctx context.Context, req *connect.Request[authv2.RoleRequest]) (*connect.Response[authv2.RoleAnswer], error) {
	role, err := h.p.RoleOf(ctx, req.Msg.GetProject(), req.Msg.GetUser())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&authv2.RoleAnswer{Role: string(role)}), nil
}

func recordToWire(rec ProjectRecord) *authv2.ProjectRecord {
	return &authv2.ProjectRecord{
		Id:               rec.ID,
		ConfigRepository: rec.Config,
		CodeRepositories: rec.Code,
		Claimant:         rec.Claimant,
		CreatedAt:        rec.CreatedAt.UnixNano(),
	}
}

func recordsToWire(recs []ProjectRecord) *authv2.ProjectRecords {
	out := &authv2.ProjectRecords{}
	for _, rec := range recs {
		out.Projects = append(out.Projects, recordToWire(rec))
	}
	return out
}

func recordFromWire(rec *authv2.ProjectRecord) ProjectRecord {
	return ProjectRecord{
		ID:        rec.GetId(),
		Config:    rec.GetConfigRepository(),
		Code:      rec.GetCodeRepositories(),
		Claimant:  rec.GetClaimant(),
		CreatedAt: time.Unix(0, rec.GetCreatedAt()).UTC(),
	}
}

func recordsFromWire(recs *authv2.ProjectRecords) []ProjectRecord {
	var out []ProjectRecord
	for _, rec := range recs.GetProjects() {
		out = append(out, recordFromWire(rec))
	}
	return out
}

func grantToWire(g Grant) *authv2.GrantRecord {
	return &authv2.GrantRecord{Id: g.ID, Project: g.Project, Grantee: g.Grantee, Role: string(g.Role), By: g.By, At: g.At.UnixNano()}
}

func grantFromWire(g *authv2.GrantRecord) Grant {
	return Grant{
		ID: g.GetId(), Project: g.GetProject(), Grantee: g.GetGrantee(), Role: Role(g.GetRole()),
		By: g.GetBy(), At: time.Unix(0, g.GetAt()).UTC(),
	}
}

// at is the table on another node.
type at struct {
	conn *rpc.Client
	svc  authv2connect.ProjectServiceClient
}

// Dial is a client of the table on id.
func Dial(h host.Host, id libp2ppeer.ID) (Projects, error) {
	conn, err := rpc.NewClient(h, id, Protocol)
	if err != nil {
		return nil, fmt.Errorf("auth: dialling %s: %w", id, err)
	}
	return at{conn: conn, svc: authv2connect.NewProjectServiceClient(conn.HTTP(), conn.URL(), codec.Option())}, nil
}

func (a at) Close() error {
	a.conn.Close()
	return nil
}

func (a at) LinkProject(ctx context.Context, l Link) (ProjectRecord, error) {
	res, err := a.svc.Link(ctx, connect.NewRequest(&authv2.LinkRequest{
		Project: l.Project, ConfigRepository: l.Config, CodeRepositories: l.Code, Claimant: l.Claimant, Replace: l.Replace,
	}))
	if err != nil {
		return ProjectRecord{}, unwire(err)
	}
	return recordFromWire(res.Msg), nil
}

func (a at) Project(ctx context.Context, id string) (ProjectRecord, error) {
	res, err := a.svc.Project(ctx, connect.NewRequest(&authv2.ProjectRequest{Project: id}))
	if err != nil {
		return ProjectRecord{}, unwire(err)
	}
	return recordFromWire(res.Msg), nil
}

func (a at) Repository(ctx context.Context, repository string) (RepositoryRecord, error) {
	res, err := a.svc.Repository(ctx, connect.NewRequest(&authv2.RepositoryRequest{Repository: repository}))
	if err != nil {
		return RepositoryRecord{}, unwire(err)
	}
	return RepositoryRecord{
		Project: res.Msg.GetProject(), Repository: res.Msg.GetRepository(), Configures: res.Msg.GetConfigures(),
	}, nil
}

func (a at) ProjectsOf(ctx context.Context, claimant string) ([]ProjectRecord, error) {
	res, err := a.svc.ProjectsOf(ctx, connect.NewRequest(&authv2.ClaimantRequest{Claimant: claimant}))
	if err != nil {
		return nil, unwire(err)
	}
	return recordsFromWire(res.Msg), nil
}

func (a at) ListProjects(ctx context.Context) ([]ProjectRecord, error) {
	res, err := a.svc.ListProjects(ctx, connect.NewRequest(&authv2.ListProjectsRequest{}))
	if err != nil {
		return nil, unwire(err)
	}
	return recordsFromWire(res.Msg), nil
}

func (a at) GrantRole(ctx context.Context, project, grantee string, role Role, by string) (Grant, error) {
	res, err := a.svc.Grant(ctx, connect.NewRequest(&authv2.GrantRequest{Project: project, Grantee: grantee, Role: string(role), By: by}))
	if err != nil {
		return Grant{}, unwire(err)
	}
	return grantFromWire(res.Msg), nil
}

func (a at) Revoke(ctx context.Context, project, id, by string) error {
	_, err := a.svc.Revoke(ctx, connect.NewRequest(&authv2.RevokeRequest{Project: project, GrantId: id, By: by}))
	return unwire(err)
}

func (a at) Grants(ctx context.Context, project string) ([]Grant, error) {
	res, err := a.svc.Grants(ctx, connect.NewRequest(&authv2.ProjectRequest{Project: project}))
	if err != nil {
		return nil, unwire(err)
	}
	var out []Grant
	for _, g := range res.Msg.GetGrants() {
		out = append(out, grantFromWire(g))
	}
	return out, nil
}

func (a at) RoleOf(ctx context.Context, project, user string) (Role, error) {
	res, err := a.svc.Role(ctx, connect.NewRequest(&authv2.RoleRequest{Project: project, User: user}))
	if err != nil {
		return RoleNone, unwire(err)
	}
	return Role(res.Msg.GetRole()), nil
}

// wire is the one code table from a table failure to a status. The default is
// Unavailable: anything not named came out of the store, which a caller retries
// elsewhere.
func wire(err error) error {
	switch {
	case errors.Is(err, ErrNoProject),
		errors.Is(err, ErrSeparator),
		errors.Is(err, ErrNoRepository),
		errors.Is(err, ErrNoClaimant),
		errors.Is(err, ErrNoRole),
		errors.Is(err, ErrNoGrantee):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, ErrNotLinked),
		errors.Is(err, ErrNoGrant):
		return connect.NewError(connect.CodeNotFound, err)
	// An id or a repository another project holds is a well-formed link the state
	// refuses, and the same link will never succeed.
	case errors.Is(err, ErrProjectClaimed),
		errors.Is(err, ErrRepositoryClaimed):
		return connect.NewError(connect.CodeAlreadyExists, err)
	default:
		return connect.NewError(connect.CodeUnavailable, err)
	}
}

// unwire turns a status back into the sentinel the local table would have
// returned, so a caller has one error path whichever node answered. It matches the
// message as well as the code, because sentinels share codes.
func unwire(err error) error {
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		return err
	}
	var candidates []error
	switch cerr.Code() {
	case connect.CodeInvalidArgument:
		candidates = []error{ErrNoProject, ErrSeparator, ErrNoRepository, ErrNoClaimant, ErrNoRole, ErrNoGrantee}
	case connect.CodeNotFound:
		candidates = []error{ErrNotLinked, ErrNoGrant}
	case connect.CodeAlreadyExists:
		candidates = []error{ErrProjectClaimed, ErrRepositoryClaimed}
	}
	for _, c := range candidates {
		if strings.HasPrefix(cerr.Message(), c.Error()) {
			return fmt.Errorf("%w: %s", c, cerr.Message())
		}
	}
	return err
}
