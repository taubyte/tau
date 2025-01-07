package service

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	"github.com/Masterminds/semver"
	pb "github.com/taubyte/tau/pkg/spore-drive/proto/gen/health/v1"
	pbconnect "github.com/taubyte/tau/pkg/spore-drive/proto/gen/health/v1/healthv1connect"
)

func (s *Service) Ping(ctx context.Context, req *connect.Request[pb.Empty]) (*connect.Response[pb.Empty], error) {
	return noValReturn(nil)
}

func (s *Service) Supports(ctx context.Context, req *connect.Request[pb.SupportsRequest]) (*connect.Response[pb.Empty], error) {
	version, err := semver.NewVersion(req.Msg.GetVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid version: %w", err))
	}

	supported, err := semver.NewVersion(s.version)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create supported version: %w", err))
	}

	if version.GreaterThan(supported) {
		return nil, fmt.Errorf("version %s is not supported", version)
	}

	return noValReturn(nil)
}

func (s *Service) Attach(mux *http.ServeMux) {
	mux.Handle(s.path, s.handler)
}

func Serve(ctx context.Context, version string) (*Service, error) {
	srv := &Service{
		ctx:     ctx,
		version: version,
	}

	srv.path, srv.handler = pbconnect.NewHealthServiceHandler(srv)

	return srv, nil
}
