package service

import (
	"context"
	"net"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
	pbconnect "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1/taucorderv1connect"
	_ "github.com/taubyte/tau/services/seer"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"gotest.tools/v3/assert"
)

func TestHealth(t *testing.T) {
	ctx, ctxC := context.WithCancel(context.Background())
	defer ctxC()

	s, err := getMockService(ctx)
	assert.NilError(t, err)

	ns := &nodeService{Service: s}
	hs := &healthService{Service: s}

	s.addHandler(pbconnect.NewNodeServiceHandler(ns))

	ni, err := ns.New(ctx, connect.NewRequest(&pb.Config{
		Source: &pb.Config_Raw{
			Raw: &pb.Raw{},
		},
	}))
	assert.NilError(t, err)

	defer ns.Free(ctx, connect.NewRequest(ni.Msg))

	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		panic(err)
	}
	defer listener.Close()

	path, handler := pbconnect.NewHealthServiceHandler(hs)

	mux := http.NewServeMux()
	mux.Handle(path, handler)

	server := &http.Server{
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	go func() {
		server.Serve(listener)
	}()
	defer server.Shutdown(ctx)

	t.Run("Ping", func(t *testing.T) {
		c := pbconnect.NewHealthServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		_, err := c.Ping(ctx, connect.NewRequest(&pb.Empty{}))
		assert.NilError(t, err)
	})
}
