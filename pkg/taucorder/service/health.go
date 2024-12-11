package service

import (
	"context"

	"connectrpc.com/connect"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
)

func (ts *healthService) Ping(ctx context.Context, req *connect.Request[pb.Empty]) (*connect.Response[pb.Empty], error) {
	return connect.NewResponse(&pb.Empty{}), nil
}
