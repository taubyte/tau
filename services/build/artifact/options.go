package artifact

import "errors"

// Option configures a Builder.
type Option func(*options) error

type options struct {
	register Register
	projects Projects
	checkout Checkout
	engine   Engine
	secrets  Secrets
	keys     Keys
}

// WithKeys is how a source's clone URL becomes the key a push is compared
// against — the node's forge registry, so a source spelled by any of a
// forge's addresses is the repository it names. Optional: without it a URL
// keys by repokey's parse.
func WithKeys(k Keys) Option {
	return func(o *options) error {
		if k == nil {
			return errors.New("keys must not be nil")
		}
		o.keys = k
		return nil
	}
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

// WithSecrets resolves a build-time secret. Optional: a cloud without one
// builds everything that needs no secret, and refuses the rest by name.
func WithSecrets(s Secrets) Option {
	return func(o *options) error {
		if s == nil {
			return errors.New("secrets must not be nil")
		}
		o.secrets = s
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
