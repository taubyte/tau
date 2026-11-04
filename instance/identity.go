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
func (n *Instance) identityOption() (authService.Option, error) {
	if n.opts.identity == nil {
		return nil, nil
	}
	id, err := n.opts.identity(&added{node: n, name: Auth})
	if err != nil {
		return nil, fmt.Errorf("node: identity: %w", err)
	}
	if id == nil {
		return nil, errors.New("node: identity: built nothing")
	}
	if c, ok := id.(io.Closer); ok {
		n.addCloser(c)
	}
	return authService.WithIdentity(id), nil
}

// authConfig is auth's configuration as this node states it: the operator's,
// with the issuer defaulting to the cloud's name.
func (n *Instance) authConfig() authService.Config {
	c := n.cfg.Auth
	if c.Issuer == "" {
		c.Issuer = "tau:" + n.cfg.Cloud
	}
	return c
}
