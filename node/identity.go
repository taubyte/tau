package node

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	authService "github.com/taubyte/tau/services/auth"
	"github.com/taubyte/tau/services/build/clients"
	"github.com/taubyte/tau/services/build/frontend"
)

// identity.go wires who may use this cloud: auth answers from the Identity its
// node was composed with, and the build frontend asks it about every caller a
// forge has granted.

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
func (n *Node) identityOption() (authService.Option, error) {
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

// admissions is the frontend's Admissions: the forge says who holds the
// credential, and auth says whether they are admitted.
type admissions struct {
	forges *forge.Registry
	auth   *clients.IdentityClient
}

func (a *admissions) Admit(ctx context.Context, cred frontend.Credentials, key repokey.Key) (bool, string, error) {
	session, err := a.forges.For(key, forge.Token(cred.Forge))
	if err != nil {
		return false, "", err
	}
	who, err := session.Whoami(ctx)
	if err != nil {
		return false, "", fmt.Errorf("asking %s who holds the credential: %w", key.Host, err)
	}
	got, err := a.auth.Admit(ctx, &authv2.Caller{Forge: key.Host, Subject: who.Subject, Login: who.Login, Token: cred.Tau})
	if err != nil {
		return false, "", err
	}
	return got.GetAdmitted(), got.GetReason(), nil
}

// authConfig is auth's configuration as this node states it: the operator's,
// with the issuer defaulting to the cloud's name and each forge's OAuth app
// taken from the forge it is for.
func (n *Node) authConfig() authService.Config {
	c := n.cfg.Auth
	if c.Issuer == "" {
		c.Issuer = "tau:" + n.cfg.Cloud
	}
	c.Forges = slices.Clone(c.Forges)
	for _, f := range n.cfg.Forges {
		if f.OAuthClientID != "" {
			c.Forges = append(c.Forges, authService.ForgeApp{Host: f.Host, ClientID: f.OAuthClientID})
		}
	}
	return c
}
