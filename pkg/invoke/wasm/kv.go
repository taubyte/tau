package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/specs"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// kv.go is the host half of `taubyte:kv/store`, the `database` kind at `type: kv`
// (tau's own engine). Not cache.go with a flag: a cache may lose entries and a
// database may not, so they are separate interfaces.

const kvInterface = "taubyte:kv/store@0.1.0"

// The vocabulary's verbs for the kv data model. `watch` is the fifth and is not
// served — see the interface doc.
const (
	opKVGet    = "get"
	opKVPut    = "put"
	opKVDelete = "delete"
	opKVList   = "list"
)

// keysExport is the guest's call for the `list` verb; `list` is a WIT keyword.
const keysExport = "keys"

// The error variant's cases, in the order handler.wit declares them.
const (
	errNoSuchKV uint32 = iota
	errKVDenied
	errKVNotFound
	errKVUnavailable
)

// ErrNoKV is a spec that grants a kv database on a tier composed without a
// backing.
var ErrNoKV = errors.New("wasm: this resource reaches a kv database and this tier has nothing to open one with")

// KV is one key-value database.
type KV interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	Keys(ctx context.Context, prefix string) ([]string, error)
}

// KVs opens the kv database a grant names; named rather than Open so one type can
// back several data models.
type KVs interface {
	OpenKV(ctx context.Context, grant invoke.Grant) (KV, error)
}

// WithKV is what backs `taubyte:kv/store`.
func WithKV(k KVs) Option {
	return func(r *Runtime) { r.kv = k }
}

// kvGrantsOf is the database grants whose data model is kv: filtered by kind here
// and by `type` in the composer.
func kvGrantsOf(spec invoke.Spec) map[string]invoke.Grant {
	var out map[string]invoke.Grant
	for _, g := range spec.Reaches {
		if g.Kind != databaseKind || g.Settings[specs.DatabaseKeyType] != specs.DatabaseTypeKv {
			continue
		}
		if out == nil {
			out = map[string]invoke.Grant{}
		}
		out[g.Label] = g
	}
	return out
}

// kvImports registers the four functions for one instantiation.
func (r *Runtime) kvImports(spec invoke.Spec) []component.Option {
	grants := kvGrantsOf(spec)

	open := func(ctx context.Context, label, op string) (KV, *component.VariantValue) {
		grant, why := r.addressed(ctx, spec, grants, label, op)
		if why != nil {
			if why.missing {
				return nil, kvFailure(errNoSuchKV, why.detail)
			}
			if why.unavailable {
				return nil, kvFailure(errKVUnavailable, why.detail)
			}
			return nil, kvFailure(errKVDenied, why.detail)
		}
		if r.kv == nil {
			return nil, kvFailure(errKVUnavailable, "this node has nothing to open a database with")
		}

		db, err := r.kv.OpenKV(ctx, grant)
		if err != nil {
			return nil, kvFailure(errKVUnavailable, err.Error())
		}
		return db, nil
	}

	errorType := func(t *component.TypeTable) component.TypeRef {
		return t.Variant(
			component.VariantCaseSpec{Name: "no-such-database", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "denied", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "not-found"},
			component.VariantCaseSpec{Name: "unavailable", Type: component.Prim("string")},
		)
	}

	getTbl := component.NewTypeTable()
	getFD := getTbl.Func(
		[]component.TypeRef{component.Prim("string"), component.Prim("string")},
		getTbl.Result(getTbl.List(component.Prim("u8")), errorType(getTbl)),
	)
	get := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		label, key, err := twoStrings(args)
		if err != nil {
			return nil, err
		}
		db, refused := open(ctx, label, opKVGet)
		if refused != nil {
			return errorResult(refused), nil
		}

		value, err := db.Get(ctx, key)
		if err != nil {
			return errorResult(kvArm(err)), nil
		}
		return []component.Value{component.ResultValue{Payload: byteList(value)}}, nil
	}

	putTbl := component.NewTypeTable()
	putFD := putTbl.Func(
		[]component.TypeRef{component.Prim("string"), component.Prim("string"), putTbl.List(component.Prim("u8"))},
		putTbl.Result(component.TypeRef{}, errorType(putTbl)),
	)
	put := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("kv.put: expected 3 arguments, got %d", len(args))
		}
		label, key, err := twoStrings(args)
		if err != nil {
			return nil, err
		}
		value, err := readBytes(args[2])
		if err != nil {
			return nil, fmt.Errorf("kv.put: %w", err)
		}

		db, refused := open(ctx, label, opKVPut)
		if refused != nil {
			return errorResult(refused), nil
		}
		if err := db.Put(ctx, key, value); err != nil {
			return errorResult(kvArm(err)), nil
		}
		return []component.Value{component.ResultValue{}}, nil
	}

	deleteTbl := component.NewTypeTable()
	deleteFD := deleteTbl.Func(
		[]component.TypeRef{component.Prim("string"), component.Prim("string")},
		deleteTbl.Result(component.TypeRef{}, errorType(deleteTbl)),
	)
	remove := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		label, key, err := twoStrings(args)
		if err != nil {
			return nil, err
		}
		db, refused := open(ctx, label, opKVDelete)
		if refused != nil {
			return errorResult(refused), nil
		}
		if err := db.Delete(ctx, key); err != nil {
			return errorResult(kvArm(err)), nil
		}
		return []component.Value{component.ResultValue{}}, nil
	}

	keysTbl := component.NewTypeTable()
	keysFD := keysTbl.Func(
		[]component.TypeRef{component.Prim("string"), component.Prim("string")},
		keysTbl.Result(keysTbl.List(component.Prim("string")), errorType(keysTbl)),
	)
	keys := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		label, prefix, err := twoStrings(args)
		if err != nil {
			return nil, err
		}
		db, refused := open(ctx, label, opKVList)
		if refused != nil {
			return errorResult(refused), nil
		}

		names, err := db.Keys(ctx, prefix)
		if err != nil {
			return errorResult(kvArm(err)), nil
		}

		out := make([]component.Value, 0, len(names))
		for _, name := range names {
			out = append(out, name)
		}
		return []component.Value{component.ResultValue{Payload: out}}, nil
	}

	return []component.Option{
		component.WithImportCustom(kvInterface, opKVGet, get, getFD, getTbl.Resolver()),
		component.WithImportCustom(kvInterface, opKVPut, put, putFD, putTbl.Resolver()),
		component.WithImportCustom(kvInterface, opKVDelete, remove, deleteFD, deleteTbl.Resolver()),
		component.WithImportCustom(kvInterface, keysExport, keys, keysFD, keysTbl.Resolver()),
	}
}

// kvArm maps a Go error onto the guest's arm; a miss is its own arm.
func kvArm(err error) *component.VariantValue {
	if errors.Is(err, ErrNotFound) {
		return kvFailure(errKVNotFound, "")
	}
	return kvFailure(errKVUnavailable, err.Error())
}

func kvFailure(disc uint32, detail string) *component.VariantValue {
	v := component.VariantValue{Disc: disc}
	if disc != errKVNotFound {
		v.Payload = detail
	}
	return &v
}
