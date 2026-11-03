// Package secret is the host half of `taubyte:secret/vault`, written once for every
// engine: a guest reads a secret its access block grants, when it asks, so a rotation
// is seen by the next read. Nothing is cached here, or it would be an environment
// variable again.
package secret

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/samyfodil/warpjs/hostmod"
	"github.com/samyfodil/wazy/component"
	"github.com/samyfodil/wazy/component/wit"

	"github.com/taubyte/tau/pkg/invoke"
	witfs "github.com/taubyte/tau/pkg/invoke/wasm/wit"
	"github.com/taubyte/tau/pkg/specs"
)

// Interface is the vault as a guest imports it.
const Interface = "taubyte:secret/vault@0.1.0"

// The vocabulary declares one verb for this kind, and it is a read.
const opGet = "get"

// The cases of the WIT's error variant.
const (
	caseNoSuchSecret = "no-such-secret"
	caseDenied       = "denied"
	caseNotSet       = "not-set"
	caseUnavailable  = "unavailable"
)

// definition is the WIT guests are built against, read once, and cases its error
// variant's case indices by name.
var definition, cases = define()

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

// Register defines the vault in reg and implements it as spec's: what it is granted,
// read through r. A nil r answers unavailable, so the guest is told rather than
// trapped.
func Register(reg *hostmod.Registry, spec invoke.Spec, r Reader) error {
	if err := reg.Define(definition); err != nil {
		return err
	}
	grants := GrantsOf(spec)
	get := func(ctx context.Context, args []hostmod.Value) ([]hostmod.Value, error) {
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
			return failure(caseNotSet, nil), nil
		case err != nil:
			// The value never appears in a message.
			return failure(caseUnavailable, err.Error()), nil
		}
		return []hostmod.Value{hostmod.ResultValue{Payload: value}}, nil
	}
	return reg.Implement(Interface, "get", get)
}

func failure(c string, detail hostmod.Value) []hostmod.Value {
	return []hostmod.Value{hostmod.ResultValue{IsErr: true, Payload: hostmod.VariantValue{Disc: cases[c], Payload: detail}}}
}

// define is the vault's WIT and its error's case indices, which the WIT orders.
func define() (*wit.Package, map[string]uint32) {
	pkg, err := wit.ParseFS(witfs.FS, "deps/taubyte-secret")
	if err != nil {
		panic(err)
	}
	iface := pkg.Interface(Interface)
	i := slices.IndexFunc(iface.Funcs, func(f *wit.Func) bool { return f.Name == opGet })
	sig, err := iface.Funcs[i].Signature(nil)
	if err != nil {
		panic(err)
	}
	resolve := sig.Types.Resolver()
	result := resolve(*sig.Desc.Results.Unnamed.TypeIndex).(component.ResultDesc)
	variant := resolve(*result.Err.TypeIndex).(component.VariantDesc)
	out := map[string]uint32{}
	for i, c := range variant.Cases {
		out[c.Name] = uint32(i)
	}
	return pkg, out
}
