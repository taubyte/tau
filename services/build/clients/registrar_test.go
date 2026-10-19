package clients

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/discovery"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
	"github.com/taubyte/tau/services/build/frontend"
)

var _ frontend.Registrar = (*RegistrarClient)(nil)

type registeringRepositories struct {
	fakeRepositories
	registered []string
}

func (f *registeringRepositories) Register(_ context.Context, req *connect.Request[authv2.RepositoryRef]) (*connect.Response[authv2.Empty], error) {
	f.registered = append(f.registered, req.Msg.GetRepositoryKey())
	return connect.NewResponse(&authv2.Empty{}), nil
}

type creatingProjects struct {
	fakeProjects
	created *authv2.CreateProjectRequest
}

func (f *creatingProjects) Create(_ context.Context, req *connect.Request[authv2.CreateProjectRequest]) (*connect.Response[authv2.CreateProjectResponse], error) {
	f.created = req.Msg
	if req.Msg.GetName() == "taken" {
		return nil, connect.NewError(connect.CodePermissionDenied, nil)
	}
	return connect.NewResponse(&authv2.CreateProjectResponse{Id: "proj-1", Name: req.Msg.GetName()}), nil
}

func TestRegistrarForwardsBothWrites(t *testing.T) {
	mn := newMocknet(t)
	caller, server := newHost(t, mn), newHost(t, mn)
	repos, projects := &registeringRepositories{}, &creatingProjects{}
	mux := http.NewServeMux()
	mux.Handle(authv2connect.NewRepositoryServiceHandler(repos))
	mux.Handle(authv2connect.NewProjectServiceHandler(projects))
	serve(t, server, AuthProtocol, mux)

	p, err := NewPool(caller, discovery.Static(server.ID()), AuthProtocol, 2)
	require.NoError(t, err)
	t.Cleanup(p.Close)
	c, err := Registrar(p)
	require.NoError(t, err)

	require.NoError(t, c.Register(t.Context(), "github.com/acme/pay"))
	require.Equal(t, []string{"github.com/acme/pay"}, repos.registered)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(c.Register(t.Context(), "")))

	id, err := c.Create(t.Context(), "pay", "github.com/acme/cfg", "github.com/acme/pay")
	require.NoError(t, err)
	require.Equal(t, "proj-1", id)
	require.Equal(t, "github.com/acme/cfg", projects.created.GetConfigRepositoryKey())

	_, err = c.Create(t.Context(), "taken", "a", "b")
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestRegistrarRefusesAnotherProtocol(t *testing.T) {
	_, err := Registrar(nil)
	require.Error(t, err)
}
