package service

import (
	"context"
	"net"
	"net/http"
	"slices"
	"testing"
	"time"

	seerClient "github.com/taubyte/tau/clients/p2p/seer"

	"connectrpc.com/connect"
	"github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/services/seer"
	"github.com/taubyte/tau/dream"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
	pbconnect "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1/taucorderv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/services/auth"
	_ "github.com/taubyte/tau/services/tns"
)

func TestSeer(t *testing.T) {
	ctx, ctxC := context.WithCancel(context.Background())
	defer ctxC()

	s, err := getMockService(ctx)
	assert.NilError(t, err)

	ns := &nodeService{Service: s}
	ss := &seerService{Service: s}

	s.addHandler(pbconnect.NewNodeServiceHandler(ns))

	uname := t.Name()
	u := dream.New(dream.UniverseConfig{
		Name: uname,
	})
	defer u.Stop()

	seerClient.DefaultUsageBeaconInterval = 100 * time.Millisecond
	seerClient.DefaultAnnounceBeaconInterval = 100 * time.Millisecond
	seerClient.DefaultGeoBeaconInterval = 100 * time.Millisecond

	authLoc := seer.Location{Latitude: 32, Longitude: -96}
	tnsLoc := seer.Location{Latitude: 36, Longitude: 3}

	assert.NilError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]common.ServiceConfig{"seer": {}, "auth": {Location: authLoc}, "tns": {Location: tnsLoc}},
		Simples: map[string]dream.SimpleConfig{
			"n1": {Clients: map[string]*common.ClientConfig{"seer": {}}},
		},
	}))

	// wait for nodes to announce
	n1, err := u.Simple("n1")
	assert.NilError(t, err)
	n1seer, err := n1.Seer()
	assert.NilError(t, err)
	for {
		time.Sleep(500 * time.Millisecond)
		l, _ := n1seer.Usage().List()
		if len(l) >= 2 {
			break
		}
	}

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

	path, handler := pbconnect.NewSeerServiceHandler(ss)

	mux := http.NewServeMux()
	mux.Handle(path, handler)

	server := &http.Server{
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	go func() {
		server.Serve(listener)
	}()
	defer server.Shutdown(ctx)

	t.Parallel()

	t.Run("List nodes", func(t *testing.T) {
		c := pbconnect.NewSeerServiceClient(http.DefaultClient, "http://"+listener.Addr().String())

		stream, err := c.List(ctx, connect.NewRequest(&pb.NodesListRequest{Node: ni.Msg}))
		assert.NilError(t, err)

		known := []string{u.Auth().Node().ID().String(), u.TNS().Node().ID().String()}
		all := make([]string, 0)
		for stream.Receive() {
			assert.NilError(t, stream.Err())
			pid := stream.Msg().Id
			all = append(all, pid)
			assert.Equal(t, slices.Contains(known, pid), true)
		}
		assert.Equal(t, len(all), 2)
	})

	t.Run("Usage node", func(t *testing.T) {
		c := pbconnect.NewSeerServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		pid := u.Auth().Node().ID().String()
		ret, err := c.Usage(ctx, connect.NewRequest(&pb.NodesUsageRequest{
			Node: ni.Msg,
			Peer: pid,
		}))
		assert.NilError(t, err)

		assert.Equal(t, ret.Msg.GetPeer().GetId(), pid)
		assert.Equal(t, ret.Msg.GetAddress(), "127.0.0.1")
		assert.Equal(t, ret.Msg.GetCpuCount() > 0, true)
	})

	t.Run("Geo node", func(t *testing.T) {
		c := pbconnect.NewSeerServiceClient(http.DefaultClient, "http://"+listener.Addr().String())

		stream, err := c.Location(ctx, connect.NewRequest(&pb.LocationRequest{
			Node: ni.Msg,
			Filter: &pb.LocationRequest_All{
				All: true,
			},
		}))
		assert.NilError(t, err)

		known := map[string]*seer.Location{
			u.Auth().Node().ID().String(): &authLoc,
			u.TNS().Node().ID().String():  &tnsLoc,
		}
		all := make([]string, 0)
		for stream.Receive() {
			assert.NilError(t, stream.Err())
			pid := stream.Msg().GetPeer().GetId()
			all = append(all, pid)
			loc := stream.Msg().Location
			assert.Equal(t, known[pid] != nil, true)
			assert.Equal(t, known[pid].Latitude, loc.Latitude)
			assert.Equal(t, known[pid].Longitude, loc.Longitude)
		}
		assert.Equal(t, len(all), 2)
	})

	t.Run("Geo node (area)", func(t *testing.T) {
		c := pbconnect.NewSeerServiceClient(http.DefaultClient, "http://"+listener.Addr().String())

		stream, err := c.Location(ctx, connect.NewRequest(&pb.LocationRequest{
			Node: ni.Msg,
			Filter: &pb.LocationRequest_Area{
				Area: &pb.LocationArea{
					Location: &pb.Location{
						Latitude:  29,
						Longitude: -95,
					},
					Distance: 400 * 1000, // in meters
				},
			},
		}))
		assert.NilError(t, err)

		known := map[string]*seer.Location{
			u.Auth().Node().ID().String(): &authLoc,
		}
		all := make([]string, 0)
		for stream.Receive() {
			assert.NilError(t, stream.Err())
			pid := stream.Msg().GetPeer().GetId()
			all = append(all, pid)
			loc := stream.Msg().Location
			assert.Equal(t, known[pid] != nil, true)
			assert.Equal(t, known[pid].Latitude, loc.Latitude)
			assert.Equal(t, known[pid].Longitude, loc.Longitude)
		}
		assert.Equal(t, len(all), 1)
	})

	t.Run("Geo node (pid)", func(t *testing.T) {
		c := pbconnect.NewSeerServiceClient(http.DefaultClient, "http://"+listener.Addr().String())

		stream, err := c.Location(ctx, connect.NewRequest(&pb.LocationRequest{
			Node: ni.Msg,
			Filter: &pb.LocationRequest_Peers{
				Peers: &pb.Peers{
					Pids: []string{u.TNS().Node().ID().String()},
				},
			},
		}))
		assert.NilError(t, err)

		known := map[string]*seer.Location{
			u.TNS().Node().ID().String(): &tnsLoc,
		}
		all := make([]string, 0)
		for stream.Receive() {
			assert.NilError(t, stream.Err())
			pid := stream.Msg().GetPeer().GetId()
			all = append(all, pid)
			loc := stream.Msg().Location
			assert.Equal(t, known[pid] != nil, true)
			assert.Equal(t, known[pid].Latitude, loc.Latitude)
			assert.Equal(t, known[pid].Longitude, loc.Longitude)
		}
		assert.Equal(t, len(all), 1)
	})

}
