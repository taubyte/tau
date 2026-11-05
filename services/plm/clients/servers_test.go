package clients

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"

	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2/plmv2connect"
	"github.com/taubyte/tau/services/plm"
)

// The plm nodes ingress's client is tested against: mocknet,
// discovery.Static, no port.

// fakeBackend answers plm.proto's five RPCs however a test says. A nil hook
// is an Unimplemented, so a test that forgot to set one fails loudly rather
// than passing on a zero value.
type fakeBuild struct {
	enqueue func(*plmv2.PushEvent) (string, error)
	get     func(id string) (*plmv2.Job, error)
	list    func(projectID string, send func(*plmv2.JobRef) error) error
	cancel  func(id string) error
	retry   func(id string) error
	follow  func(jobID string, offset int64, send func(*plmv2.Job_Event) error) error
	// snapshot answers a Follow that asks for one, so a test can tell the two
	// requests apart.
	snapshot func(jobID string, offset int64, send func(*plmv2.Job_Event) error) error
	built    func(project, environment, resource string) (string, error)

	mu sync.Mutex
	// runs is the attempt each Follow asked for, in order.
	runs []int32
}

func (f *fakeBuild) asked() []int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int32(nil), f.runs...)
}

func (f *fakeBuild) Artifact(_ context.Context, req *connect.Request[plmv2.ArtifactRequest]) (*connect.Response[plmv2.Built], error) {
	if f.built == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, nil)
	}
	cid, err := f.built(req.Msg.GetProject(), req.Msg.GetEnvironment(), req.Msg.GetResource())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&plmv2.Built{Cid: cid}), nil
}

func (f *fakeBuild) Follow(_ context.Context, req *connect.Request[plmv2.FollowRequest], stream *connect.ServerStream[plmv2.Job_Event]) error {
	f.mu.Lock()
	f.runs = append(f.runs, req.Msg.GetAttempt())
	f.mu.Unlock()
	read := f.follow
	if req.Msg.GetSnapshot() {
		read = f.snapshot
	}
	if read == nil {
		return connect.NewError(connect.CodeUnimplemented, nil)
	}
	return read(req.Msg.GetJobId(), req.Msg.GetOffset(), stream.Send)
}

func (f *fakeBuild) Enqueue(_ context.Context, req *connect.Request[plmv2.PushEvent]) (*connect.Response[plmv2.JobRef], error) {
	if f.enqueue == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, nil)
	}
	id, err := f.enqueue(req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&plmv2.JobRef{Id: id}), nil
}

func (f *fakeBuild) Get(_ context.Context, req *connect.Request[plmv2.JobRef]) (*connect.Response[plmv2.Job], error) {
	if f.get == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, nil)
	}
	job, err := f.get(req.Msg.GetId())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(job), nil
}

func (f *fakeBuild) List(_ context.Context, req *connect.Request[plmv2.ProjectRef], stream *connect.ServerStream[plmv2.JobRef]) error {
	if f.list == nil {
		return connect.NewError(connect.CodeUnimplemented, nil)
	}
	return f.list(req.Msg.GetId(), stream.Send)
}

func (f *fakeBuild) Cancel(_ context.Context, req *connect.Request[plmv2.JobRef]) (*connect.Response[plmv2.Empty], error) {
	if f.cancel == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, nil)
	}
	if err := f.cancel(req.Msg.GetId()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&plmv2.Empty{}), nil
}

func (f *fakeBuild) Retry(_ context.Context, req *connect.Request[plmv2.JobRef]) (*connect.Response[plmv2.Empty], error) {
	if f.retry == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, nil)
	}
	if err := f.retry(req.Msg.GetId()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&plmv2.Empty{}), nil
}

func serveBuild(t testing.TB, mn mocknet.Mocknet, node *fakeBuild) host.Host {
	t.Helper()
	h := newHost(t, mn)
	mux := http.NewServeMux()
	mux.Handle(plmv2connect.NewQueueServiceHandler(node))
	mux.Handle(plmv2connect.NewJobServiceHandler(node))
	serve(t, h, plm.Protocol, mux)
	return h
}
