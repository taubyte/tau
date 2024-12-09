package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/p2p/peer"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
	pbconnect "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1/taucorderv1connect"
	protocolCommon "github.com/taubyte/tau/services/common"
	"github.com/taubyte/tau/services/monkey"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"gotest.tools/v3/assert"

	"github.com/taubyte/tau/clients/p2p/patrick/mock"
)

func TestMonkey(t *testing.T) {
	ctx, ctxC := context.WithCancel(context.Background())
	defer ctxC()

	s, err := getMockService(ctx)
	assert.NilError(t, err)

	ns := &nodeService{Service: s}
	ms := &monkeyService{Service: s}

	s.addHandler(pbconnect.NewNodeServiceHandler(ns))

	uname := t.Name()
	u := dream.New(dream.UniverseConfig{
		Name: uname,
	})
	defer u.Stop()

	fakeJobs := make(map[string]*patrick.Job, 0)
	mockPatrick := &mock.Starfish{Jobs: fakeJobs}

	monkey.NewPatrick = func(ctx context.Context, node peer.Node) (patrick.Client, error) {
		return mockPatrick, nil
	}
	protocolCommon.MockedPatrick = true

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

	assert.NilError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]common.ServiceConfig{"monkey": {}, "hoarder": {}},
	}))

	ni, err := ns.New(ctx, connect.NewRequest(&pb.Config{
		Source: &pb.Config_Universe{
			Universe: &pb.Dream{
				Universe: uname,
			},
		},
	}))
	assert.NilError(t, err)
	defer ns.Free(ctx, connect.NewRequest(ni.Msg))

	ninst := s.nodes[ni.Msg.Id]
	ninst.patrickClient = mockPatrick

	for _, fj := range []*patrick.Job{fjob1, fjob2} {
		assert.NilError(t, mockPatrick.AddJob(t, u.Monkey().Node(), fj))
	}

	assert.Equal(t, len(fakeJobs), 2)

	for {
		time.Sleep(time.Second) // give time to monkey to pick up jobs
		l, _ := ninst.monkeyClient.List()
		fmt.Println(l)
		if len(l) > 1 {
			break
		}
	}

	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		panic(err)
	}
	defer listener.Close()

	hpath, handler := pbconnect.NewMonkeyServiceHandler(ms)

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
		c := pbconnect.NewMonkeyServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		stream, err := c.List(ctx, connect.NewRequest(ni.Msg))
		assert.NilError(t, err)

		all := make([]string, 0)
		for stream.Receive() {
			assert.NilError(t, stream.Err())
			job := stream.Msg()
			all = append(all, job.GetId())
		}

		fmt.Println(all)
		assert.Equal(t, slices.Contains(all, fjob1.Id), true)
		assert.Equal(t, slices.Contains(all, fjob2.Id), true)
		assert.Equal(t, len(all), 2)
	})

	t.Run("Get", func(t *testing.T) {
		c := pbconnect.NewMonkeyServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		for jid, ojob := range fakeJobs {
			job, err := c.Get(ctx, connect.NewRequest(&pb.GetJobInstanceRequest{
				Node: ni.Msg,
				Id:   jid,
			}))
			assert.NilError(t, err)

			assert.Equal(t, job.Msg.GetId(), jid)
			assert.Equal(t, job.Msg.GetStatus(), int32(ojob.Status))
		}
	})

	// t.Run("State", func(t *testing.T) {
	// 	c := pbconnect.NewPatrickServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
	// 	for _, n := range u.AllPatrick() {
	// 		pid := n.Node().ID().String()
	// 		cstate, err := c.State(ctx, connect.NewRequest(&pb.ConsensusStateRequest{
	// 			Node: ni.Msg,
	// 			Pid:  pid,
	// 		}))
	// 		assert.NilError(t, err)
	// 		assert.Equal(t, cstate.Msg.GetMember().GetId(), pid)
	// 		assert.Equal(t, len(cstate.Msg.GetCrdt().GetHeads()), 0) // should be empty
	// 	}
	// })

	// t.Run("States", func(t *testing.T) {
	// 	c := pbconnect.NewPatrickServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
	// 	allPats := u.AllPatrick()

	// 	pids := make([]string, 0, len(allPats))
	// 	for _, n := range allPats {
	// 		pids = append(pids, n.Node().ID().String())
	// 	}

	// 	sstream, err := c.States(ctx, connect.NewRequest(ni.Msg))
	// 	assert.NilError(t, err)

	// 	count := 0
	// 	for sstream.Receive() {
	// 		msg := sstream.Msg()
	// 		assert.Equal(t, slices.Contains(pids, msg.GetMember().GetId()), true)
	// 		assert.Equal(t, len(msg.GetCrdt().GetHeads()), 0) // should be empty
	// 		count++
	// 	}

	// 	assert.Equal(t, count, len(allPats))
	// })

}
