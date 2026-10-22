package decompile

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

// The fixture is a compiled object built by hand — no schema, no compile — because what
// the two depths do is read off the record, and a test that had to compile first would be
// testing the vocabulary instead.
//
//	object/
//	  function/QmFnCheckout   authored; its queue block was handed two ops and wrote one
//	  function/QmFnAudit      authored; its whole cache block was handed to it
//	  queue/QmQReserve        produced
//	  queue/QmQManual         authored
//	  workflow/QmWf           authored, the source of all of it
//	  namespace/QmNsEu/       a scope, so the walk has to go deeper than the project
//	lowered/                  the record
//	indexes/                  a sibling that is not config either
const (
	fnCheckout = "QmFnCheckout"
	fnAudit    = "QmFnAudit"
	fnEu       = "QmFnEu"
	qReserve   = "QmQReserve"
	qManual    = "QmQManual"
	qEu        = "QmQEu"
	workflow   = "QmWf"
	nsEu       = "QmNsEu"
)

func compiled(t *testing.T) Object {
	t.Helper()

	project := object.New[object.Refrence]()

	functions := group(t, project, "function")
	checkout := instance(t, functions, fnCheckout, "checkout")
	queueBlock := group(t, checkout, "queue")
	entry(t, queueBlock, "checkout_reserve", "receive", "send")
	entry(t, queueBlock, "checkout_charge", "send")

	audit := instance(t, functions, fnAudit, "audit")
	entry(t, group(t, audit, "cache"), "orders", "get")

	queues := group(t, project, "queue")
	instance(t, queues, qReserve, "checkout_reserve")
	instance(t, queues, qManual, "manual")

	instance(t, group(t, project, "workflow"), workflow, "checkout")

	scope := instance(t, group(t, project, "namespace"), nsEu, "local")
	instance(t, group(t, scope, "queue"), qEu, "local_reserve")
	worker := instance(t, group(t, scope, "function"), fnEu, "eu_worker")
	entry(t, group(t, worker, "queue"), "local_reserve", "receive")

	root := object.New[object.Refrence]()
	assert.NilError(t, root.Child("object").Add(project))
	assert.NilError(t, root.Child(interp.LoweredKey).Add(record(t)))
	assert.NilError(t, root.Child("indexes").Add(object.New[object.Refrence]()))
	return root
}

// record is what a compile would have written beside that object: two queues nobody wrote
// and the four ops its lowering handed out.
func record(t *testing.T) Object {
	t.Helper()

	resources := object.New[object.Refrence]()
	for id, p := range map[string]map[string]any{
		qReserve: {"from": workflow, "at": "steps/0", "kind": "queue", "name": "checkout_reserve"},
		qEu:      {"from": workflow, "kind": "queue", "name": "local_reserve", "scope": "local"},
	} {
		one := object.New[object.Refrence]()
		for k, v := range p {
			one.Set(k, v)
		}
		assert.NilError(t, resources.Child(id).Add(one))
	}

	rec := object.New[object.Refrence]()
	assert.NilError(t, rec.Child("resources").Add(resources))
	// A path and a value, and nothing about what either means: the same record works for
	// a vocabulary that never heard of an access entry.
	rec.Set("entries", []any{
		written(fnCheckout, "receive", "queue", "checkout_reserve", "ops"),
		written(fnCheckout, "send", "queue", "checkout_charge", "ops"),
		written(fnAudit, "get", "cache", "orders", "ops"),
		written(fnEu, "receive", "queue", "local_reserve", "ops"),
	})
	return rec
}

func written(on, value string, path ...string) map[string]any {
	segs := make([]any, 0, len(path))
	for _, p := range path {
		segs = append(segs, p)
	}
	return map[string]any{"on": on, "path": segs, "value": value, "from": workflow}
}

func group(t *testing.T, parent Object, key string) Object {
	t.Helper()
	g := object.New[object.Refrence]()
	assert.NilError(t, parent.Child(key).Add(g))
	return g
}

func instance(t *testing.T, group Object, id, name string) Object {
	t.Helper()
	o := object.New[object.Refrence]()
	o.Set("name", name)
	assert.NilError(t, group.Child(id).Add(o))
	return o
}

func entry(t *testing.T, block Object, name string, ops ...string) {
	t.Helper()
	e := object.New[object.Refrence]()
	e.Set("ops", ops)
	assert.NilError(t, block.Child(name).Add(e))
}

func at(t *testing.T, o Object, path ...string) Object {
	t.Helper()
	got, err := o.Fetch(path...)
	assert.NilError(t, err)
	return got
}

func gone(t *testing.T, o Object, path ...string) {
	t.Helper()
	_, err := o.Fetch(path...)
	assert.Assert(t, err != nil, "expected %v to be gone", path)
}

func run(t *testing.T, obj Object, keep bool) Object {
	t.Helper()
	ctx := transform.NewContext[object.Refrence](context.Background())
	out, err := transform.Pipe(ctx, obj, unwrapEnvelope(), &authoredOnly{lowered: loweredOf(obj), keep: keep})
	assert.NilError(t, err)
	return out
}

// Half way: what somebody wrote, and only that.
func TestAuthoredOnly_DropsWhatNobodyWrote(t *testing.T) {
	out := run(t, compiled(t), false)

	// The envelope and both siblings are behind us: what is left is the project.
	gone(t, out, "object")
	gone(t, out, interp.LoweredKey)
	gone(t, out, "indexes")

	// The produced queues go; the one somebody wrote stays, and so does the workflow the
	// lowering read — a lowering ADDS, it never replaces.
	gone(t, out, "queue", qReserve)
	at(t, out, "queue", qManual)
	at(t, out, "workflow", workflow)

	// An op that was handed over comes back off; one the author wrote beside it stays,
	// so the entry survives.
	reserve := at(t, out, "function", fnCheckout, "queue", "checkout_reserve")
	assert.DeepEqual(t, values(reserve, "ops"), []string{"send"})

	// An entry whose every op was handed over goes, and the block it was in stays because
	// another entry is still in it.
	gone(t, out, "function", fnCheckout, "queue", "checkout_charge")
	at(t, out, "function", fnCheckout, "queue")

	// The block goes too when its last entry does.
	gone(t, out, "function", fnAudit, "cache")
	at(t, out, "function", fnAudit)

	// A scope is walked like the project: both a produced resource in it and an entry on
	// a resource in it are found.
	gone(t, out, "namespace", nsEu, "queue", qEu)
	gone(t, out, "namespace", nsEu, "function", fnEu, "queue")
}

// All the way: the same object the compiler produced, minus the record, which is not
// config in either depth.
func TestAuthoredOnly_WithAsLoweredKeepsEverything(t *testing.T) {
	out := run(t, compiled(t), true)

	gone(t, out, interp.LoweredKey)

	at(t, out, "queue", qReserve)
	at(t, out, "queue", qManual)
	at(t, out, "namespace", nsEu, "queue", qEu)

	reserve := at(t, out, "function", fnCheckout, "queue", "checkout_reserve")
	assert.DeepEqual(t, values(reserve, "ops"), []string{"receive", "send"})
	at(t, out, "function", fnCheckout, "queue", "checkout_charge")
	at(t, out, "function", fnAudit, "cache", "orders")
	at(t, out, "namespace", nsEu, "function", fnEu, "queue", "local_reserve")
}

// An object that carries a record without an envelope — nothing chrooted it — still must
// not have the record written out as though it were a group.
func TestAuthoredOnly_DropsTheRecordWithoutAnEnvelope(t *testing.T) {
	for _, keep := range []bool{false, true} {
		root := object.New[object.Refrence]()
		instance(t, group(t, root, "queue"), qManual, "manual")
		assert.NilError(t, root.Child(interp.LoweredKey).Add(record(t)))

		ctx := transform.NewContext[object.Refrence](context.Background())
		out, err := (&authoredOnly{lowered: loweredOf(root), keep: keep}).Process(ctx, root)
		assert.NilError(t, err)

		gone(t, out, interp.LoweredKey)
		at(t, out, "queue", qManual)
	}
}

func TestLoweredOf(t *testing.T) {
	l := loweredOf(compiled(t))

	assert.Equal(t, len(l.Resources), 2)
	assert.Assert(t, l.Produced(qReserve))
	assert.Assert(t, l.Produced(qEu))
	assert.Assert(t, !l.Produced(qManual))
	assert.Equal(t, l.Resources[qReserve].From, workflow)
	assert.Equal(t, l.Resources[qReserve].At, "steps/0")
	assert.Equal(t, l.Resources[qEu].Scope, "local")
	assert.Equal(t, len(l.Entries), 4)

	// An object with no record answers "nothing was lowered" rather than failing.
	assert.Equal(t, len(loweredOf(object.New[object.Refrence]()).Resources), 0)
}

// The chroot-unwrap is gated on the schema declaring a sibling to make room for, and
// lowering is one — a v2 vocabulary that indexes nothing is still chrooted.
func TestPipe_UnwrapGate(t *testing.T) {
	id := []*engine.Attribute{engine.String("id", engine.Required())}

	plain := engine.Root(id, engine.DefineGroup("queue", engine.DefineIter(id,
		engine.Resource("queue", "Queue", "Queue", "queue"))))
	lowers := engine.Root(id, engine.DefineGroup("workflow", engine.DefineIter(id,
		engine.Resource("workflow", "Workflow", "Workflow", "workflow"),
		interp.Lowers(interp.Into("queue",
			interp.Named(interp.Concat(interp.Attr("name"), interp.Text("_work"))))))))

	assert.Assert(t, !interp.UsesIndexing(lowers))
	assert.Equal(t, len((&Decompiler{compileRoot: plain}).pipe(interp.Lowered{})), 2)
	assert.Equal(t, len((&Decompiler{compileRoot: lowers}).pipe(interp.Lowered{})), 3)
}
