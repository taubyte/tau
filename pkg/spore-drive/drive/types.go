package drive

import (
	"context"

	"github.com/taubyte/tau/pkg/mycelium"
	host "github.com/taubyte/tau/pkg/mycelium/host"
	"github.com/taubyte/tau/pkg/spore-drive/config"
	"github.com/taubyte/tau/pkg/spore-drive/course"
)

type sporedrive struct {
	parser  config.Parser
	network *mycelium.Network

	tauBinary     []byte
	tauBinaryHash string

	hostWrapper func(ctx context.Context, h host.Host) (remoteHost, error)
}

type Spore interface {
	Displace(ctx context.Context, course course.Course) <-chan Progress
	Network() *mycelium.Network
}
