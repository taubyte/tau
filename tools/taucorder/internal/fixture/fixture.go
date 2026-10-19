// Package fixture is a small swarm for taucorder's unit tests: real libp2p
// hosts on loopback, serving the real generated v2 handlers over p2p/rpc,
// backed by in-memory fakes. It is imported only by tests.
package fixture

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/rpc"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/build/v2/buildv2connect"
	registerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/register/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/register/v2/registerv2connect"
)

// Host is a loopback libp2p host closed with the test.
func Host(t testing.TB) host.Host {
	t.Helper()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	return h
}

// Connect dials b from a.
func Connect(t testing.TB, a, b host.Host) {
	t.Helper()
	if err := a.Connect(context.Background(), peer.AddrInfo{ID: b.ID(), Addrs: b.Addrs()}); err != nil {
		t.Fatal(err)
	}
}

// Serve listens on h with what mount returns, on the protocol of its service:
//
//	fixture.Serve(t, h, cloud.Register)
func Serve(t testing.TB, h host.Host, mount func() (string, http.Handler)) protocol.ID {
	t.Helper()
	service, handler := mount()
	id := rpc.Protocol(service)
	s, err := rpc.Listen(context.Background(), h, id, handler)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return id
}

// Cloud is the fakes' state.
type Cloud struct {
	mu       sync.Mutex
	Projects map[string]string   // id -> name
	Envs     map[string][]string // project -> environments
	Jobs     map[string][]*buildv2.Job
	// Ghosts are job ids List names that Get does not know.
	Ghosts map[string][]string
	// Calls counts calls per method, by "Service/Method".
	Calls map[string]int
	// Published records Publish calls.
	Published []string
}

// NewCloud is a cloud with two projects and three jobs.
func NewCloud() *Cloud {
	return &Cloud{
		Projects: map[string]string{"p1": "shop", "p2": "blog"},
		Envs:     map[string][]string{"p1": {"main", "staging"}},
		Jobs: map[string][]*buildv2.Job{
			"p1": {
				{Id: "j1", Status: buildv2.JobStatus_JobStatusSuccess, RepositoryKey: "github/1", Ref: "refs/heads/main", CommitSha: "0123456789abcdef", Timestamp: 1700000000, Attempt: 1},
				{Id: "j2", Status: buildv2.JobStatus_JobStatusFailed, RepositoryKey: "github/1", Ref: "refs/heads/dev", CommitSha: "fedcba", Timestamp: 1700000100, Attempt: 2},
			},
			"p2": {
				{Id: "j3", Status: buildv2.JobStatus_JobStatusClaimed, RepositoryKey: "github/2", Ref: "refs/heads/main", CommitSha: "abc", Timestamp: 1700000050, Attempt: 1},
			},
		},
		Calls: map[string]int{},
	}
}

func (c *Cloud) count(name string) {
	c.mu.Lock()
	c.Calls[name]++
	c.mu.Unlock()
}

// Count is how often name was called.
func (c *Cloud) Count(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Calls[name]
}

// Register serves RegisterService.
func (c *Cloud) Register() (string, http.Handler) {
	_, h := registerv2connect.NewRegisterServiceHandler(&register{c: c})
	return registerv2connect.RegisterServiceName, h
}

// Auth serves ProjectService.
func (c *Cloud) Auth() (string, http.Handler) {
	_, h := authv2connect.NewProjectServiceHandler(&projects{c: c})
	return authv2connect.ProjectServiceName, h
}

// Build serves JobService.
func (c *Cloud) Build() (string, http.Handler) {
	_, h := buildv2connect.NewJobServiceHandler(&jobs{c: c})
	return buildv2connect.JobServiceName, h
}

type register struct {
	registerv2connect.UnimplementedRegisterServiceHandler
	c *Cloud
}

func (r *register) Environments(_ context.Context, req *connect.Request[registerv2.ProjectRequest]) (*connect.Response[registerv2.Deployments], error) {
	r.c.count("RegisterService/Environments")
	envs, ok := r.c.Envs[req.Msg.GetProject()]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no deployments for %s", req.Msg.GetProject()))
	}
	return connect.NewResponse(&registerv2.Deployments{Environments: envs}), nil
}

func (r *register) Publish(_ context.Context, req *connect.Request[registerv2.PublishRequest]) (*connect.Response[registerv2.PublishResponse], error) {
	r.c.count("RegisterService/Publish")
	r.c.mu.Lock()
	r.c.Published = append(r.c.Published, req.Msg.GetProject())
	r.c.mu.Unlock()
	return connect.NewResponse(&registerv2.PublishResponse{Object: "obj"}), nil
}

type projects struct {
	authv2connect.UnimplementedProjectServiceHandler
	c *Cloud
}

func (p *projects) List(_ context.Context, _ *connect.Request[authv2.Empty], s *connect.ServerStream[authv2.ProjectRef]) error {
	p.c.count("ProjectService/List")
	for _, id := range []string{"p2", "p1"} {
		if _, ok := p.c.Projects[id]; !ok {
			continue
		}
		if err := s.Send(&authv2.ProjectRef{Id: id}); err != nil {
			return err
		}
	}
	return nil
}

func (p *projects) Get(_ context.Context, req *connect.Request[authv2.ProjectRef]) (*connect.Response[authv2.Project], error) {
	p.c.count("ProjectService/Get")
	name, ok := p.c.Projects[req.Msg.GetId()]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no project %s", req.Msg.GetId()))
	}
	return connect.NewResponse(&authv2.Project{Id: req.Msg.GetId(), Name: name}), nil
}

type jobs struct {
	buildv2connect.UnimplementedJobServiceHandler
	c *Cloud
}

func (j *jobs) List(_ context.Context, req *connect.Request[buildv2.ProjectRef], s *connect.ServerStream[buildv2.JobRef]) error {
	j.c.count("JobService/List")
	for _, job := range j.c.Jobs[req.Msg.GetId()] {
		if err := s.Send(&buildv2.JobRef{Id: job.GetId()}); err != nil {
			return err
		}
	}
	for _, id := range j.c.Ghosts[req.Msg.GetId()] {
		if err := s.Send(&buildv2.JobRef{Id: id}); err != nil {
			return err
		}
	}
	return nil
}

func (j *jobs) Get(_ context.Context, req *connect.Request[buildv2.JobRef]) (*connect.Response[buildv2.Job], error) {
	j.c.count("JobService/Get")
	for _, list := range j.c.Jobs {
		for _, job := range list {
			if job.GetId() == req.Msg.GetId() {
				return connect.NewResponse(job), nil
			}
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no job %s", req.Msg.GetId()))
}

func (j *jobs) Follow(_ context.Context, req *connect.Request[buildv2.FollowRequest], s *connect.ServerStream[buildv2.LogChunk]) error {
	j.c.count("JobService/Follow")
	for i := range 3 {
		if err := s.Send(&buildv2.LogChunk{Data: []byte(fmt.Sprintf("line %d\n", i)), NextOffset: int64(i + 1)}); err != nil {
			return err
		}
	}
	return nil
}
