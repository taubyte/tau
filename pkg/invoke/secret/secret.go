// Package secret is the host half of `taubyte:secret/vault`, written once for every
// engine: a guest reads a secret its access block grants, when it asks, so a rotation
// is seen by the next read. Nothing is cached here, or it would be an environment
// variable again.
package secret

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/hostmodule"
	witfs "github.com/taubyte/tau/pkg/invoke/wasm/wit"
	"github.com/taubyte/tau/pkg/specs"
)

// Definition is the interface, read from the WIT guests are built against.
var Definition = hostmodule.MustDefine(witfs.FS, "deps/taubyte-secret", "taubyte:secret/vault@0.1.0")

// The vocabulary declares one verb for this kind, and it is a read.
const opGet = "get"

// The cases of the WIT's error variant.
const (
	caseNoSuchSecret = "no-such-secret"
	caseDenied       = "denied"
	caseNotSet       = "not-set"
	caseUnavailable  = "unavailable"
)

// ErrNotSet is a declared secret with no value: its own arm, never an empty string.
var ErrNotSet = errors.New("secret: nothing is set under that name")

// Reader reads the value a grant names.
type Reader interface {
	SecretFor(ctx context.Context, grant invoke.Grant) ([]byte, error)
}

// GrantsOf is every secret a spec is granted, by label.
func GrantsOf(spec invoke.Spec) map[string]invoke.Grant {
	var out map[string]invoke.Grant
	for _, g := range spec.Reaches {
		if g.Kind != specs.KindSecret {
			continue
		}
		if out == nil {
			out = map[string]invoke.Grant{}
		}
		out[g.Label] = g
	}
	return out
}

// Module is spec's vault: what it is granted, read through r. A nil r answers
// unavailable, so the guest is told rather than trapped.
func Module(spec invoke.Spec, r Reader) hostmodule.Module {
	grants := GrantsOf(spec)
	get := func(ctx context.Context, args []hostmodule.Value) (hostmodule.Value, error) {
		label, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("secret.get: argument is %T, want string", args[0])
		}

		grant, ok := grants[label]
		switch {
		case !ok:
			return failure(caseNoSuchSecret, label), nil
		case grant.Refused != "":
			return failure(caseDenied, grant.Refused), nil
		case !grant.Allows(opGet):
			return failure(caseDenied, label+" cannot get"), nil
		case r == nil:
			return failure(caseUnavailable, "this node has nothing to read a secret with"), nil
		}

		value, err := r.SecretFor(ctx, grant)
		switch {
		case errors.Is(err, ErrNotSet):
			return hostmodule.Fail(hostmodule.Variant{Case: caseNotSet}), nil
		case err != nil:
			// The value never appears in a message.
			return failure(caseUnavailable, err.Error()), nil
		}
		return hostmodule.Ok(value), nil
	}

	m, err := Definition.Implement(map[string]hostmodule.Func{"get": get})
	if err != nil {
		// The definition is this package's own WIT, and get is all it declares.
		panic(err)
	}
	return m
}

func failure(c, detail string) hostmodule.Result {
	return hostmodule.Fail(hostmodule.Variant{Case: c, Payload: detail})
}
