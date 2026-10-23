package engine

import (
	"testing"

	"github.com/spf13/afero"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

func tableSchema() Schema {
	return SchemaDefinition(Root(Attributes(String("id")),
		DefineGroup("things", DefineIter([]*Attribute{String("id", Required())})),
		DefineGroup("table", DefineIter([]*Attribute{StringSlice("branches", Required())},
			Nested(DefineGroup("inner", DefineIter([]*Attribute{StringSlice("branches")}))))).With(RequiredGroup()),
	).With(Leaf("config"), Identity("id", "name")))
}

func TestARequiredGroupRefusesAbsentAndEmpty(t *testing.T) {
	for doc, want := range map[string]string{
		"id: p\n":            "required group 'table'",
		"id: p\ntable: {}\n": "required group 'table' is empty",
	} {
		_, err := processFiles(t, tableSchema(), nil, nil, map[string]string{"/config.yaml": doc})
		assert.ErrorContains(t, err, want, doc)
	}

	flat, err := processFiles(t, tableSchema(), nil, nil, map[string]string{
		"/config.yaml": "id: p\ntable:\n    a:\n        branches: [main]\n",
	})
	assert.NilError(t, err)
	assert.Assert(t, flat["table"] != nil)
}

func TestATableIsDumpedIntoTheRootDocument(t *testing.T) {
	src := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(src, "/config.yaml", []byte(
		"id: p\ntable:\n    a:\n        branches: [main]\n        inner:\n            b:\n                branches: [x]\n"), 0o644))
	assert.NilError(t, afero.WriteFile(src, "/things/one.yaml", []byte("id: one\n"), 0o644))

	e, err := New(tableSchema(), yaseer.VirtualFS(src, "/"))
	assert.NilError(t, err)
	obj, err := e.Process()
	assert.NilError(t, err)

	out := afero.NewMemMapFs()
	d, err := New(tableSchema(), yaseer.VirtualFS(out, "/"))
	assert.NilError(t, err)
	assert.NilError(t, d.Dump(obj))

	exists, err := afero.Exists(out, "/table")
	assert.NilError(t, err)
	assert.Assert(t, !exists, "a table is not a directory of documents")
	exists, err = afero.Exists(out, "/things/one.yaml")
	assert.NilError(t, err)
	assert.Assert(t, exists, "a resource keeps its own document")

	back, err := New(tableSchema(), yaseer.VirtualFS(out, "/"))
	assert.NilError(t, err)
	again, err := back.Process()
	assert.NilError(t, err)
	assert.DeepEqual(t, obj.Flat(), again.Flat())
}
