package wasm

import (
	"errors"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/secret"
)

// secret.go serves `taubyte:secret/vault` from pkg/invoke/secret, the host module
// every engine attaches.

var secretInterface = secret.Definition.Name()

// ErrNoSecrets is a spec that grants a secret on a tier composed without a backing.
var ErrNoSecrets = errors.New("wasm: this resource reads a secret and this tier has nothing to read one with")

// Secrets reads the value a grant names; the method is named so one type also backs
// services/worker's Secrets seam.
type Secrets = secret.Reader

// WithSecrets is what backs `taubyte:secret/vault`.
func WithSecrets(s Secrets) Option {
	return func(r *Runtime) { r.secrets = s }
}

func secretGrantsOf(spec invoke.Spec) map[string]invoke.Grant { return secret.GrantsOf(spec) }

func (r *Runtime) secretImports(spec invoke.Spec) []component.Option {
	return attach(secret.Module(spec, r.secrets))
}
