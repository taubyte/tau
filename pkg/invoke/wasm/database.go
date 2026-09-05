package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// database.go is the host half of `taubyte:handler/database`, for the `sql`
// data model.
//
// It is cache.go's shape again — a label resolved against the grant, an op
// check before anything is touched, a refusal that says why — with one thing
// the other two do not have: VALUES THAT ARE NOT BYTES. A SQL argument is a
// string, an integer, a float, a boolean, a blob or NULL, and each has to cross
// the ABI as itself, because binding a number as text is how a query stops
// using an index.
//
// # What is not here
//
// THE OTHER THREE TYPES. `kv`, `document` and `vector` are the same kind with
// different verbs, and each is its own interface when it lands.
//
// A TRANSACTION. Nothing in the vocabulary declares one — `query` and `exec`
// are the whole surface — so a guest that needs one issues BEGIN and COMMIT as
// statements and gets whatever its connection gives it. Making that reliable
// means a connection pinned across calls, which is a resource in the WIT sense
// and a decision about what a "session" is; neither is guessed at here.

const databaseInterface = "taubyte:handler/database@0.1.0"

const (
	opQuery = "query"
	opExec  = "exec"
)

// The error variant's cases, in the order handler.wit declares them.
const (
	errNoSuchDatabase uint32 = iota
	errDatabaseDenied
	errDatabaseUnavailable
	errStatementFailed
)

// The value variant's cases, in the order handler.wit declares them.
const (
	valNull uint32 = iota
	valText
	valInteger
	valReal
	valBoolean
	valBlob
)

var (
	// ErrNoDatabases is a spec that grants a database on a tier composed
	// without a backing. Like ErrNoCaches it is a construction fault.
	ErrNoDatabases = errors.New("wasm: this resource reaches a database and this tier has nothing to open one with")

	// ErrStatementFailed is what a backing returns when the SERVER said no — a
	// syntax error, a constraint, a permission. The tier gives it its own arm
	// because a caller retries an outage and fixes a statement.
	ErrStatementFailed = errors.New("wasm: the database refused the statement")
)

// Rows is one result set, flat. Values is row-major and Columns gives the
// width — see the interface doc for why it is not a list of lists.
type Rows struct {
	Columns []string
	Values  []any
}

// Databases is what a guest queries through.
//
// THE WHOLE GRANT IS PASSED, as with a cache: what the backing needs beyond
// "which one" — the engine, the address, the credential to resolve — is on it,
// and a handle alone would make the backing resolve it back into a resource.
type Databases interface {
	Query(ctx context.Context, grant invoke.Grant, statement string, args []any) (Rows, error)
	Exec(ctx context.Context, grant invoke.Grant, statement string, args []any) (int64, error)
}

// WithDatabases is what backs `taubyte:handler/database`.
func WithDatabases(d Databases) Option {
	return func(r *Runtime) { r.databases = d }
}

const (
	databaseKind = "database"
	sqlModel     = "sql"
)

// databaseGrantsOf is the database grants whose data model is SQL.
//
// THE KIND IS `database` AND THE TYPE SELECTS THE INTERFACE. Four data models
// live under one kind with different verbs, so filtering by kind alone would
// make a kv database refuse to start on a tier that serves only sql — and the
// two are served by different interfaces.
func databaseGrantsOf(spec invoke.Spec) map[string]invoke.Grant {
	var out map[string]invoke.Grant
	for _, g := range spec.Reaches {
		if g.Kind != databaseKind || g.Settings["type"] != sqlModel {
			continue
		}
		if out == nil {
			out = map[string]invoke.Grant{}
		}
		out[g.Label] = g
	}
	return out
}

// databaseImports registers query and exec for one instantiation.
func (r *Runtime) databaseImports(spec invoke.Spec) []component.Option {
	grants := databaseGrantsOf(spec)

	// resolve is the shared half: which database, and may this verb touch it.
	resolve := func(ctx context.Context, label, op string) (invoke.Grant, *component.VariantValue) {
		grant, why := r.addressed(ctx, grants, label, op)
		if why != nil {
			if why.missing {
				return grant, databaseFailure(errNoSuchDatabase, why.detail)
			}
			if why.unavailable {
				return grant, databaseFailure(errDatabaseUnavailable, why.detail)
			}
			return grant, databaseFailure(errDatabaseDenied, why.detail)
		}
		if r.databases == nil {
			return grant, databaseFailure(errDatabaseUnavailable, "this node has nothing to open a database with")
		}
		return grant, nil
	}

	valueType := func(t *component.TypeTable) component.TypeRef {
		return t.Variant(
			component.VariantCaseSpec{Name: "null"},
			component.VariantCaseSpec{Name: "text", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "integer", Type: component.Prim("s64")},
			component.VariantCaseSpec{Name: "real", Type: component.Prim("f64")},
			component.VariantCaseSpec{Name: "boolean", Type: component.Prim("bool")},
			component.VariantCaseSpec{Name: "blob", Type: t.List(component.Prim("u8"))},
		)
	}

	errorType := func(t *component.TypeTable) component.TypeRef {
		return t.Variant(
			component.VariantCaseSpec{Name: "no-such-database", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "denied", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "unavailable", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "failed", Type: component.Prim("string")},
		)
	}

	queryTbl := component.NewTypeTable()
	queryValue := valueType(queryTbl)
	queryFD := queryTbl.Func(
		[]component.TypeRef{component.Prim("string"), component.Prim("string"), queryTbl.List(queryValue)},
		queryTbl.Result(
			queryTbl.Record(
				"columns", queryTbl.List(component.Prim("string")),
				"values", queryTbl.List(queryValue),
			),
			errorType(queryTbl),
		),
	)
	query := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		label, statement, bound, err := statementArgs(opQuery, args)
		if err != nil {
			return nil, err
		}

		grant, refused := resolve(ctx, label, opQuery)
		if refused != nil {
			return errorResult(refused), nil
		}

		rows, err := r.databases.Query(ctx, grant, statement, bound)
		if err != nil {
			return errorResult(databaseArm(err)), nil
		}

		columns := make([]component.Value, 0, len(rows.Columns))
		for _, name := range rows.Columns {
			columns = append(columns, name)
		}
		values := make([]component.Value, 0, len(rows.Values))
		for _, v := range rows.Values {
			values = append(values, lowerSQL(v))
		}
		return []component.Value{component.ResultValue{
			Payload: []component.Value{columns, values},
		}}, nil
	}

	execTbl := component.NewTypeTable()
	execFD := execTbl.Func(
		[]component.TypeRef{component.Prim("string"), component.Prim("string"), execTbl.List(valueType(execTbl))},
		execTbl.Result(component.Prim("s64"), errorType(execTbl)),
	)
	exec := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		label, statement, bound, err := statementArgs(opExec, args)
		if err != nil {
			return nil, err
		}

		grant, refused := resolve(ctx, label, opExec)
		if refused != nil {
			return errorResult(refused), nil
		}

		affected, err := r.databases.Exec(ctx, grant, statement, bound)
		if err != nil {
			return errorResult(databaseArm(err)), nil
		}
		return []component.Value{component.ResultValue{Payload: affected}}, nil
	}

	return []component.Option{
		component.WithImportCustom(databaseInterface, opQuery, query, queryFD, queryTbl.Resolver()),
		component.WithImportCustom(databaseInterface, opExec, exec, execFD, execTbl.Resolver()),
	}
}

// statementArgs lifts the three arguments both calls take.
func statementArgs(op string, args []component.Value) (label, statement string, bound []any, err error) {
	if len(args) != 3 {
		return "", "", nil, fmt.Errorf("database.%s: expected 3 arguments, got %d", op, len(args))
	}
	label, statement, err = twoStrings(args)
	if err != nil {
		return "", "", nil, fmt.Errorf("database.%s: %w", op, err)
	}

	list, ok := args[2].([]component.Value)
	if !ok {
		if args[2] == nil {
			return label, statement, nil, nil
		}
		return "", "", nil, fmt.Errorf("database.%s: arguments are %T, want a list", op, args[2])
	}

	bound = make([]any, 0, len(list))
	for i, item := range list {
		v, err := liftSQL(item)
		if err != nil {
			return "", "", nil, fmt.Errorf("database.%s: argument %d: %w", op, i, err)
		}
		bound = append(bound, v)
	}
	return label, statement, bound, nil
}

// liftSQL turns one guest value into what a driver binds.
//
// NULL IS A NIL AND NOT AN EMPTY STRING, which is the whole reason the variant
// has its own case: binding "" where the author meant NULL writes a row that
// does not match `IS NULL` and does not violate a NOT NULL constraint either.
func liftSQL(v component.Value) (any, error) {
	variant, ok := v.(component.VariantValue)
	if !ok {
		return nil, fmt.Errorf("expected a value variant, got %T", v)
	}

	switch variant.Disc {
	case valNull:
		return nil, nil
	case valText:
		return variant.Payload, nil
	case valInteger, valReal, valBoolean:
		return variant.Payload, nil
	case valBlob:
		return readBytes(variant.Payload)
	default:
		return nil, fmt.Errorf("value case %d is not one this interface declares", variant.Disc)
	}
}

// lowerSQL turns what a driver scanned into a guest value.
//
// A TYPE THIS INTERFACE CANNOT NAME BECOMES ITS TEXT rather than an error: a
// driver may hand back a time, a decimal or a UUID, and refusing the whole
// result set because one column is a timestamp would make most tables
// unreadable. What is NOT done is guessing a number out of it.
func lowerSQL(v any) component.Value {
	switch value := v.(type) {
	case nil:
		return component.VariantValue{Disc: valNull}
	case string:
		return component.VariantValue{Disc: valText, Payload: value}
	case []byte:
		return component.VariantValue{Disc: valBlob, Payload: value}
	case bool:
		return component.VariantValue{Disc: valBoolean, Payload: value}
	case int64:
		return component.VariantValue{Disc: valInteger, Payload: value}
	case int:
		return component.VariantValue{Disc: valInteger, Payload: int64(value)}
	case int32:
		return component.VariantValue{Disc: valInteger, Payload: int64(value)}
	case float64:
		return component.VariantValue{Disc: valReal, Payload: value}
	case float32:
		return component.VariantValue{Disc: valReal, Payload: float64(value)}
	default:
		return component.VariantValue{Disc: valText, Payload: fmt.Sprint(value)}
	}
}

// databaseArm maps a Go error onto the arm the guest sees.
//
// A STATEMENT THE SERVER REFUSED IS NOT AN OUTAGE. A caller retries one and
// fixes the other, and folding them together would have every syntax error
// retried until a timeout.
func databaseArm(err error) *component.VariantValue {
	if errors.Is(err, ErrStatementFailed) {
		return databaseFailure(errStatementFailed, err.Error())
	}
	return databaseFailure(errDatabaseUnavailable, err.Error())
}

func databaseFailure(disc uint32, detail string) *component.VariantValue {
	return &component.VariantValue{Disc: disc, Payload: detail}
}
