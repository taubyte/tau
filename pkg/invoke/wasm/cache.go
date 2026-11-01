package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/specs"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// cache.go is the host half of `taubyte:cache/store`. The workload's `cache:`
// block, carried in invoke.Spec.Reaches, is checked before the store is touched:
// an undeclared label is `no-such-cache`, an unrequested verb `denied`. There is no
// second place to widen it. A cache naming an authorizer is refused by the
// composer; `watch` has no export yet and gets `denied`.

// cacheInterface is the WIT interface these imports satisfy. The version
// suffix is stripped when matched, so one registration serves every patch.
const cacheInterface = "taubyte:cache/store@0.1.0"

// The vocabulary's verbs for the cache kind. get/list are reads, put/delete
// writes; `watch` is the fifth and is not implemented.
const (
	opGet    = "get"
	opPut    = "put"
	opDelete = "delete"
	opList   = "list"
)

// The error variant's cases, in the order handler.wit declares them.
const (
	errNoSuchCache uint32 = iota
	errDenied
	errNotFound
	errTooLarge
	errUnavailable
)

var (
	// ErrNoCaches is a spec that grants a cache on a tier composed without a
	// backing. It is a construction fault rather than a guest's, so it is
	// refused at Start rather than at the first call.
	ErrNoCaches = errors.New("wasm: this resource reaches a cache and this tier has nothing to open one with")

	// ErrNotFound is a Caches miss, mapped to `not-found`; anything else is
	// `unavailable`.
	ErrNotFound = errors.New("wasm: no such entry")

	// ErrTooLarge is a value bigger than the whole cache.
	ErrTooLarge = errors.New("wasm: that value is larger than the whole cache")
)

// Cache is one cache, as this tier uses it.
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)
}

// Caches opens the cache a grant names; storage and ceilings are pkg/cache's and
// the composer's.
type Caches interface {
	// Open takes the whole grant, whose settings carry the lifetime and ceiling, so the
	// backing never resolves a handle via the register.
	Open(ctx context.Context, grant invoke.Grant) (Cache, error)
}

// WithCaches is what backs `taubyte:cache/store`. Optional: a tier without
// one runs everything that reaches no cache, and refuses at Start anything
// that does.
func WithCaches(c Caches) Option {
	return func(r *Runtime) { r.caches = c }
}

// grantsOf is the cache entries of a spec's access block, by label.
func grantsOf(spec invoke.Spec) map[string]invoke.Grant {
	var out map[string]invoke.Grant
	for _, g := range spec.Reaches {
		if g.Kind != cacheKind {
			continue
		}
		if out == nil {
			out = map[string]invoke.Grant{}
		}
		out[g.Label] = g
	}
	return out
}

const cacheKind = specs.KindCache

// cacheImports registers the four functions per instance, capturing the grants:
// a spec's access block cannot change under a running instance.
func (r *Runtime) cacheImports(spec invoke.Spec) []component.Option {
	// Registered even with nothing granted: a missing import traps and kills the
	// instance, where a registered one can answer `no-such-cache`.
	grants := grantsOf(spec)

	// open resolves a label to a cache, or to the arm that says why not.
	open := func(ctx context.Context, label, op string) (Cache, *component.VariantValue) {
		// A refused grant says why rather than a bare "denied".
		grant, why := r.addressed(ctx, spec, grants, label, op)
		if why != nil {
			if why.missing {
				return nil, failure(errNoSuchCache, why.detail)
			}
			if why.unavailable {
				return nil, failure(errUnavailable, why.detail)
			}
			return nil, failure(errDenied, why.detail)
		}

		if r.caches == nil {
			// Unreachable while Start refuses a granted spec with no backing; a nil dereference
			// here would trap.
			return nil, failure(errUnavailable, "this node has nothing to open a cache with")
		}

		cache, err := r.caches.Open(ctx, grant)
		if err != nil {
			return nil, failure(errUnavailable, err.Error())
		}
		return cache, nil
	}

	bytes := func(t *component.TypeTable) component.TypeRef {
		return t.List(component.Prim("u8"))
	}

	// The error variant, rebuilt per signature: a TypeTable is one function's
	// type space and a ref from one cannot be used in another.
	errorType := func(t *component.TypeTable) component.TypeRef {
		return t.Variant(
			component.VariantCaseSpec{Name: "no-such-cache", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "denied", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "not-found"},
			component.VariantCaseSpec{Name: "too-large"},
			component.VariantCaseSpec{Name: "unavailable", Type: component.Prim("string")},
		)
	}

	getTbl := component.NewTypeTable()
	getFD := getTbl.Func(
		[]component.TypeRef{component.Prim("string"), component.Prim("string")},
		getTbl.Result(bytes(getTbl), errorType(getTbl)),
	)
	get := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		label, key, err := twoStrings(args)
		if err != nil {
			return nil, err
		}
		cache, refused := open(ctx, label, opGet)
		if refused != nil {
			return errorResult(refused), nil
		}

		value, err := cache.Get(ctx, key)
		if err != nil {
			return errorResult(arm(err)), nil
		}
		return []component.Value{component.ResultValue{Payload: byteList(value)}}, nil
	}

	putTbl := component.NewTypeTable()
	putFD := putTbl.Func(
		[]component.TypeRef{component.Prim("string"), component.Prim("string"), bytes(putTbl)},
		putTbl.Result(component.TypeRef{}, errorType(putTbl)),
	)
	put := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("cache.put: expected 3 arguments, got %d", len(args))
		}
		label, key, err := twoStrings(args)
		if err != nil {
			return nil, err
		}
		value, err := readBytes(args[2])
		if err != nil {
			return nil, fmt.Errorf("cache.put: %w", err)
		}

		cache, refused := open(ctx, label, opPut)
		if refused != nil {
			return errorResult(refused), nil
		}
		if err := cache.Put(ctx, key, value); err != nil {
			return errorResult(arm(err)), nil
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
		cache, refused := open(ctx, label, opDelete)
		if refused != nil {
			return errorResult(refused), nil
		}
		if err := cache.Delete(ctx, key); err != nil {
			return errorResult(arm(err)), nil
		}
		return []component.Value{component.ResultValue{}}, nil
	}

	listTbl := component.NewTypeTable()
	listFD := listTbl.Func(
		[]component.TypeRef{component.Prim("string"), component.Prim("string")},
		listTbl.Result(listTbl.List(component.Prim("string")), errorType(listTbl)),
	)
	list := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		label, prefix, err := twoStrings(args)
		if err != nil {
			return nil, err
		}
		cache, refused := open(ctx, label, opList)
		if refused != nil {
			return errorResult(refused), nil
		}

		names, err := cache.List(ctx, prefix)
		if err != nil {
			return errorResult(arm(err)), nil
		}

		out := make([]component.Value, 0, len(names))
		for _, name := range names {
			out = append(out, name)
		}
		return []component.Value{component.ResultValue{Payload: out}}, nil
	}

	return []component.Option{
		component.WithImportCustom(cacheInterface, opGet, get, getFD, getTbl.Resolver()),
		component.WithImportCustom(cacheInterface, opPut, put, putFD, putTbl.Resolver()),
		component.WithImportCustom(cacheInterface, opDelete, remove, deleteFD, deleteTbl.Resolver()),
		component.WithImportCustom(cacheInterface, opList, list, listFD, listTbl.Resolver()),
	}
}

// arm maps a Go error onto the guest's variant case; a miss is its own arm so it is
// never read as an outage.
func arm(err error) *component.VariantValue {
	switch {
	case errors.Is(err, ErrNotFound):
		return failure(errNotFound, "")
	case errors.Is(err, ErrTooLarge):
		return failure(errTooLarge, "")
	default:
		// The store's words cross here: they reach the workload's own author.
		return failure(errUnavailable, err.Error())
	}
}

// failure builds one arm. The payload-free cases carry nothing, which is what
// the variant declares.
func failure(disc uint32, detail string) *component.VariantValue {
	v := component.VariantValue{Disc: disc}
	if disc == errNoSuchCache || disc == errDenied || disc == errUnavailable {
		v.Payload = detail
	}
	return &v
}

func errorResult(v *component.VariantValue) []component.Value {
	return []component.Value{component.ResultValue{IsErr: true, Payload: *v}}
}

func twoStrings(args []component.Value) (first, second string, err error) {
	if len(args) < 2 {
		return "", "", fmt.Errorf("cache: expected 2 string arguments, got %d", len(args))
	}
	first, ok := args[0].(string)
	if !ok {
		return "", "", fmt.Errorf("cache: first argument is %T, want string", args[0])
	}
	second, ok = args[1].(string)
	if !ok {
		return "", "", fmt.Errorf("cache: second argument is %T, want string", args[1])
	}
	return first, second, nil
}

// liftU32 reads a u32 argument, accepting a widened scalar too, as either crosses
// the ABI.
func liftU32(v component.Value) (uint32, error) {
	switch n := v.(type) {
	case uint32:
		return n, nil
	case uint64:
		return uint32(n), nil
	case int32:
		return uint32(n), nil
	case int64:
		return uint32(n), nil
	case nil:
		return 0, nil
	}
	return 0, fmt.Errorf("expected a u32, got %T", v)
}

// byteList lowers a slice into list<u8> as a []byte, which wazy specialises; a nil
// slice becomes an empty list, never an absent value.
func byteList(value []byte) []byte {
	if value == nil {
		return []byte{}
	}
	return value
}

// readBytes lifts list<u8>, accepting the general shape too.
func readBytes(v component.Value) ([]byte, error) {
	switch value := v.(type) {
	case []byte:
		return value, nil
	case []component.Value:
		out := make([]byte, 0, len(value))
		for i, item := range value {
			b, ok := item.(uint8)
			if !ok {
				return nil, fmt.Errorf("element %d is %T, want u8", i, item)
			}
			out = append(out, b)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected a list, got %T", v)
	}
}
