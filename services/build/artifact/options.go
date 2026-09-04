package artifact

import "errors"

// Option configures a Builder.
type Option func(*options) error

type options struct {
	register Register
	projects Projects
	checkout Checkout
	engine   Engine
}

// WithRegister is where a build reads the configuration it builds against.
func WithRegister(r Register) Option {
	return func(o *options) error {
		if r == nil {
			return errors.New("register must not be nil")
		}
		o.register = r
		return nil
	}
}

// WithProjects says which repository backs a project's code.
func WithProjects(p Projects) Option {
	return func(o *options) error {
		if p == nil {
			return errors.New("projects must not be nil")
		}
		o.projects = p
		return nil
	}
}

// WithCheckout is how a build reads a repository at a commit.
func WithCheckout(c Checkout) Option {
	return func(o *options) error {
		if c == nil {
			return errors.New("checkout must not be nil")
		}
		o.checkout = c
		return nil
	}
}

// WithEngine is what a build runs in.
func WithEngine(e Engine) Option {
	return func(o *options) error {
		if e == nil {
			return errors.New("engine must not be nil")
		}
		o.engine = e
		return nil
	}
}
