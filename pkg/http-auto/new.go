package auto

import (
	"context"

	"github.com/taubyte/tau/p2p/peer"
	service "github.com/taubyte/tau/pkg/http"
	"github.com/taubyte/tau/pkg/http/options"
)

// New is the autocert-managed HTTPS Service constructor. Pure options — no
// tau config. Config-driven setup lives in services/common.NewHTTPService.
func New(ctx context.Context, node peer.Node, ops ...options.Option) (service.Service, error) {
	return newAuto(ctx, node, ops...)
}
