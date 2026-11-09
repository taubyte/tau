package instance

import (
	"context"
	"crypto/tls"

	"github.com/taubyte/tau/services/cm"
)

// CM is the service that holds what the cloud keeps once for every
// region.
var CM = cm.Name

// ServiceTLS is what a listener its composer opens beside the node's services
// answers handshakes with: the cloud's certificate for `<service>.<zone>` (the
// zone's wildcard, when it issues one), from a cm client of the node's own, or nil
// when the cloud issues none. Each service with a public listener builds its own
// (cm.ClientOn); the gateway's listener takes TLSConfig.
func (n *Instance) ServiceTLS() (*tls.Config, error) {
	n.sharedMu.Lock()
	defer n.sharedMu.Unlock()
	if n.certs == nil {
		c, err := cm.ClientOn(context.Background(), n.HostOf(""))
		if c == nil || err != nil {
			return nil, err
		}
		n.certs = c
		n.addCloser(c)
	}
	return n.certs.TLSConfig(), nil
}
