package instance

import (
	"errors"
	"fmt"
	"io"

	authService "github.com/taubyte/tau/services/auth"
)

// Who may use this cloud: auth answers from the Identity this node was composed with.

// WithIdentity builds the identity auth answers admission from, on a node that
// runs auth. It is handed the node the way an added service is, so it can
// reach whatever it decides from. Without it auth admits every caller a forge
// vouched for.
func WithIdentity(build func(h Host) (authService.Identity, error)) Option {
	return func(o *options) error {
		if build == nil {
			return errors.New("node: a nil identity")
		}
		o.identity = build
		return nil
	}
}

// identityOption is the auth option the composed identity becomes, or nil.
// openIdentity builds who decides admission, when the node was told how, and gives
// it to the services on the node: auth reads it beside itself.
func (n *Instance) openIdentity() error {
	if n.opts.identity == nil || !n.cfg.runs(Auth) {
		return nil
	}
	id, err := n.opts.identity(&added{node: n, name: Auth})
	if err != nil {
		return fmt.Errorf("node: identity: %w", err)
	}
	if id == nil {
		return errors.New("node: identity: built nothing")
	}
	if c, ok := id.(io.Closer); ok {
		n.addCloser(c)
	}
	n.provide("", id)
	return nil
}
