package service

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
	pbconnect "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1/taucorderv1connect"
	srvcommon "github.com/taubyte/tau/services/common"
	_ "github.com/taubyte/tau/services/seer"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"gotest.tools/v3/assert"
)

func TestSwarm(t *testing.T) {
	ctx, ctxC := context.WithCancel(context.Background())
	defer ctxC()

	s, err := getMockService(ctx)
	assert.NilError(t, err)

	ns := &nodeService{Service: s}
	ss := &swarmService{Service: s}

	s.addHandler(pbconnect.NewNodeServiceHandler(ns))

	uname := t.Name()
	u := dream.New(dream.UniverseConfig{
		Name: uname,
	})
	defer u.Stop()

	assert.NilError(t, u.StartWithConfig(&dream.Config{Services: map[string]common.ServiceConfig{"seer": {}}}))

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

	path, handler := pbconnect.NewSwarmServiceHandler(ss)

	mux := http.NewServeMux()
	mux.Handle(path, handler)

	server := &http.Server{
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	go func() {
		server.Serve(listener)
	}()
	defer server.Shutdown(ctx)

	t.Run("Connect peer", func(t *testing.T) {
		tp, err := u.CreateSimpleNode("test_peer", &dream.SimpleConfig{})
		assert.NilError(t, err)
		assert.NilError(t, tp.WaitForSwarm(3*time.Second))
		tpma := tp.Peer().Addrs()[0]

		c := pbconnect.NewSwarmServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		p, err := c.Connect(ctx, connect.NewRequest(&pb.ConnectRequest{
			Node:    ni.Msg,
			Address: tpma.String() + "/p2p/" + tp.ID().String(),
		}))
		assert.NilError(t, err)
		assert.Equal(t, p.Msg.GetId(), tp.ID().String())
		assert.DeepEqual(t, p.Msg.GetAddresses(), []string{tpma.String()})
	})

	t.Run("Discover service", func(t *testing.T) {
		c := pbconnect.NewSwarmServiceClient(http.DefaultClient, "http://"+listener.Addr().String())

		pstream, err := c.Discover(ctx, connect.NewRequest(&pb.DiscoverRequest{
			Node:    ni.Msg,
			Service: srvcommon.SeerProtocol,
		}))
		assert.NilError(t, err)
		count := 0
		for pstream.Receive() {
			count++
		}
		assert.Equal(t, count, 1)
	})

	t.Run("List peers (no ping)", func(t *testing.T) {
		tp, err := u.CreateSimpleNode("test_peer_2", &dream.SimpleConfig{})
		assert.NilError(t, err)
		assert.NilError(t, tp.WaitForSwarm(3*time.Second))
		tpma := tp.Peer().Addrs()[0]

		c := pbconnect.NewSwarmServiceClient(http.DefaultClient, "http://"+listener.Addr().String())

		_, err = c.Connect(ctx, connect.NewRequest(&pb.ConnectRequest{
			Node:    ni.Msg,
			Address: tpma.String() + "/p2p/" + tp.ID().String(),
		}))
		assert.NilError(t, err)

		pstream, err := c.List(ctx, connect.NewRequest(&pb.ListRequest{
			Node: ni.Msg,
		}))
		assert.NilError(t, err)
		count := 0
		hit := false
		for pstream.Receive() {
			msg := pstream.Msg()
			count++
			if msg.GetId() == tp.ID().String() {
				hit = true
			}
		}
		assert.Equal(t, hit, true)
		assert.Equal(t, count > 1, true)
	})

	t.Run("List peers (with ping)", func(t *testing.T) {
		tp, err := u.CreateSimpleNode("test_peer_3", &dream.SimpleConfig{})
		assert.NilError(t, err)
		assert.NilError(t, tp.WaitForSwarm(3*time.Second))
		tpma := tp.Peer().Addrs()[0]

		c := pbconnect.NewSwarmServiceClient(http.DefaultClient, "http://"+listener.Addr().String())

		_, err = c.Connect(ctx, connect.NewRequest(&pb.ConnectRequest{
			Node:    ni.Msg,
			Address: tpma.String() + "/p2p/" + tp.ID().String(),
		}))
		assert.NilError(t, err)

		pstream, err := c.List(ctx, connect.NewRequest(&pb.ListRequest{
			Node: ni.Msg,
			Ping: &pb.ListPingRequest{
				Count:       3,
				Concurrency: 4,
			},
		}))
		assert.NilError(t, err)
		count := 0
		hit := false
		for pstream.Receive() {
			msg := pstream.Msg()
			count++
			if msg.GetId() == tp.ID().String() {
				hit = true
				assert.Equal(t, msg.GetPingStatus().GetUp(), true)
				assert.Equal(t, int(msg.GetPingStatus().GetCountTotal()), 3)
				assert.Equal(t, int(msg.GetPingStatus().GetCount()), 3)
				assert.Equal(t, time.Duration(msg.GetPingStatus().GetLatency()) < 5*time.Millisecond, true)
			}
		}
		assert.Equal(t, hit, true)
		assert.Equal(t, count > 1, true)
	})

	t.Run("Ping peer", func(t *testing.T) {
		tp, err := u.CreateSimpleNode("test_peer_4", &dream.SimpleConfig{})
		assert.NilError(t, err)
		assert.NilError(t, tp.WaitForSwarm(3*time.Second))
		tpma := tp.Peer().Addrs()[0]

		c := pbconnect.NewSwarmServiceClient(http.DefaultClient, "http://"+listener.Addr().String())

		_, err = c.Connect(ctx, connect.NewRequest(&pb.ConnectRequest{
			Node:    ni.Msg,
			Address: tpma.String() + "/p2p/" + tp.ID().String(),
		}))
		assert.NilError(t, err)

		ret, err := c.Ping(ctx, connect.NewRequest(&pb.PingRequest{
			Node:  ni.Msg,
			Pid:   tp.ID().String(),
			Count: 5,
		}))

		assert.NilError(t, err)
		assert.Equal(t, int(ret.Msg.GetPingStatus().GetCountTotal()), 5)
		assert.Equal(t, int(ret.Msg.GetPingStatus().GetCount()), 5)
		assert.Equal(t, ret.Msg.GetPingStatus().GetUp(), true)
		assert.Equal(t, time.Duration(ret.Msg.GetPingStatus().GetLatency()) < 5*time.Millisecond, true)
	})

	t.Run("Ping peer (timeout)", func(t *testing.T) {
		tp, err := u.CreateSimpleNode("test_peer_5", &dream.SimpleConfig{})
		assert.NilError(t, err)
		assert.NilError(t, tp.WaitForSwarm(3*time.Second))
		tpma := tp.Peer().Addrs()[0]

		c := pbconnect.NewSwarmServiceClient(http.DefaultClient, "http://"+listener.Addr().String())

		_, err = c.Connect(ctx, connect.NewRequest(&pb.ConnectRequest{
			Node:    ni.Msg,
			Address: tpma.String() + "/p2p/" + tp.ID().String(),
		}))
		assert.NilError(t, err)

		ret, err := c.Ping(ctx, connect.NewRequest(&pb.PingRequest{
			Node:    ni.Msg,
			Pid:     tp.ID().String(),
			Count:   100000,
			Timeout: int64(5 * time.Millisecond),
		}))

		assert.NilError(t, err)
		assert.Equal(t, int(ret.Msg.GetPingStatus().GetCountTotal()), 100000)
		assert.Equal(t, int(ret.Msg.GetPingStatus().GetCount()) < 100000, true)
		assert.Equal(t, ret.Msg.GetPingStatus().GetUp(), false)
	})

	t.Run("Wait for swarm", func(t *testing.T) {
		c := pbconnect.NewSwarmServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		_, err = c.Wait(ctx, connect.NewRequest(&pb.WaitRequest{
			Node:    ni.Msg,
			Timeout: int64(3 * time.Second),
		}))
		assert.NilError(t, err)
	})

}
