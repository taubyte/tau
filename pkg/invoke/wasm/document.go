package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// document.go is the host half of `taubyte:document/store` — the `database`
// kind at `type: document`.
//
// # The bytes are the JSON
//
// A document is an arbitrary nested object and the canonical ABI has no type
// for one. Declaring a variant deep enough would be declaring JSON in WIT, and
// every guest would translate to and from its own JSON anyway — so the document
// crosses as bytes and a guest uses the parser it already has. The host does
// not read them beyond checking they decode, which is what keeps this interface
// stable while the shape of a document stays the author's business.

const documentInterface = "taubyte:document/store@0.1.0"

const (
	opDocGet    = "get"
	opDocPut    = "put"
	opDocDelete = "delete"
	opDocQuery  = "query"
)

// findExport is what the guest calls. The VERB is `query` — which is what the
// configuration writes and what the op check tests — and the function is
// `find`, because `query` reads as SQL's and this is not that.
const findExport = "find"

// The error variant's cases, in the order handler.wit declares them.
const (
	errNoSuchDoc uint32 = iota
	errDocDenied
	errDocNotFound
	errDocMalformed
	errDocUnavailable
)

var (
	// ErrNoDocuments is a spec that grants a document database on a tier
	// composed without a backing.
	ErrNoDocuments = errors.New("wasm: this resource reaches a document database and this tier has nothing to open one with")

	// ErrMalformed is a document that is not a JSON object, in either
	// direction. It is its OWN arm because a caller fixes it and does not retry
	// it — the same distinction the sql interface draws between a refused
	// statement and an outage.
	ErrMalformed = errors.New("wasm: that is not a json object")
)

const documentModel = "document"

// Documents is one document database.
type Documents interface {
	Get(ctx context.Context, id string) ([]byte, error)
	Put(ctx context.Context, id string, doc []byte) error
	Delete(ctx context.Context, id string) error
	Find(ctx context.Context, filter []byte) ([]byte, error)
}

// DocumentStores opens the one a grant names. See KVs for why the method is
// named rather than being a bare Open.
type DocumentStores interface {
	OpenDocuments(ctx context.Context, grant invoke.Grant) (Documents, error)
}

// WithDocuments is what backs `taubyte:document/store`.
func WithDocuments(d DocumentStores) Option {
	return func(r *Runtime) { r.documents = d }
}

func documentGrantsOf(spec invoke.Spec) map[string]invoke.Grant {
	var out map[string]invoke.Grant
	for _, g := range spec.Reaches {
		if g.Kind != databaseKind || g.Settings["type"] != documentModel {
			continue
		}
		if out == nil {
			out = map[string]invoke.Grant{}
		}
		out[g.Label] = g
	}
	return out
}

// documentImports registers the four functions for one instantiation.
func (r *Runtime) documentImports(spec invoke.Spec) []component.Option {
	grants := documentGrantsOf(spec)

	open := func(ctx context.Context, label, op string) (Documents, *component.VariantValue) {
		grant, why := r.addressed(ctx, grants, label, op)
		if why != nil {
			if why.missing {
				return nil, docFailure(errNoSuchDoc, why.detail)
			}
			if why.unavailable {
				return nil, docFailure(errDocUnavailable, why.detail)
			}
			return nil, docFailure(errDocDenied, why.detail)
		}
		if r.documents == nil {
			return nil, docFailure(errDocUnavailable, "this node has nothing to open a database with")
		}

		db, err := r.documents.OpenDocuments(ctx, grant)
		if err != nil {
			return nil, docFailure(errDocUnavailable, err.Error())
		}
		return db, nil
	}

	errorType := func(t *component.TypeTable) component.TypeRef {
		return t.Variant(
			component.VariantCaseSpec{Name: "no-such-database", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "denied", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "not-found"},
			component.VariantCaseSpec{Name: "malformed", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "unavailable", Type: component.Prim("string")},
		)
	}

	// bytesIn is the signature every call here has except put: two strings in,
	// bytes or an error out.
	reader := func(op string, run func(Documents, context.Context, string) ([]byte, error)) (component.HostFunc, component.FuncDesc, component.Resolver) {
		table := component.NewTypeTable()
		descriptor := table.Func(
			[]component.TypeRef{component.Prim("string"), component.Prim("string")},
			table.Result(table.List(component.Prim("u8")), errorType(table)),
		)
		fn := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
			label, argument, err := twoStrings(args)
			if err != nil {
				return nil, err
			}
			db, refused := open(ctx, label, op)
			if refused != nil {
				return errorResult(refused), nil
			}

			out, err := run(db, ctx, argument)
			if err != nil {
				return errorResult(docArm(err)), nil
			}
			return []component.Value{component.ResultValue{Payload: byteList(out)}}, nil
		}
		return fn, descriptor, table.Resolver()
	}

	get, getFD, getResolve := reader(opDocGet, func(db Documents, ctx context.Context, id string) ([]byte, error) {
		return db.Get(ctx, id)
	})

	// FIND TAKES ITS FILTER AS A STRING, not a byte list, because the guest
	// already has it as JSON text and the extra lowering would be a copy for
	// nothing. The ANSWER is bytes, because a result set is not text a guest
	// wants to build a string out of.
	find, findFD, findResolve := reader(opDocQuery, func(db Documents, ctx context.Context, filter string) ([]byte, error) {
		return db.Find(ctx, []byte(filter))
	})

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
		db, refused := open(ctx, label, opDocDelete)
		if refused != nil {
			return errorResult(refused), nil
		}
		if err := db.Delete(ctx, id); err != nil {
			return errorResult(docArm(err)), nil
		}
		return []component.Value{component.ResultValue{}}, nil
	}

	putTbl := component.NewTypeTable()
	putFD := putTbl.Func(
		[]component.TypeRef{component.Prim("string"), component.Prim("string"), putTbl.List(component.Prim("u8"))},
		putTbl.Result(component.TypeRef{}, errorType(putTbl)),
	)
	put := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("document.put: expected 3 arguments, got %d", len(args))
		}
		label, id, err := twoStrings(args)
		if err != nil {
			return nil, err
		}
		doc, err := readBytes(args[2])
		if err != nil {
			return nil, fmt.Errorf("document.put: %w", err)
		}

		db, refused := open(ctx, label, opDocPut)
		if refused != nil {
			return errorResult(refused), nil
		}
		if err := db.Put(ctx, id, doc); err != nil {
			return errorResult(docArm(err)), nil
		}
		return []component.Value{component.ResultValue{}}, nil
	}

	return []component.Option{
		component.WithImportCustom(documentInterface, opDocGet, get, getFD, getResolve),
		component.WithImportCustom(documentInterface, opDocPut, put, putFD, putTbl.Resolver()),
		component.WithImportCustom(documentInterface, opDocDelete, remove, deleteFD, deleteTbl.Resolver()),
		component.WithImportCustom(documentInterface, findExport, find, findFD, findResolve),
	}
}

// docArm maps a Go error onto the arm the guest sees. A MISS and A MALFORMED
// DOCUMENT are each their own, because a caller acts differently on all three.
func docArm(err error) *component.VariantValue {
	switch {
	case errors.Is(err, ErrNotFound):
		return docFailure(errDocNotFound, "")
	case errors.Is(err, ErrMalformed):
		return docFailure(errDocMalformed, err.Error())
	default:
		return docFailure(errDocUnavailable, err.Error())
	}
}

func docFailure(disc uint32, detail string) *component.VariantValue {
	v := component.VariantValue{Disc: disc}
	if disc != errDocNotFound {
		v.Payload = detail
	}
	return &v
}
