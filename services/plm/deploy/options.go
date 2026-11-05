package deploy

import (
	"context"
	"errors"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// Option configures a Deploy.
type Option func(*options) error

type options struct {
	projects  Projects
	checkout  Checkout
	register  func(context.Context) (Register, error)
	builder   Builder
	cloudFqdn string
	regions   schema.Regions
	domains   Domains
}

// WithDomains is what proves a project's zones are its own. Without it a
// project declaring one is refused.
func WithDomains(d Domains) Option {
	return func(o *options) error {
		if d == nil {
			return errors.New("domains must not be nil")
		}
		o.domains = d
		return nil
	}
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

// WithRegister is where a deploy publishes, a function since a register can go
// away between builds.
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

// WithBuilder is what a push to a CODE repository becomes. A node without one
// refuses such a push by name.
func WithBuilder(b Builder) Option {
	return func(o *options) error {
		if b == nil {
			return errors.New("builder must not be nil")
		}
		o.builder = b
		return nil
	}
}

// WithRegions is what this cloud can do with where a project says it runs. Without
// it a deploy asks schema.WithNoRegions: the build has no regions, everything is placed in
// schema.DefaultRegion, and any region a project names is refused.
func WithRegions(r schema.Regions) Option {
	return func(o *options) error {
		if r == nil {
			return errors.New("regions must not be nil")
		}
		o.regions = r
		return nil
	}
}
