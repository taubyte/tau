package service

import (
	"connectrpc.com/connect"
	pb "github.com/taubyte/tau/pkg/spore-drive/proto/gen/health/v1"
)

func noValReturn(err error) (*connect.Response[pb.Empty], error) {
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.Empty{}), nil
}
