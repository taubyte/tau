package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/specs"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// vector.go is the host half of `taubyte:vector/store`, the `database` kind at
// `type: vector`. An embedding crosses as list<f32>, never text; its meta is JSON,
// as in document.go.

const vectorInterface = "taubyte:vector/store@0.1.0"

const (
	opVectorSearch = "search"
	opVectorUpsert = "upsert"
	opVectorDelete = "delete"
)

// The error variant's cases, in the order handler.wit declares them.
const (
	errNoSuchVector uint32 = iota
	errVectorDenied
	errWrongWidth
	errVectorMalformed
	errVectorUnavailable
)

var (
	// ErrNoVectors is a spec that grants a vector database on a tier composed
	// without a backing.
	ErrNoVectors = errors.New("wasm: this resource reaches a vector database and this tier has nothing to open one with")

	// ErrWrongWidth is an embedding not matching the declared width: a caller's
	// mistake, not an outage.
	ErrWrongWidth = errors.New("wasm: that is not this database's vector width")
)

const vectorModel = specs.DatabaseTypeVector

// Match is one nearest-neighbour result, with its meta already JSON.
type Match struct {
	ID    string
	Score float32
	Meta  []byte
}

// Vectors is one vector database.
type Vectors interface {
	Search(ctx context.Context, query []float32, k int) ([]Match, error)
	Upsert(ctx context.Context, id string, embedding []float32, meta []byte) error
	Delete(ctx context.Context, id string) error
}

// VectorStores opens the vector database a grant names.
type VectorStores interface {
	OpenVectors(ctx context.Context, grant invoke.Grant) (Vectors, error)
}

// WithVectors is what backs `taubyte:vector/store`.
func WithVectors(v VectorStores) Option {
	return func(r *Runtime) { r.vectors = v }
}

func vectorGrantsOf(spec invoke.Spec) map[string]invoke.Grant {
	var out map[string]invoke.Grant
	for _, g := range spec.Reaches {
		if g.Kind != databaseKind || g.Settings[specs.DatabaseKeyType] != vectorModel {
			continue
		}
		if out == nil {
			out = map[string]invoke.Grant{}
		}
		out[g.Label] = g
	}
	return out
}

// vectorImports registers the three functions for one instantiation.
func (r *Runtime) vectorImports(spec invoke.Spec) []component.Option {
	grants := vectorGrantsOf(spec)

	open := func(ctx context.Context, label, op string) (Vectors, *component.VariantValue) {
		grant, why := r.addressed(ctx, spec, grants, label, op)
		if why != nil {
			if why.missing {
				return nil, vectorFailure(errNoSuchVector, why.detail)
			}
			if why.unavailable {
				return nil, vectorFailure(errVectorUnavailable, why.detail)
			}
			return nil, vectorFailure(errVectorDenied, why.detail)
		}
		if r.vectors == nil {
			return nil, vectorFailure(errVectorUnavailable, "this node has nothing to open a database with")
		}

		db, err := r.vectors.OpenVectors(ctx, grant)
		if err != nil {
			return nil, vectorFailure(errVectorUnavailable, err.Error())
		}
		return db, nil
	}

	errorType := func(t *component.TypeTable) component.TypeRef {
		return t.Variant(
			component.VariantCaseSpec{Name: "no-such-database", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "denied", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "wrong-width", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "malformed", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "unavailable", Type: component.Prim("string")},
		)
	}

	searchTbl := component.NewTypeTable()
	searchFD := searchTbl.Func(
		[]component.TypeRef{
			component.Prim("string"),
			searchTbl.List(component.Prim("f32")),
			component.Prim("s32"),
		},
		searchTbl.Result(
			searchTbl.List(searchTbl.Record(
				"id", component.Prim("string"),
				"score", component.Prim("f32"),
				"meta", component.Prim("string"),
			)),
			errorType(searchTbl),
		),
	)
	search := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("vector.search: expected 3 arguments, got %d", len(args))
		}
		label, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("vector.search: first argument is %T, want string", args[0])
		}
		query, err := readFloats(args[1])
		if err != nil {
			return nil, fmt.Errorf("vector.search: %w", err)
		}
		k, ok := args[2].(int32)
		if !ok {
			return nil, fmt.Errorf("vector.search: k is %T, want s32", args[2])
		}

		db, refused := open(ctx, label, opVectorSearch)
		if refused != nil {
			return errorResult(refused), nil
		}

		found, err := db.Search(ctx, query, int(k))
		if err != nil {
			return errorResult(vectorArm(err)), nil
		}

		out := make([]component.Value, 0, len(found))
		for _, m := range found {
			out = append(out, []component.Value{m.ID, m.Score, string(m.Meta)})
		}
		return []component.Value{component.ResultValue{Payload: out}}, nil
	}

	upsertTbl := component.NewTypeTable()
	upsertFD := upsertTbl.Func(
		[]component.TypeRef{
			component.Prim("string"), component.Prim("string"),
			upsertTbl.List(component.Prim("f32")), component.Prim("string"),
		},
		upsertTbl.Result(component.TypeRef{}, errorType(upsertTbl)),
	)
	upsert := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 4 {
			return nil, fmt.Errorf("vector.upsert: expected 4 arguments, got %d", len(args))
		}
		label, id, err := twoStrings(args)
		if err != nil {
			return nil, fmt.Errorf("vector.upsert: %w", err)
		}
		embedding, err := readFloats(args[2])
		if err != nil {
			return nil, fmt.Errorf("vector.upsert: %w", err)
		}
		meta, ok := args[3].(string)
		if !ok {
			return nil, fmt.Errorf("vector.upsert: meta is %T, want string", args[3])
		}

		db, refused := open(ctx, label, opVectorUpsert)
		if refused != nil {
			return errorResult(refused), nil
		}
		if err := db.Upsert(ctx, id, embedding, []byte(meta)); err != nil {
			return errorResult(vectorArm(err)), nil
		}
		return []component.Value{component.ResultValue{}}, nil
	}

	deleteTbl := component.NewTypeTable()
	deleteFD := deleteTbl.Func(
		[]component.TypeRef{component.Prim("string"), component.Prim("string")},
		deleteTbl.Result(component.TypeRef{}, errorType(deleteTbl)),
	)
	remove := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		label, id, err := twoStrings(args)
		if err != nil {
			return nil, err
		}
		db, refused := open(ctx, label, opVectorDelete)
		if refused != nil {
			return errorResult(refused), nil
		}
		if err := db.Delete(ctx, id); err != nil {
			return errorResult(vectorArm(err)), nil
		}
		return []component.Value{component.ResultValue{}}, nil
	}

	return []component.Option{
		component.WithImportCustom(vectorInterface, opVectorSearch, search, searchFD, searchTbl.Resolver()),
		component.WithImportCustom(vectorInterface, opVectorUpsert, upsert, upsertFD, upsertTbl.Resolver()),
		component.WithImportCustom(vectorInterface, opVectorDelete, remove, deleteFD, deleteTbl.Resolver()),
	}
}

// readFloats lifts a list<f32>, specialised by wazy like list<u8>; the general shape
// is accepted too.
func readFloats(v component.Value) ([]float32, error) {
	switch value := v.(type) {
	case []float32:
		return value, nil
	case []component.Value:
		out := make([]float32, 0, len(value))
		for i, item := range value {
			f, ok := item.(float32)
			if !ok {
				return nil, fmt.Errorf("element %d is %T, want f32", i, item)
			}
			out = append(out, f)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected a list of f32, got %T", v)
	}
}

func vectorArm(err error) *component.VariantValue {
	switch {
	case errors.Is(err, ErrWrongWidth):
		return vectorFailure(errWrongWidth, err.Error())
	case errors.Is(err, ErrMalformed):
		return vectorFailure(errVectorMalformed, err.Error())
	default:
		return vectorFailure(errVectorUnavailable, err.Error())
	}
}

func vectorFailure(disc uint32, detail string) *component.VariantValue {
	return &component.VariantValue{Disc: disc, Payload: detail}
}
