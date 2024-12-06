package service

import (
	"context"
	"net"
	"net/http"
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

func TestAuthRepos(t *testing.T) {
	ctx, ctxC := context.WithCancel(context.Background())
	defer ctxC()

	s, err := getMockService(ctx)
	assert.NilError(t, err)

	ns := &nodeService{Service: s}
	rs := &reposService{Service: s}

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

	path, handler := pbconnect.NewRepositoriesInAuthServiceHandler(rs)

	mux := http.NewServeMux()
	mux.Handle(path, handler)

	server := &http.Server{
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	go func() {
		server.Serve(listener)
	}()
	defer server.Shutdown(ctx)

	createFakeProject(t, u, "fake-project", 1, "fake-user", "0", "1")
	registerFakeRepo(t, u, "2")
	registerFakeRepo(t, u, "3")

	t.Parallel()

	t.Run("Get repository", func(t *testing.T) {
		c := pbconnect.NewRepositoriesInAuthServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		var repoId int64
		for repoId = range 4 {
			repo, err := c.Get(ctx, connect.NewRequest(&pb.ByRepositoryRequest{
				Node: ni.Msg,
				Id: &pb.RepositoryId{
					Id: &pb.RepositoryId_Github{
						Github: repoId,
					},
				},
			}))
			assert.NilError(t, err)

			assert.Equal(t, repo.Msg.GetId().GetGithub(), repoId)
			assert.Equal(t, repo.Msg.GetDeployKeyPrivate(), "fake-deploy-priv-key")
		}
	})

	t.Run("List repos", func(t *testing.T) {
		c := pbconnect.NewRepositoriesInAuthServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		pstream, err := c.List(ctx, connect.NewRequest(ni.Msg))
		assert.NilError(t, err)

		repos := make([]int64, 0)
		for pstream.Receive() {
			repos = append(repos, pstream.Msg().GetId().GetGithub())
		}

		assert.Equal(t, len(repos), 4)
		// assert.Equal(t, slices.Contains(prjs, projectID), true)
		// assert.Equal(t, slices.Contains(prjs, projectID2), true)
	})

}
