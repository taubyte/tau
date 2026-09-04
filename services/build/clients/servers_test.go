package clients

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"

	authv1 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v1"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v1/authv1connect"
	buildv1 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v1"
	"github.com/taubyte/tau/pkg/proto/gen/tau/build/v1/buildv1connect"
	"github.com/taubyte/tau/services/build"
)

// The servers the frontend's three adapters are tested against. Same fixture as
// harness_test.go's: mocknet, discovery.Static, and no port anywhere.

// ── auth's HookService ───────────────────────────────────────────────────────

type fakeHooks struct {
	get func(id string) (*authv1.Hook, error)
}

func (f *fakeHooks) Get(_ context.Context, req *connect.Request[authv1.HookRef]) (*connect.Response[authv1.Hook], error) {
	hook, err := f.get(req.Msg.GetId())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(hook), nil
}

func (f *fakeHooks) List(context.Context, *connect.Request[authv1.Empty], *connect.ServerStream[authv1.HookRef]) error {
	return connect.NewError(connect.CodeUnimplemented, nil)
}

func serveHooks(t testing.TB, mn mocknet.Mocknet, get func(string) (*authv1.Hook, error)) host.Host {
	t.Helper()
	h := newHost(t, mn)
	mux := http.NewServeMux()
	mux.Handle(authv1connect.NewHookServiceHandler(&fakeHooks{get: get}))
	serve(t, h, AuthProtocol, mux)
	return h
}

// ── auth's ProjectService ────────────────────────────────────────────────────

type fakeProjects struct {
	get func(id string) (*authv1.Project, error)
}

func (f *fakeProjects) Get(_ context.Context, req *connect.Request[authv1.ProjectRef]) (*connect.Response[authv1.Project], error) {
	project, err := f.get(req.Msg.GetId())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(project), nil
}

func (f *fakeProjects) List(context.Context, *connect.Request[authv1.Empty], *connect.ServerStream[authv1.ProjectRef]) error {
	return connect.NewError(connect.CodeUnimplemented, nil)
}

func (f *fakeProjects) Create(context.Context, *connect.Request[authv1.CreateProjectRequest]) (*connect.Response[authv1.CreateProjectResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func serveProjects(t testing.TB, mn mocknet.Mocknet, get func(string) (*authv1.Project, error)) host.Host {
	t.Helper()
	h := newHost(t, mn)
	mux := http.NewServeMux()
	mux.Handle(authv1connect.NewProjectServiceHandler(&fakeProjects{get: get}))
	serve(t, h, AuthProtocol, mux)
	return h
}

// ── build's own backend ──────────────────────────────────────────────────────

// fakeBackend answers build.proto's five RPCs however a test says. A nil hook
// is an Unimplemented, so a test that forgot to set one fails loudly rather
// than passing on a zero value.
type fakeBackend struct {
	enqueue func(*buildv1.PushEvent) (string, error)
	get     func(id string) (*buildv1.Job, error)
	list    func(projectID string, send func(*buildv1.JobRef) error) error
	cancel  func(id string) error
	retry   func(id string) error
	follow  func(jobID string, offset int64, send func(*buildv1.LogChunk) error) error
}

func (f *fakeBackend) Follow(_ context.Context, req *connect.Request[buildv1.FollowRequest], stream *connect.ServerStream[buildv1.LogChunk]) error {
	if f.follow == nil {
		return connect.NewError(connect.CodeUnimplemented, nil)
	}
	return f.follow(req.Msg.GetJobId(), req.Msg.GetOffset(), stream.Send)
}

func (f *fakeBackend) Enqueue(_ context.Context, req *connect.Request[buildv1.PushEvent]) (*connect.Response[buildv1.JobRef], error) {
	if f.enqueue == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, nil)
	}
	id, err := f.enqueue(req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&buildv1.JobRef{Id: id}), nil
}

func (f *fakeBackend) Get(_ context.Context, req *connect.Request[buildv1.JobRef]) (*connect.Response[buildv1.Job], error) {
	if f.get == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, nil)
	}
	job, err := f.get(req.Msg.GetId())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(job), nil
}

func (f *fakeBackend) List(_ context.Context, req *connect.Request[buildv1.ProjectRef], stream *connect.ServerStream[buildv1.JobRef]) error {
	if f.list == nil {
		return connect.NewError(connect.CodeUnimplemented, nil)
	}
	return f.list(req.Msg.GetId(), stream.Send)
}

func (f *fakeBackend) Cancel(_ context.Context, req *connect.Request[buildv1.JobRef]) (*connect.Response[buildv1.Empty], error) {
	if f.cancel == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, nil)
	}
	if err := f.cancel(req.Msg.GetId()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&buildv1.Empty{}), nil
}

func (f *fakeBackend) Retry(_ context.Context, req *connect.Request[buildv1.JobRef]) (*connect.Response[buildv1.Empty], error) {
	if f.retry == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, nil)
	}
	if err := f.retry(req.Msg.GetId()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&buildv1.Empty{}), nil
}

func serveBuild(t testing.TB, mn mocknet.Mocknet, backend *fakeBackend) host.Host {
	t.Helper()
	h := newHost(t, mn)
	mux := http.NewServeMux()
	mux.Handle(buildv1connect.NewQueueServiceHandler(backend))
	mux.Handle(buildv1connect.NewJobServiceHandler(backend))
	serve(t, h, build.Protocol, mux)
	return h
}
