package instance

import (
	"net"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/services/plm/hosted"
)

// PLMAddr is where this node's plm listens, nil when it runs none or it has not
// started.
func (n *Instance) PLMAddr() net.Addr {
	if f, ok := service.Get[hosted.Service](&added{node: n}); ok {
		return f.Addr()
	}
	return nil
}
