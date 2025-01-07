package service

import (
	"context"
	"net/http"

	pbconnect "github.com/taubyte/tau/pkg/spore-drive/proto/gen/health/v1/healthv1connect"
)

type Service struct {
	pbconnect.UnimplementedHealthServiceHandler

	ctx context.Context

	path    string
	handler http.Handler

	version string
}
