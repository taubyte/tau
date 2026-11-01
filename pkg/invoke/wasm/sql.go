package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/specs"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// sql.go is the host half of `taubyte:sql/store`, cache.go's shape with typed
// values: a SQL argument crosses as itself, since binding a number as text defeats
// an index. No transaction: none is declared, so BEGIN and COMMIT are statements
// on whatever connection a call gets.

const sqlInterface = "taubyte:sql/store@0.1.0"

const (
	opQuery = "query"
	opExec  = "exec"
)

// The error variant's cases, in the order handler.wit declares them.
const (
	errNoSuchSQL uint32 = iota
	errSQLDenied
	errSQLUnavailable
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
	// ErrNoSQL is a spec that grants a database on a tier composed
	// without a backing. Like ErrNoCaches it is a construction fault.
	ErrNoSQL = errors.New("wasm: this resource reaches a database and this tier has nothing to open one with")

	// ErrStatementFailed is the server refusing (syntax, constraint, permission), its
	// own arm since a caller fixes it rather than retries.
	ErrStatementFailed = errors.New("wasm: the database refused the statement")
)

// Rows is one result set, flat. Values is row-major and Columns gives the
// width — see the interface doc for why it is not a list of lists.
type Rows struct {
	Columns []string
	Values  []any
}

// SQL is what a guest queries through, handed the whole grant (engine, address,
// credential).
type SQL interface {
	Query(ctx context.Context, grant invoke.Grant, statement string, args []any) (Rows, error)
	Exec(ctx context.Context, grant invoke.Grant, statement string, args []any) (Outcome, error)
}

// Outcome is what a write changed, 0 when the driver did not say; see the WIT on
// why RETURNING is portable and last-insert-id is not.
type Outcome struct {
	Affected     int64
	LastInsertID int64
}

// WithSQL is what backs `taubyte:sql/store`.
func WithSQL(d SQL) Option {
	return func(r *Runtime) { r.sqlStore = d }
}

const (
	databaseKind = specs.KindDatabase
	sqlModel     = specs.DatabaseTypeSql
)

// sqlGrantsOf is the database grants whose data model is SQL: filtered by type too,
// since each data model has its own interface.
func sqlGrantsOf(spec invoke.Spec) map[string]invoke.Grant {
	var out map[string]invoke.Grant
	for _, g := range spec.Reaches {
		if g.Kind != databaseKind || g.Settings[specs.DatabaseKeyType] != sqlModel {
			continue
		}
		if out == nil {
			out = map[string]invoke.Grant{}
		}
		out[g.Label] = g
	}
	return out
}

// sqlImports registers query and exec for one instantiation.
func (r *Runtime) sqlImports(spec invoke.Spec) []component.Option {
	grants := sqlGrantsOf(spec)

	// resolve is the shared half: which database, and may this verb touch it.
	resolve := func(ctx context.Context, label, op string) (invoke.Grant, *component.VariantValue) {
		grant, why := r.addressed(ctx, spec, grants, label, op)
		if why != nil {
			if why.missing {
				return grant, sqlFailure(errNoSuchSQL, why.detail)
			}
			if why.unavailable {
				return grant, sqlFailure(errSQLUnavailable, why.detail)
			}
			return grant, sqlFailure(errSQLDenied, why.detail)
		}
		if r.sqlStore == nil {
			return grant, sqlFailure(errSQLUnavailable, "this node has nothing to open a database with")
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

		rows, err := r.sqlStore.Query(ctx, grant, statement, bound)
		if err != nil {
			return errorResult(sqlArm(err)), nil
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
		execTbl.Result(
			execTbl.Record(
				"affected", component.Prim("s64"),
				"last-insert-id", component.Prim("s64"),
			),
			errorType(execTbl),
		),
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

		outcome, err := r.sqlStore.Exec(ctx, grant, statement, bound)
		if err != nil {
			return errorResult(sqlArm(err)), nil
		}
		// A record lowers as its fields in declaration order.
		return []component.Value{component.ResultValue{
			Payload: []component.Value{outcome.Affected, outcome.LastInsertID},
		}}, nil
	}

	return []component.Option{
		component.WithImportCustom(sqlInterface, opQuery, query, queryFD, queryTbl.Resolver()),
		component.WithImportCustom(sqlInterface, opExec, exec, execFD, execTbl.Resolver()),
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

// liftSQL turns one guest value into what a driver binds; NULL is nil, never "".
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

// lowerSQL turns a scanned value into a guest value; a type the interface cannot
// name (time, decimal, UUID) becomes its text, never a guessed number.
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

// sqlArm maps a Go error onto the guest's arm; a refused statement is not an outage.
func sqlArm(err error) *component.VariantValue {
	if errors.Is(err, ErrStatementFailed) {
		return sqlFailure(errStatementFailed, err.Error())
	}
	return sqlFailure(errSQLUnavailable, err.Error())
}

func sqlFailure(disc uint32, detail string) *component.VariantValue {
	return &component.VariantValue{Disc: disc, Payload: detail}
}
