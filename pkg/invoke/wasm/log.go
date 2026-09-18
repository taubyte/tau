package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// log.go is the host half of `taubyte:log/records` — the `sync` kind at
// `type: log`.
//
// THE KIND IS `sync` AND THE TYPE SELECTS THIS INTERFACE, the same way
// `database` at `type: sql` selects sql.go's. A grant is filtered by kind AND
// type here for that reason: `sync` also covers a queue, whose verbs are not
// these and whose backing is a different thing.
//
// # What a log promises that a queue does not
//
// Reading consumes nothing. Every reader keeps its own cursor, so two workloads
// reading one log both see every record — which is why `read` is a plain call
// and there is no claim, no acknowledgement and no visibility timeout here.

const logInterface = "taubyte:log/records@0.1.0"

// The vocabulary's verbs for the log type.
const (
	opAppend = "append"
	opRead   = "read"
)

// logType is the `sync` type this interface serves.
const logType = "log"

// The error variant's cases, in the order the WIT declares them.
const (
	errNoSuchLog uint32 = iota
	errLogDenied
	errLogNotWritable
	errLogTooLarge
	errBadCursor
	errLogUnavailable
)

var (
	// ErrNoLogs is a spec that reaches a log on a tier composed without a
	// backing. It is a construction fault rather than a guest's, so it is
	// refused at Start rather than at the first call.
	ErrNoLogs = errors.New("wasm: this resource reaches a log and this tier has nothing to append to")

	// ErrRecordTooLarge is one record bigger than the log will take.
	ErrRecordTooLarge = errors.New("wasm: that record is larger than this log accepts")

	// ErrBadCursor is a position this log never issued, or one whose records
	// retention has already dropped. It is its own arm because a caller fixes
	// it — by starting from the beginning — rather than retrying it.
	ErrBadCursor = errors.New("wasm: that is not a position this log can read from")
)

// Record is one entry as it was appended.
type Record struct {
	// Cursor is where the record sits. A caller hands it back to read what
	// follows; nothing about its contents is promised.
	Cursor string

	// At is unix SECONDS, off the LOG's clock rather than the appender's: what
	// a log orders is when it took the record.
	At int64

	Data []byte
}

// Logs is what a guest appends to and reads.
//
// IT IS A SEAM BECAUSE ORDER IS A DISTRIBUTED PROBLEM and this package's job is
// the ABI. Who decides the order, where the bytes live and what retention drops
// are the node's — see node/log.go, which serialises appends through the raft
// leader and keeps the records in the replicated store.
type Logs interface {
	// Append adds one record and answers the cursor it landed at.
	Append(ctx context.Context, grant invoke.Grant, data []byte) (string, error)

	// Read answers the records after a cursor, oldest first. An empty cursor
	// starts at the oldest record still held. Fewer than limit means caught up.
	Read(ctx context.Context, grant invoke.Grant, after string, limit uint32) ([]Record, error)
}

// WithLogs is what backs `taubyte:log/records`.
func WithLogs(l Logs) Option {
	return func(r *Runtime) { r.logs = l }
}

// logGrantsOf is the log grants this tier would have to serve — refused ones do
// not count, for the reason servableStorage states.
func logGrantsOf(spec invoke.Spec) map[string]invoke.Grant {
	var out map[string]invoke.Grant
	for _, g := range spec.Reaches {
		if g.Kind != syncKind || g.Settings[settingSyncType] != logType || g.Refused != "" {
			continue
		}
		if out == nil {
			out = map[string]invoke.Grant{}
		}
		out[g.Label] = g
	}
	return out
}

// logImports registers append and read for one instantiation.
func (r *Runtime) logImports(spec invoke.Spec) []component.Option {
	grants := logGrantsOf(spec)

	open := func(ctx context.Context, label, op string) (invoke.Grant, *component.VariantValue) {
		grant, why := r.addressed(ctx, spec, grants, label, op)
		if why != nil {
			if why.missing {
				return grant, logFailure(errNoSuchLog, why.detail)
			}
			if why.unavailable {
				return grant, logFailure(errLogUnavailable, why.detail)
			}
			return grant, logFailure(errLogDenied, why.detail)
		}
		if r.logs == nil {
			return grant, logFailure(errLogUnavailable, "this node has nothing to append to")
		}
		return grant, nil
	}

	errorType := func(t *component.TypeTable) component.TypeRef {
		return t.Variant(
			component.VariantCaseSpec{Name: "no-such-log", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "denied", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "not-writable", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "too-large", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "bad-cursor", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "unavailable", Type: component.Prim("string")},
		)
	}

	appendTbl := component.NewTypeTable()
	appendFD := appendTbl.Func(
		[]component.TypeRef{component.Prim("string"), appendTbl.List(component.Prim("u8"))},
		appendTbl.Result(component.Prim("string"), errorType(appendTbl)),
	)
	appendRecord := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("log.append: expected 2 arguments, got %d", len(args))
		}
		label, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("log.append: label is %T, want a string", args[0])
		}
		data, err := readBytes(args[1])
		if err != nil {
			return nil, fmt.Errorf("log.append: %w", err)
		}

		grant, refused := open(ctx, label, opAppend)
		if refused != nil {
			return errorResult(refused), nil
		}

		cursor, err := r.logs.Append(ctx, grant, data)
		if err != nil {
			return errorResult(logArm(err)), nil
		}
		return []component.Value{component.ResultValue{Payload: cursor}}, nil
	}

	readTbl := component.NewTypeTable()
	entryType := readTbl.Record(
		"cursor", component.Prim("string"),
		"at", component.Prim("s64"),
		"data", readTbl.List(component.Prim("u8")),
	)
	readFD := readTbl.Func(
		[]component.TypeRef{component.Prim("string"), component.Prim("string"), component.Prim("u32")},
		readTbl.Result(readTbl.List(entryType), errorType(readTbl)),
	)
	readRecords := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("log.read: expected 3 arguments, got %d", len(args))
		}
		label, after, err := twoStrings(args)
		if err != nil {
			return nil, fmt.Errorf("log.read: %w", err)
		}
		limit, err := liftU32(args[2])
		if err != nil {
			return nil, fmt.Errorf("log.read: %w", err)
		}

		grant, refused := open(ctx, label, opRead)
		if refused != nil {
			return errorResult(refused), nil
		}

		records, err := r.logs.Read(ctx, grant, after, limit)
		if err != nil {
			return errorResult(logArm(err)), nil
		}

		out := make([]component.Value, 0, len(records))
		for _, rec := range records {
			// A record lowers as its fields in declaration order.
			out = append(out, []component.Value{rec.Cursor, rec.At, byteList(rec.Data)})
		}
		return []component.Value{component.ResultValue{Payload: out}}, nil
	}

	return []component.Option{
		component.WithImportCustom(logInterface, opAppend, appendRecord, appendFD, appendTbl.Resolver()),
		component.WithImportCustom(logInterface, opRead, readRecords, readFD, readTbl.Resolver()),
	}
}

// logArm maps a Go error onto the arm the guest sees.
func logArm(err error) *component.VariantValue {
	switch {
	case errors.Is(err, ErrNotWritable):
		return logFailure(errLogNotWritable, err.Error())
	case errors.Is(err, ErrRecordTooLarge):
		return logFailure(errLogTooLarge, err.Error())
	case errors.Is(err, ErrBadCursor):
		return logFailure(errBadCursor, err.Error())
	}
	return logFailure(errLogUnavailable, err.Error())
}

func logFailure(disc uint32, detail string) *component.VariantValue {
	return &component.VariantValue{Disc: disc, Payload: detail}
}
