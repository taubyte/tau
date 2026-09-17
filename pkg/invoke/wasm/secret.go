package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// secret.go is the host half of `taubyte:secret/vault`.
//
// # It is the rotation path, not a second way to do the same thing
//
// A secret already reaches a workload as an environment variable, resolved once
// at start. That is right for most of them and wrong for exactly one case: a
// rotated secret does not reach a running instance. Reading it here takes
// effect on the next call, which is what makes "rotation needs no push" true
// rather than "rotation needs no push, only a redeploy".
//
// # The value is not cached here
//
// Whatever backs this may cache; this does not. A host function that remembered
// a secret for the life of an instance would be the environment variable again,
// wearing a function call.

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

	// ErrNotSet is a declared secret with no value. It is its OWN arm and never
	// an empty string: a workload handed "" for its database password fails
	// somewhere else, later, as an application that looks broken.
	ErrNotSet = errors.New("wasm: nothing is set under that name")
)

const secretKind = "secret"

// Secrets reads the value a grant names.
//
// THE METHOD IS NAMED FOR WHAT IT TAKES, like KVs and DocumentStores: one type
// backs this and services/worker's own Secrets seam, and that one resolves a
// secret from a project, an environment and an id rather than from a grant.
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

// secretArm maps a Go error onto the arm the guest sees.
//
// THE VALUE IS NEVER IN THE MESSAGE, and neither is anything a backing might
// have wrapped it with. `not-set` carries nothing at all: the caller already
// knows which name it asked for, and a message here would be one more place a
// secret's name travels.
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
