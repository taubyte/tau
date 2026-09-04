package deploy

import (
	"context"
	"errors"
)

// Option configures a Deploy.
type Option func(*options) error

type options struct {
	projects  Projects
	checkout  Checkout
	register  func(context.Context) (Register, error)
	cloudFqdn string
}

// WithProjects is how a deploy tells a configuration repository from a code
// one.
func WithProjects(p Projects) Option {
	return func(o *options) error {
		if p == nil {
			return errors.New("projects must not be nil")
		}
		o.projects = p
		return nil
	}
}

// WithCheckout is how a deploy reads a repository at a commit.
func WithCheckout(c Checkout) Option {
	return func(o *options) error {
		if c == nil {
			return errors.New("checkout must not be nil")
		}
		o.checkout = c
		return nil
	}
}

// WithRegister is where a deploy publishes.
//
// IT IS A FUNCTION AND NOT A VALUE, because which node answers is a routing
// question: a register can go away between two builds, and one resolved at
// construction would be the one this node dialed at boot forever.
func WithRegister(find func(context.Context) (Register, error)) Option {
	return func(o *options) error {
		if find == nil {
			return errors.New("register lookup must not be nil")
		}
		o.register = find
		return nil
	}
}

// WithLocalRegister is WithRegister for a node that runs one itself.
func WithLocalRegister(r Register) Option {
	if r == nil {
		return func(*options) error { return errors.New("register must not be nil") }
	}
	return WithRegister(func(context.Context) (Register, error) { return r, nil })
}

// WithCloudFqdn is the zone this cloud serves. Without it a generated name is
// reported rather than published: half a name in a routing table is one no
// request will ever carry.
func WithCloudFqdn(fqdn string) Option {
	return func(o *options) error {
		o.cloudFqdn = fqdn
		return nil
	}
}
