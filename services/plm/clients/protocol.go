package clients

import (
	"errors"
	"fmt"

	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

// AuthProtocol is "/tau/auth/v2", derived from auth's schema.
var AuthProtocol = rpc.Protocol(authv2connect.IdentityServiceName)

// requireProtocol refuses a pool built for another service. Nothing else would
// notice: the call would reach a real peer, find no such method, and come back
// as an Unimplemented from a service that was never meant to be asked.
func requireProtocol(p *Pool, want protocol.ID, what string) error {
	if p == nil {
		return errors.New("build: no pool")
	}
	if p.Protocol() != want {
		return fmt.Errorf("build: %s needs a pool on %s, got %s", what, want, p.Protocol())
	}
	return nil
}
