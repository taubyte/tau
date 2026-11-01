package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/specs"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// secret.go is the host half of `taubyte:secret/vault`: the rotation path, since an
// environment variable resolved at start never sees a rotated secret. Not cached
// here, or it would be the environment variable again.

const secretInterface = "taubyte:secret/vault@0.1.0"

// The vocabulary declares one verb for this kind, and it is a read.
const opGetSecret = "get"

// The error variant's cases, in the order handler.wit declares them.
const (
	errNoSuchSecret uint32 = iota
	errSecretDenied
	errNotSet
	errSecretUnavailable
)

var (
	// ErrNoSecrets is a spec that grants a secret on a tier composed without a
	// backing.
	ErrNoSecrets = errors.New("wasm: this resource reads a secret and this tier has nothing to read one with")

	// ErrNotSet is a declared secret with no value: its own arm, never an empty string.
	ErrNotSet = errors.New("wasm: nothing is set under that name")
)

const secretKind = specs.KindSecret

// Secrets reads the value a grant names; the method is named so one type also backs
// services/worker's Secrets seam.
type Secrets interface {
	SecretFor(ctx context.Context, grant invoke.Grant) ([]byte, error)
}

// WithSecrets is what backs `taubyte:secret/vault`.
func WithSecrets(s Secrets) Option {
	return func(r *Runtime) { r.secrets = s }
}

func secretGrantsOf(spec invoke.Spec) map[string]invoke.Grant {
	var out map[string]invoke.Grant
	for _, g := range spec.Reaches {
		if g.Kind != secretKind {
			continue
		}
		if out == nil {
			out = map[string]invoke.Grant{}
		}
		out[g.Label] = g
	}
	return out
}

// secretImports registers get for one instantiation.
func (r *Runtime) secretImports(spec invoke.Spec) []component.Option {
	grants := secretGrantsOf(spec)

	table := component.NewTypeTable()
	failureRef := table.Variant(
		component.VariantCaseSpec{Name: "no-such-secret", Type: component.Prim("string")},
		component.VariantCaseSpec{Name: "denied", Type: component.Prim("string")},
		component.VariantCaseSpec{Name: "not-set"},
		component.VariantCaseSpec{Name: "unavailable", Type: component.Prim("string")},
	)
	descriptor := table.Func(
		[]component.TypeRef{component.Prim("string")},
		table.Result(table.List(component.Prim("u8")), failureRef),
	)

	get := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("secret.get: expected 1 argument, got %d", len(args))
		}
		label, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("secret.get: argument is %T, want string", args[0])
		}

		grant, ok := grants[label]
		if !ok {
			return errorResult(secretFailure(errNoSuchSecret, label)), nil
		}
		if grant.Refused != "" {
			return errorResult(secretFailure(errSecretDenied, grant.Refused)), nil
		}
		if !grant.Allows(opGetSecret) {
			return errorResult(secretFailure(errSecretDenied, label+" cannot get")), nil
		}
		if r.secrets == nil {
			return errorResult(secretFailure(errSecretUnavailable, "this node has nothing to read a secret with")), nil
		}

		value, err := r.secrets.SecretFor(ctx, grant)
		if err != nil {
			return errorResult(secretArm(err)), nil
		}
		return []component.Value{component.ResultValue{Payload: byteList(value)}}, nil
	}

	return []component.Option{
		component.WithImportCustom(secretInterface, opGetSecret, get, descriptor, table.Resolver()),
	}
}

// secretArm maps a Go error onto the guest's arm. The value never appears in a
// message, and `not-set` carries nothing.
func secretArm(err error) *component.VariantValue {
	if errors.Is(err, ErrNotSet) {
		return secretFailure(errNotSet, "")
	}
	return secretFailure(errSecretUnavailable, err.Error())
}

func secretFailure(disc uint32, detail string) *component.VariantValue {
	v := component.VariantValue{Disc: disc}
	if disc != errNotSet {
		v.Payload = detail
	}
	return &v
}
