package service

import (
	"context"
	"net"
	"net/http"
	"slices"
	"testing"
	"time"

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

func TestAuth(t *testing.T) {
	ctx, ctxC := context.WithCancel(context.Background())
	defer ctxC()

	s, err := getMockService(ctx)
	assert.NilError(t, err)

	ns := &nodeService{Service: s}
	as := &authService{Service: s}

	s.addHandler(pbconnect.NewNodeServiceHandler(ns))

	uname := t.Name()
	u := dream.New(dream.UniverseConfig{
		Name: uname,
	})
	defer u.Stop()

	assert.NilError(t, u.StartWithConfig(&dream.Config{Services: map[string]common.ServiceConfig{
		"auth": {Others: map[string]int{"copies": 2}},
	}}))

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

	path, handler := pbconnect.NewAuthServiceHandler(as)

	mux := http.NewServeMux()
	mux.Handle(path, handler)

	server := &http.Server{
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	go func() {
		server.Serve(listener)
	}()
	defer server.Shutdown(ctx)

	t.Run("Discover service", func(t *testing.T) {
		c := pbconnect.NewAuthServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		pstream, err := c.Discover(ctx, connect.NewRequest(&pb.DiscoverServiceRequest{
			Node: ni.Msg,
		}))
		assert.NilError(t, err)
		count := 0
		for pstream.Receive() {
			count++
		}
		assert.Equal(t, count, 2)
	})

	t.Run("Discover service (count set)", func(t *testing.T) {
		c := pbconnect.NewAuthServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		pstream, err := c.Discover(ctx, connect.NewRequest(&pb.DiscoverServiceRequest{
			Node:  ni.Msg,
			Count: 1,
		}))
		assert.NilError(t, err)
		count := 0
		for pstream.Receive() {
			count++
		}
		assert.Equal(t, count, 1)
	})

	t.Run("Discover service (timeout)", func(t *testing.T) {
		c := pbconnect.NewAuthServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		pstream, err := c.Discover(ctx, connect.NewRequest(&pb.DiscoverServiceRequest{
			Node:    ni.Msg,
			Timeout: int64(time.Millisecond),
		}))
		assert.NilError(t, err)
		count := 0
		for pstream.Receive() {
			count++
		}
		assert.Equal(t, count <= 2, true)
	})

	t.Run("List service", func(t *testing.T) {
		c := pbconnect.NewAuthServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		pstream, err := c.List(ctx, connect.NewRequest(ni.Msg))
		assert.NilError(t, err)
		count := 0
		for pstream.Receive() {
			count++
		}
		assert.Equal(t, count, 2)
	})

	t.Run("State", func(t *testing.T) {
		c := pbconnect.NewAuthServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		for _, n := range u.AllAuth() {
			pid := n.Node().ID().String()
			cstate, err := c.State(ctx, connect.NewRequest(&pb.ConsensusStateRequest{
				Node: ni.Msg,
				Pid:  pid,
			}))
			assert.NilError(t, err)
			assert.Equal(t, cstate.Msg.GetMember().GetId(), pid)
			assert.Equal(t, len(cstate.Msg.GetCrdt().GetHeads()), 0) // should be empty
		}
	})

	t.Run("States", func(t *testing.T) {
		c := pbconnect.NewAuthServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		allAuth := u.AllAuth()

		pids := make([]string, 0, len(allAuth))
		for _, n := range allAuth {
			pids = append(pids, n.Node().ID().String())
		}

		sstream, err := c.States(ctx, connect.NewRequest(ni.Msg))
		assert.NilError(t, err)

		count := 0
		for sstream.Receive() {
			msg := sstream.Msg()
			assert.Equal(t, slices.Contains(pids, msg.GetMember().GetId()), true)
			assert.Equal(t, len(msg.GetCrdt().GetHeads()), 0) // should be empty
			count++
		}

		assert.Equal(t, count, len(allAuth))
	})
}
