package satellite

import (
	"context"

	"github.com/hashicorp/go-plugin"
	"github.com/taubyte/tau/pkg/vm-orbit/proto"
	"google.golang.org/grpc"
)

func (st *satellite) GRPCServer(broker *plugin.GRPCBroker, s *grpc.Server) error {
	proto.RegisterPluginServer(s, &GRPCPluginServer{
		broker:    broker,
		satellite: st,
	})

	return nil
}

func (p *satellite) GRPCClient(ctx context.Context, broker *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	return nil, ErrorLinkClient
}
