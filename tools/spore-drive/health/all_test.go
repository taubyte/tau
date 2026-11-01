package service

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/taubyte/tau/tools/spore-drive/proto/gen/health/v1"
	"gotest.tools/v3/assert"
)

func setupService() *Service {
	svc := &Service{
		ctx:     context.TODO(),
		version: "99.0.0",
	}
	return svc
}

func TestService_Ping(t *testing.T) {
	svc := setupService()

	resp, err := svc.Ping(context.TODO(), connect.NewRequest(&pb.Empty{}))
	assert.NilError(t, err)
	assert.Assert(t, resp != nil)
}

func TestService_Supports(t *testing.T) {
	svc := setupService()

	_, err := svc.Supports(context.TODO(), connect.NewRequest(&pb.SupportsRequest{
		Version: "98.0.0",
	}))
	assert.NilError(t, err)

	_, err = svc.Supports(context.TODO(), connect.NewRequest(&pb.SupportsRequest{
		Version: "invalid",
	}))
	assert.ErrorContains(t, err, "invalid version")

	_, err = svc.Supports(context.TODO(), connect.NewRequest(&pb.SupportsRequest{
		Version: "100.0.0",
	}))
	assert.ErrorContains(t, err, "is not supported")
}
