package service

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
	pbconnect "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1/taucorderv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/services/hoarder"
)

func TestHoarder(t *testing.T) {
	ctx, ctxC := context.WithCancel(context.Background())
	defer ctxC()

	s, err := getMockService(ctx)
	assert.NilError(t, err)

	ns := &nodeService{Service: s}
	hs := &hoarderService{Service: s}

	s.addHandler(pbconnect.NewNodeServiceHandler(ns))

	uname := t.Name()
	u := dream.New(dream.UniverseConfig{
		Name: uname,
	})
	defer u.Stop()

	assert.NilError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]common.ServiceConfig{"hoarder": {}},
		Simples: map[string]dream.SimpleConfig{
			"n1": {Clients: map[string]*common.ClientConfig{"hoarder": {}}},
		},
	}))

	// wait for nodes to announce
	n1, err := u.Simple("n1")
	assert.NilError(t, err)
	n1hoarder, err := n1.Hoarder()
	assert.NilError(t, err)

	cid1, err := n1.AddFile(strings.NewReader("hello world"))
	assert.NilError(t, err)

	cid2, err := n1.AddFile(strings.NewReader("hello world 2"))
	assert.NilError(t, err)

	cid3, err := n1.AddFile(strings.NewReader("hello world 3"))
	assert.NilError(t, err)

	_, err = n1hoarder.Stash(cid1, n1.Peer().Addrs()[0].String()+"/p2p/"+n1.ID().String())
	assert.NilError(t, err)

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

	path, handler := pbconnect.NewHoarderServiceHandler(hs)

	mux := http.NewServeMux()
	mux.Handle(path, handler)

	server := &http.Server{
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	go func() {
		server.Serve(listener)
	}()
	defer server.Shutdown(ctx)

	t.Run("List cids", func(t *testing.T) {
		c := pbconnect.NewHoarderServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		stream, err := c.List(ctx, connect.NewRequest(ni.Msg))
		assert.NilError(t, err)

		all := make([]string, 0)
		for stream.Receive() {
			assert.NilError(t, stream.Err())
			pid := stream.Msg().GetCid()
			all = append(all, pid)
		}
		assert.Equal(t, len(all), 1)
	})

	t.Run("Stash cid1 again", func(t *testing.T) {
		c := pbconnect.NewHoarderServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		_, err := c.Stash(ctx, connect.NewRequest(&pb.StashRequest{
			Node: ni.Msg,
			Cid:  cid1,
			Providers: []*pb.Peer{
				{
					Id:        n1.ID().String(),
					Addresses: []string{n1.Peer().Addrs()[0].String()},
				},
			},
		}))
		assert.NilError(t, err)
	})

	t.Run("Stash new cid", func(t *testing.T) {
		c := pbconnect.NewHoarderServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		_, err := c.Stash(ctx, connect.NewRequest(&pb.StashRequest{
			Node: ni.Msg,
			Cid:  cid2,
			Providers: []*pb.Peer{
				{
					Id:        n1.ID().String(),
					Addresses: []string{n1.Peer().Addrs()[0].String()},
				},
			},
		}))
		assert.NilError(t, err)
	})

	t.Run("Stash peers already connected", func(t *testing.T) {
		c := pbconnect.NewHoarderServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		_, err := c.Stash(ctx, connect.NewRequest(&pb.StashRequest{
			Node: ni.Msg,
			Cid:  cid3,
		}))
		assert.NilError(t, err)
	})

}
