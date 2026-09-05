package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// cache.go is the host half of `taubyte:handler/cache` — the first capability a
// guest gets beyond answering a request.
//
// # The configuration is the grant, and it is checked here
//
// A workload's `cache:` block names what it reaches and with which verbs. The
// compiler carries that through to invoke.Spec.Reaches, and every call below
// resolves the guest's label against it BEFORE the store is touched: a label
// nobody declared is `no-such-cache`, and a verb nobody asked for is `denied`.
//
// THERE IS NO SECOND PLACE TO WIDEN IT. The tier does not consult a policy, a
// node setting, or the cache resource itself — a grant that is not in the
// compiled object does not exist, which is what makes "what can this function
// touch" answerable by reading the repository.
//
// # What is deliberately not here
//
// AN AUTHORIZER. The vocabulary lets a cache name one — a function the target
// consults per operation — and this tier does not call it. A cache that
// declares one is refused where the grant is built (see the composer), rather
// than served as if it had not: serving it would be granting access the target
// said somebody else decides.
//
// A WATCH. `cache` declares the verb and the host ABI never had an export for
// it; whether it is a stream or a poll is undecided (see .notes/v2's WIT port).
// A resource asking for it gets `denied` from the op check, which is the honest
// answer: the platform does not do it yet.

// cacheInterface is the WIT interface these imports satisfy. The version
// suffix is stripped when matched, so one registration serves every patch.
const cacheInterface = "taubyte:handler/cache@0.1.0"

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

	// ErrNotFound is what a Caches implementation returns for a miss. The tier
	// maps it onto the guest's `not-found` arm, and anything else onto
	// `unavailable` — so a store that reported a miss as a failure would make
	// every cache miss look like an outage.
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

// Caches opens the cache a grant names.
//
// IT IS A SEAM BECAUSE A CACHE IS A REPLICATED STORE and this package's job is
// the ABI. What a handle means, where the bytes live and how a ceiling is
// enforced are pkg/cache's and the composer's; what is here is the lowering,
// the grant check and the mapping from a Go error onto an arm of a variant.
type Caches interface {
	// Open takes the whole grant rather than its handle, because what the
	// backing needs beyond "which one" — the declared lifetime and ceiling —
	// is on it. A handle alone would make the backing resolve it back into a
	// resource, which means the register on the request path.
	Open(ctx context.Context, grant invoke.Grant) (Cache, error)
}

// WithCaches is what backs `taubyte:handler/cache`. Optional: a tier without
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

const cacheKind = "cache"

// cacheImports registers the four functions for one instantiation.
//
// THE GRANTS ARE CAPTURED HERE, per instance, and not read per call. A spec is
// one revision of one resource, so its access block cannot change under a
// running instance — and a lookup that reached back into shared state would be
// a lookup that could answer differently between two calls of one request.
func (r *Runtime) cacheImports(spec invoke.Spec) []component.Option {
	// THE FOUR ARE REGISTERED EVEN WHEN NOTHING IS GRANTED, and that is the
	// difference between a clean refusal and a dead instance. An import a
	// component declares and the host does not provide is a TRAP — the guest
	// takes the whole instance with it — so a workload that calls a cache it
	// never declared would die instead of being told `no-such-cache`. A guest
	// bug is not a reason to lose warm state.
	grants := grantsOf(spec)

	// open resolves a label to a cache, or to the arm that says why not.
	open := func(ctx context.Context, label, op string) (Cache, *component.VariantValue) {
		grant, ok := grants[label]
		if !ok {
			return nil, failure(errNoSuchCache, label)
		}
		// A REFUSED GRANT SAYS WHY, because "denied" alone would send an
		// author looking at their own ops list for a reason that is not there.
		if grant.Refused != "" {
			return nil, failure(errDenied, grant.Refused)
		}
		if !grant.Allows(op) {
			return nil, failure(errDenied, label+" cannot "+op)
		}

		if r.caches == nil {
			// Unreachable while Start refuses a granted spec on a tier with no
			// backing. Kept because the alternative is a nil dereference in a
			// host function, which traps.
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

// arm maps a Go error onto the variant case the guest sees.
//
// A MISS IS ITS OWN ARM. Folding it into `unavailable` would make every cache
// miss — the common case, the thing a cache is for — read as an outage, and an
// author would write a retry around it.
func arm(err error) *component.VariantValue {
	switch {
	case errors.Is(err, ErrNotFound):
		return failure(errNotFound, "")
	case errors.Is(err, ErrTooLarge):
		return failure(errTooLarge, "")
	default:
		// THE STORE'S OWN WORDS DO CROSS HERE, unlike on a public ingress:
		// this reaches the workload's author, who owns both sides, and a
		// message that said only "unavailable" would make a misconfigured
		// cache indistinguishable from an unreachable peer.
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

// byteList lowers a Go slice into the list<u8> the ABI wants.
//
// IT IS A []byte AND NOT A []Value. wazy specialises list<u8> to the Go slice
// — which is what wasm.go's own requestValue already hands it for the request
// body — so building a Value per byte would be a 200KB allocation of interfaces
// per call, and the engine would reject it anyway.
//
// A NIL SLICE IS AN EMPTY LIST AND NOT A NIL VALUE: a stored empty value is a
// legitimate answer, and lowering it as nothing would hand the guest an absent
// case its type does not have.
func byteList(value []byte) []byte {
	if value == nil {
		return []byte{}
	}
	return value
}

// readBytes lifts the guest's list<u8>. The specialised shape is what arrives;
// the general one is accepted because a list that came from somewhere other
// than a guest's own vector is still a list.
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
