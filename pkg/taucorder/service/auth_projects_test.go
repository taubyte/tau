package service

import (
	"context"
	"net"
	"net/http"
	"slices"
	"testing"

	"connectrpc.com/connect"
	"github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
	pbconnect "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1/taucorderv1connect"
	_ "github.com/taubyte/tau/services/auth"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"gotest.tools/v3/assert"
)

func TestAuthProjects(t *testing.T) {
	ctx, ctxC := context.WithCancel(context.Background())
	defer ctxC()

	s, err := getMockService(ctx)
	assert.NilError(t, err)

	ns := &nodeService{Service: s}
	ps := &projectsService{Service: s}

	s.addHandler(pbconnect.NewNodeServiceHandler(ns))

	uname := t.Name()
	u := dream.New(dream.UniverseConfig{
		Name: uname,
	})
	defer u.Stop()

	assert.NilError(t, u.StartWithConfig(&dream.Config{Services: map[string]common.ServiceConfig{"auth": {}}}))

	ni, err := ns.New(ctx, connect.NewRequest(&pb.Config{
		Source: &pb.Config_Universe{
			Universe: &pb.Dream{
				Universe: uname,
			},
		},
	}))
	assert.NilError(t, err)

	defer ns.Free(ctx, connect.NewRequest(ni.Msg))

	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		panic(err)
	}
	defer listener.Close()

	path, handler := pbconnect.NewProjectsInAuthServiceHandler(ps)

	mux := http.NewServeMux()
	mux.Handle(path, handler)

	server := &http.Server{
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	go func() {
		server.Serve(listener)
	}()
	defer server.Shutdown(ctx)

	projectID := createFakeProject(t, u, "fake-project", 1, "fake-user", "1", "2")
	projectID2 := createFakeProject(t, u, "fake-project-2", 1, "fake-user", "3", "4")

	t.Parallel()

	t.Run("Get project 1", func(t *testing.T) {
		c := pbconnect.NewProjectsInAuthServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		prj, err := c.Get(ctx, connect.NewRequest(&pb.ByProjectRequest{
			Node: ni.Msg,
			Id:   projectID,
		}))
		assert.NilError(t, err)

		assert.Equal(t, prj.Msg.GetId(), projectID)
		assert.Equal(t, prj.Msg.GetName(), "fake-project")
		assert.Equal(t, prj.Msg.GetProvider(), "github")
		assert.Equal(t, prj.Msg.GetRepositories().GetCode().GetId().GetGithub(), int64(2))
		assert.Equal(t, prj.Msg.GetRepositories().GetConfig().GetId().GetGithub(), int64(1))
	})

	t.Run("Get project 2", func(t *testing.T) {
		c := pbconnect.NewProjectsInAuthServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		prj, err := c.Get(ctx, connect.NewRequest(&pb.ByProjectRequest{
			Node: ni.Msg,
			Id:   projectID2,
		}))
		assert.NilError(t, err)

		assert.Equal(t, prj.Msg.GetId(), projectID2)
		assert.Equal(t, prj.Msg.GetName(), "fake-project-2")
		assert.Equal(t, prj.Msg.GetProvider(), "github")
		assert.Equal(t, prj.Msg.GetRepositories().GetCode().GetId().GetGithub(), int64(4))
		assert.Equal(t, prj.Msg.GetRepositories().GetConfig().GetId().GetGithub(), int64(3))
	})

	t.Run("List projects", func(t *testing.T) {
		c := pbconnect.NewProjectsInAuthServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		pstream, err := c.List(ctx, connect.NewRequest(ni.Msg))
		assert.NilError(t, err)

		prjs := make([]string, 0)
		for pstream.Receive() {
			prjs = append(prjs, pstream.Msg().GetId())
		}

		assert.Equal(t, len(prjs), 2)
		assert.Equal(t, slices.Contains(prjs, projectID), true)
		assert.Equal(t, slices.Contains(prjs, projectID2), true)
	})

}
