package gateway

import (
	"context"

	"github.com/ipfs/go-log/v2"
	tauConfig "github.com/taubyte/tau/config"
)

var logger = log.Logger("gateway.service")

func New(ctx context.Context, config *tauConfig.Node) (Gateway, error)
