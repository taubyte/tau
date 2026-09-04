package clients

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/protocol"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	authv1 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v1"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v1/authv1connect"
)

// This package's fixture is mocknet and nothing else: no DHT, no datastore, no
// port. discovery.Static supplies the peers, which is what
// p2p/discovery's own doc says a service test should inject when the question
// under test is not discovery.

func newMocknet(t testing.TB) mocknet.Mocknet {
	t.Helper()
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	return mn
}

func newHost(t testing.TB, mn mocknet.Mocknet) host.Host {
	t.Helper()
	h, err := mn.GenPeer()
	require.NoError(t, err)
	require.NoError(t, mn.LinkAll())
	return h
}

// fakeRepositories is an auth RepositoryService that answers Get however the
// test says and refuses everything else.
type fakeRepositories struct {
	get func(key string) (*authv1.Repository, error)
}

func (f *fakeRepositories) Get(_ context.Context, req *connect.Request[authv1.RepositoryRef]) (*connect.Response[authv1.Repository], error) {
	r, err := f.get(req.Msg.GetRepositoryKey())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(r), nil
}

func (f *fakeRepositories) List(context.Context, *connect.Request[authv1.Empty], *connect.ServerStream[authv1.RepositoryRef]) error {
	return connect.NewError(connect.CodeUnimplemented, nil)
}

func (f *fakeRepositories) Register(context.Context, *connect.Request[authv1.RepositoryRef]) (*connect.Response[authv1.Empty], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (f *fakeRepositories) Unregister(context.Context, *connect.Request[authv1.RepositoryRef]) (*connect.Response[authv1.Empty], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

// serveAuth mounts a RepositoryService on a fresh mocknet host and returns it.
func serveAuth(t testing.TB, mn mocknet.Mocknet, get func(string) (*authv1.Repository, error)) host.Host {
	t.Helper()
	h := newHost(t, mn)
	mux := http.NewServeMux()
	mux.Handle(authv1connect.NewRepositoryServiceHandler(&fakeRepositories{get: get}))
	serve(t, h, AuthProtocol, mux)
	return h
}

func serve(t testing.TB, h host.Host, id protocol.ID, handler http.Handler) {
	t.Helper()
	srv, err := rpc.Listen(t.Context(), h, id, handler)
	require.NoError(t, err)
	t.Cleanup(func() { srv.Close() })
}
