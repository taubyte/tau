package container

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/pkg/vault"
)

// A workload reads a secret its access block grants through the helper at
// /.taubyte/secret, as a build step does (pkg/vault): when it asks, never from its
// environment, under the same checks the wasm tier's `secret.get` makes.

const (
	secretKind  = specs.KindSecret
	opGetSecret = "get"
)

// Secrets reads the value a grant names; the wasm tier reads through the same seam.
type Secrets interface {
	SecretFor(ctx context.Context, grant invoke.Grant) ([]byte, error)
}

// WithSecrets is what a workload's vault reads through. Without one, a workload
// granted a secret is still served a vault, which answers that this node cannot read.
func WithSecrets(s Secrets) Option {
	return func(o *options) error {
		if s == nil {
			return errors.New("container: a nil secrets reader")
		}
		o.secrets = s
		return nil
	}
}

// granted is one instance's vault: what its spec grants, read through the node's.
type granted struct {
	grants  map[string]invoke.Grant
	secrets Secrets

	mu     sync.Mutex
	served [][]byte
}

func (g *granted) Read(ctx context.Context, label string) ([]byte, error) {
	grant, ok := g.grants[label]
	switch {
	case !ok:
		return nil, fmt.Errorf("%w: the access block names no secret %q", vault.ErrNotGranted, label)
	case grant.Refused != "":
		return nil, fmt.Errorf("%w: %s", vault.ErrNotGranted, grant.Refused)
	case !grant.Allows(opGetSecret):
		return nil, fmt.Errorf("%w: %s cannot get", vault.ErrNotGranted, label)
	case g.secrets == nil:
		return nil, errors.New("this node has nothing to read a secret with")
	}
	return g.secrets.SecretFor(ctx, grant)
}

func (g *granted) remember(value []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.served = append(g.served, bytes.Clone(value))
}

// mask is b with every value served so far masked, for what this tier repeats of a
// workload's own output.
func (g *granted) mask(b []byte) []byte {
	if g == nil {
		return b
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.served) == 0 {
		return b
	}
	var out bytes.Buffer
	r := vault.NewRedactor(&out)
	for _, v := range g.served {
		r.Add(v)
	}
	r.Write(b)
	r.Flush()
	return out.Bytes()
}

// vaulted is an instance's vault, released with it.
type vaulted struct {
	server  *vault.Server
	granted *granted
}

func (v *vaulted) hold(h vaulted) { *v = h }

// vaultHolder is both kinds of instance this tier starts.
type vaultHolder interface{ hold(vaulted) }

func (v *vaulted) closeVault() error {
	if v.server == nil {
		return nil
	}
	return v.server.Close()
}

// vaultFor serves a vault to a workload granted a secret, and the mount that puts it
// at /.taubyte. A workload granted none gets neither.
func (e *Runtime) vaultFor(spec invoke.Spec) (vaulted, []core.VolumeMount, error) {
	var grants map[string]invoke.Grant
	for _, g := range spec.Reaches {
		if g.Kind != secretKind {
			continue
		}
		if grants == nil {
			grants = map[string]invoke.Grant{}
		}
		grants[g.Label] = g
	}
	if grants == nil {
		return vaulted{}, nil, nil
	}

	g := &granted{grants: grants, secrets: e.opts.secrets}
	server, err := vault.Serve(g, g.remember)
	if err != nil {
		return vaulted{}, nil, fmt.Errorf("container: %s: %w", spec.Name, err)
	}
	mount := core.VolumeMount{Source: server.Dir(), Destination: vault.Dir, ReadOnly: true}
	return vaulted{server: server, granted: g}, []core.VolumeMount{mount}, nil
}
