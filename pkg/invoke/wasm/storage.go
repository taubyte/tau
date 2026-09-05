package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// storage.go is the host half of `taubyte:handler/storage` — the `storage` kind
// at `type: object`.
//
// IT IS NOT kv.go WITH A THIRD NAME. The verbs overlap and the thing does not:
// a kv database holds values a guest wrote and this holds OBJECTS, which have a
// size and a modification time that are worth asking about without reading
// them, and which are content-addressed underneath so two identical ones are
// one. `head` is what that difference buys, and a shared interface would have
// nowhere to put it.

const storageInterface = "taubyte:handler/storage@0.1.0"

// The vocabulary's verbs for the storage kind.
const (
	opObjectGet    = "get"
	opObjectHead   = "head"
	opObjectPut    = "put"
	opObjectDelete = "delete"
	opObjectList   = "list"
)

// entriesExport is what the guest calls for `list`, because `list` is a WIT
// keyword. The op checked is still `list` — see kv.go's keysExport.
const entriesExport = "entries"

// The error variant's cases, in the order handler.wit declares them.
const (
	errNoSuchStorage uint32 = iota
	errStorageDenied
	errStorageNotFound
	errStorageFull
	errStorageUnavailable
)

var (
	// ErrNoStorage is a spec that grants storage on a tier composed without a
	// backing.
	ErrNoStorage = errors.New("wasm: this resource reaches storage and this tier has nothing to open a bucket with")

	// ErrFull is a bucket at the size its author declared. It is its own arm
	// because a caller acts on it — write less, delete something — where an
	// `unavailable` reads as an outage.
	ErrFull = errors.New("wasm: the bucket is at its declared size")
)

// Object is what is known about one stored object without reading it.
type Object struct {
	Name string
	Size int64

	// Modified is unix SECONDS. The vocabulary has no field finer and neither
	// does any consumer; nanoseconds would be a precision nothing sets.
	Modified int64
}

// Bucket is one object store.
type Bucket interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Head(ctx context.Context, key string) (Object, error)
	Put(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error

	// Entries are the objects under a prefix, sorted by name.
	Entries(ctx context.Context, prefix string) ([]Object, error)
}

// Buckets opens the one a grant names. The method is named for what it opens,
// for the reason KVs.OpenKV is.
type Buckets interface {
	OpenBucket(ctx context.Context, grant invoke.Grant) (Bucket, error)
}

// WithStorage is what backs `taubyte:handler/storage`.
func WithStorage(b Buckets) Option {
	return func(r *Runtime) { r.storage = b }
}

// storageKind is the vocabulary's own name for the kind.
const storageKind = "storage"

// storageGrantsOf is every storage grant, whatever its type.
//
// THE TYPE IS NOT FILTERED HERE, unlike a database's. A storage declaring
// `streaming` is a grant this interface cannot serve, and dropping it would
// tell the guest "no such storage" — which reads as a typo in a label that is
// perfectly correct. The composer refuses it by name instead, and what arrives
// here is a refused grant carrying the reason.
func storageGrantsOf(spec invoke.Spec) map[string]invoke.Grant {
	var out map[string]invoke.Grant
	for _, g := range spec.Reaches {
		if g.Kind != storageKind {
			continue
		}
		if out == nil {
			out = map[string]invoke.Grant{}
		}
		out[g.Label] = g
	}
	return out
}

// servableStorage is the storage grants this tier would have to serve — the
// refused ones do not count.
//
// A REFUSED GRANT MUST NOT REFUSE THE WORKLOAD. The composer refuses a
// `streaming` storage by name, and a node with no bucket backing would
// otherwise decline to start a resource whose only storage was one it was never
// going to serve anyway — turning a per-target refusal back into a per-workload
// one, which is exactly what Grant.Refused exists to avoid.
func servableStorage(spec invoke.Spec) map[string]invoke.Grant {
	var out map[string]invoke.Grant
	for label, g := range storageGrantsOf(spec) {
		if g.Refused != "" {
			continue
		}
		if out == nil {
			out = map[string]invoke.Grant{}
		}
		out[label] = g
	}
	return out
}

// storageImports registers the five functions for one instantiation.
func (r *Runtime) storageImports(spec invoke.Spec) []component.Option {
	grants := storageGrantsOf(spec)

	open := func(ctx context.Context, label, op string) (Bucket, *component.VariantValue) {
		grant, why := r.addressed(ctx, grants, label, op)
		if why != nil {
			if why.missing {
				return nil, storageFailure(errNoSuchStorage, why.detail)
			}
			return nil, storageFailure(errStorageDenied, why.detail)
		}
		if r.storage == nil {
			return nil, storageFailure(errStorageUnavailable, "this node has nothing to open a bucket with")
		}

		bucket, err := r.storage.OpenBucket(ctx, grant)
		if err != nil {
			return nil, storageFailure(errStorageUnavailable, err.Error())
		}
		return bucket, nil
	}

	errorType := func(t *component.TypeTable) component.TypeRef {
		return t.Variant(
			component.VariantCaseSpec{Name: "no-such-storage", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "denied", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "not-found"},
			component.VariantCaseSpec{Name: "full", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "unavailable", Type: component.Prim("string")},
		)
	}
	entryType := func(t *component.TypeTable) component.TypeRef {
		return t.Record(
			"name", component.Prim("string"),
			"size", component.Prim("u64"),
			"modified", component.Prim("s64"),
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
		bucket, refused := open(ctx, label, opObjectGet)
		if refused != nil {
			return errorResult(refused), nil
		}

		value, err := bucket.Get(ctx, key)
		if err != nil {
			return errorResult(storageArm(err)), nil
		}
		return []component.Value{component.ResultValue{Payload: byteList(value)}}, nil
	}

	headTbl := component.NewTypeTable()
	headFD := headTbl.Func(
		[]component.TypeRef{component.Prim("string"), component.Prim("string")},
		headTbl.Result(entryType(headTbl), errorType(headTbl)),
	)
	head := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		label, key, err := twoStrings(args)
		if err != nil {
			return nil, err
		}
		bucket, refused := open(ctx, label, opObjectHead)
		if refused != nil {
			return errorResult(refused), nil
		}

		object, err := bucket.Head(ctx, key)
		if err != nil {
			return errorResult(storageArm(err)), nil
		}
		return []component.Value{component.ResultValue{Payload: entryValue(object)}}, nil
	}

	putTbl := component.NewTypeTable()
	putFD := putTbl.Func(
		[]component.TypeRef{component.Prim("string"), component.Prim("string"), putTbl.List(component.Prim("u8"))},
		putTbl.Result(component.TypeRef{}, errorType(putTbl)),
	)
	put := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("storage.put: expected 3 arguments, got %d", len(args))
		}
		label, key, err := twoStrings(args)
		if err != nil {
			return nil, err
		}
		value, err := readBytes(args[2])
		if err != nil {
			return nil, fmt.Errorf("storage.put: %w", err)
		}

		bucket, refused := open(ctx, label, opObjectPut)
		if refused != nil {
			return errorResult(refused), nil
		}
		if err := bucket.Put(ctx, key, value); err != nil {
			return errorResult(storageArm(err)), nil
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
		bucket, refused := open(ctx, label, opObjectDelete)
		if refused != nil {
			return errorResult(refused), nil
		}
		if err := bucket.Delete(ctx, key); err != nil {
			return errorResult(storageArm(err)), nil
		}
		return []component.Value{component.ResultValue{}}, nil
	}

	entriesTbl := component.NewTypeTable()
	entriesFD := entriesTbl.Func(
		[]component.TypeRef{component.Prim("string"), component.Prim("string")},
		entriesTbl.Result(entriesTbl.List(entryType(entriesTbl)), errorType(entriesTbl)),
	)
	entries := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		label, prefix, err := twoStrings(args)
		if err != nil {
			return nil, err
		}
		bucket, refused := open(ctx, label, opObjectList)
		if refused != nil {
			return errorResult(refused), nil
		}

		found, err := bucket.Entries(ctx, prefix)
		if err != nil {
			return errorResult(storageArm(err)), nil
		}

		out := make([]component.Value, 0, len(found))
		for _, object := range found {
			out = append(out, entryValue(object))
		}
		return []component.Value{component.ResultValue{Payload: out}}, nil
	}

	return []component.Option{
		component.WithImportCustom(storageInterface, opObjectGet, get, getFD, getTbl.Resolver()),
		component.WithImportCustom(storageInterface, opObjectHead, head, headFD, headTbl.Resolver()),
		component.WithImportCustom(storageInterface, opObjectPut, put, putFD, putTbl.Resolver()),
		component.WithImportCustom(storageInterface, opObjectDelete, remove, deleteFD, deleteTbl.Resolver()),
		component.WithImportCustom(storageInterface, entriesExport, entries, entriesFD, entriesTbl.Resolver()),
	}
}

// entryValue is one object as the record the interface declares. A record is a
// flat list of its fields in declaration order — the same shape a vector match
// is lowered as.
func entryValue(object Object) component.Value {
	return []component.Value{object.Name, uint64(object.Size), object.Modified}
}

// storageArm maps a Go error onto the arm the guest sees.
func storageArm(err error) *component.VariantValue {
	switch {
	case errors.Is(err, ErrNotFound):
		return storageFailure(errStorageNotFound, "")
	case errors.Is(err, ErrFull):
		return storageFailure(errStorageFull, err.Error())
	}
	return storageFailure(errStorageUnavailable, err.Error())
}

func storageFailure(disc uint32, detail string) *component.VariantValue {
	v := component.VariantValue{Disc: disc}
	if disc != errStorageNotFound {
		v.Payload = detail
	}
	return &v
}
