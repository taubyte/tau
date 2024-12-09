package service

import (
	"context"
	"net"
	"net/http"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/dream"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
	pbconnect "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1/taucorderv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"gotest.tools/v3/assert"

	"github.com/taubyte/tau/clients/p2p/patrick/mock"
)

func TestPatrick(t *testing.T) {
	ctx, ctxC := context.WithCancel(context.Background())
	defer ctxC()

	s, err := getMockService(ctx)
	assert.NilError(t, err)

	ns := &nodeService{Service: s}
	ps := &patrickService{Service: s}

	s.addHandler(pbconnect.NewNodeServiceHandler(ns))

	uname := t.Name()
	u := dream.New(dream.UniverseConfig{
		Name: uname,
	})
	defer u.Stop()

	assert.NilError(t, u.StartWithConfig(&dream.Config{}))

	ni, err := ns.New(ctx, connect.NewRequest(&pb.Config{
		Source: &pb.Config_Raw{
			Raw: &pb.Raw{},
		},
	}))
	assert.NilError(t, err)
	defer ns.Free(ctx, connect.NewRequest(ni.Msg))

	fakeJobs := make(map[string]*patrick.Job, 0)
	mockPatrick := &mock.Starfish{Jobs: fakeJobs}
	ninst := s.nodes[ni.Msg.Id]
	ninst.patrickClient = mockPatrick

	fjob1 := &patrick.Job{
		Id:        "jobforjob_test",
		Timestamp: time.Now().UnixNano(),
		Logs:      make(map[string]string),
		AssetCid:  make(map[string]string),
		Meta: patrick.Meta{
			Repository: patrick.Repository{
				ID:       1,
				Provider: "github",
			},
		},
	}

	fjob2 := &patrick.Job{
		Id:        "jobforjob_test_2",
		Timestamp: time.Now().UnixNano(),
		Logs:      make(map[string]string),
		AssetCid:  make(map[string]string),
		Meta: patrick.Meta{
			Repository: patrick.Repository{
				ID:       2,
				Provider: "github",
			},
		},
	}

	for _, fj := range []*patrick.Job{fjob1, fjob2} {
		assert.NilError(t, mockPatrick.AddJob(t, ninst.Node, fj))
	}

	assert.Equal(t, len(fakeJobs), 2)

	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		panic(err)
	}
	defer listener.Close()

	hpath, handler := pbconnect.NewPatrickServiceHandler(ps)

	mux := http.NewServeMux()
	mux.Handle(hpath, handler)

	server := &http.Server{
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	go func() {
		server.Serve(listener)
	}()
	defer server.Shutdown(ctx)

	t.Parallel()

	t.Run("List", func(t *testing.T) {
		c := pbconnect.NewPatrickServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		stream, err := c.List(ctx, connect.NewRequest(ni.Msg))
		assert.NilError(t, err)

		all := make([]string, 0)
		for stream.Receive() {
			assert.NilError(t, stream.Err())
			job := stream.Msg()
			all = append(all, job.GetId())
		}

		assert.Equal(t, slices.Contains(all, fjob1.Id), true)
		assert.Equal(t, slices.Contains(all, fjob2.Id), true)
		assert.Equal(t, len(all), 2)
	})

	t.Run("Get", func(t *testing.T) {
		c := pbconnect.NewPatrickServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		for jid, ojob := range fakeJobs {
			job, err := c.Get(ctx, connect.NewRequest(&pb.GetJobRequest{
				Node: ni.Msg,
				Id:   jid,
			}))
			assert.NilError(t, err)

			assert.Equal(t, job.Msg.GetId(), jid)
			assert.Equal(t, job.Msg.GetAttempt(), int32(ojob.Attempt))
			assert.Equal(t, job.Msg.GetDelay(), int64(ojob.Delay.Time)*1000)
			assert.Equal(t, job.Msg.GetStatus(), int32(ojob.Status))
			assert.Equal(t, job.Msg.GetTimestamp(), ojob.Timestamp)
			assert.Equal(t, job.Msg.GetMeta().GetAfter(), ojob.Meta.After)
			assert.Equal(t, job.Msg.GetMeta().GetBefore(), ojob.Meta.Before)
			assert.Equal(t, job.Msg.GetMeta().GetRef(), ojob.Meta.Ref)
			assert.Equal(t, job.Msg.GetMeta().GetHeadCommit(), ojob.Meta.HeadCommit.ID)
			assert.Equal(t, job.Msg.GetMeta().GetRepository().GetId().GetGithub(), int64(ojob.Meta.Repository.ID))
			assert.Equal(t, job.Msg.GetMeta().GetRepository().GetBranch(), ojob.Meta.Repository.Branch)
			assert.Equal(t, job.Msg.GetMeta().GetRepository().GetSshUrl(), ojob.Meta.Repository.SSHURL)
		}
	})

	t.Run("State", func(t *testing.T) {
		c := pbconnect.NewPatrickServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		cstate, err := c.State(ctx, connect.NewRequest(&pb.ConsensusStateRequest{
			Node: ni.Msg,
			Pid:  ninst.ID().String(),
		}))
		assert.NilError(t, err)
		assert.Equal(t, cstate.Msg.GetMember().GetId(), ninst.ID().String())
		assert.Equal(t, len(cstate.Msg.GetCrdt().GetHeads()), 0) // should be empty
	})
}
