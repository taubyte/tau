package drive

import (
	"context"
	"time"

	"github.com/taubyte/tau/pkg/mycelium"
	host "github.com/taubyte/tau/pkg/mycelium/host"
	"github.com/taubyte/tau/tools/spore-drive/config"
	"github.com/taubyte/tau/tools/spore-drive/course"
)

type sporedrive struct {
	parser  config.Parser
	network *mycelium.Network

	tauBinary     []byte
	tauBinaryHash string

	// root is where everything tau keeps on a host lives: bin, config and
	// storage.
	root string

	now func() time.Time

	hostWrapper func(ctx context.Context, h host.Host) (remoteHost, error)
}

type Spore interface {
	Displace(ctx context.Context, course course.Course) <-chan Progress
	Network() *mycelium.Network
	Nodes(host string) (*Nodes, error)
}
