package link

import (
	"context"

	"github.com/hashicorp/go-plugin"
	"github.com/taubyte/tau/pkg/vm-orbit/proto"
	"google.golang.org/grpc"
)

func (p *link) GRPCServer(broker *plugin.GRPCBroker, s *grpc.Server) error {
	return ErrorLinkServer
}

func (p *link) GRPCClient(ctx context.Context, broker *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	return &GRPCPluginClient{
		client: proto.NewPluginClient(c),
		broker: broker,
	}, nil
}
