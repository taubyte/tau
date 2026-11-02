package engine

import (
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"

	"github.com/spf13/afero"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

func processFiles(t *testing.T, schema Schema, values map[string][]string, b Bound, files map[string]string) (map[string]any, error) {
	t.Helper()
	fs := afero.NewMemMapFs()
	for p, body := range files {
		assert.NilError(t, afero.WriteFile(fs, p, []byte(body), 0o644))
	}
	e, err := NewWith(schema, values, yaseer.VirtualFS(fs, "/"))
	assert.NilError(t, err)
	obj, err := e.ProcessFor(b)
	if err != nil {
		return nil, err
	}
	return obj.Flat(), nil
}

func thing(t *testing.T, flat map[string]any, name string) map[string]any {
	t.Helper()
	things, ok := flat["things"].(map[string]any)
	assert.Assert(t, ok, "no things in %v", flat)
	one, ok := things[name].(map[string]any)
	assert.Assert(t, ok, "no %s in %v", name, things)
	return one
}

func TestDefaultWhenReadsAValuedDiscriminator(t *testing.T) {
	schema := SchemaDefinition(Root(Attributes(String("id")),
		DefineGroup("things", DefineIter([]*Attribute{
			String("id", Required()),
			String("mode"),
			String("pick", Path("spec", "pick")),
			Int("retries", Default(3), DefaultWhen("mode", "queue")),
			Int("depth", Default(9), DefaultWhen("pick", "deep")),
			Int("never", Default(1), DefaultWhen("nobody", "x")),
		}))))

	type testCase struct {
		doc            string
		retries, depth any
	}
	for _, tc := range []testCase{
		{"id: one\nmode: queue\nspec:\n    pick: deep\n", 3, 9},
		{"id: one\nmode: log\n", nil, nil},
		{"id: one\n", nil, nil},
	} {
		flat, err := processFiles(t, schema, nil, nil, map[string]string{"/things/one.yaml": tc.doc})
		assert.NilError(t, err, tc.doc)
		one := thing(t, flat, "one")
		assert.Equal(t, one["retries"], tc.retries, tc.doc)
		assert.Equal(t, one["depth"], tc.depth, tc.doc)
		assert.Assert(t, one["never"] == nil, "a condition on an attribute the group lacks never holds")
	}
}

func TestAScalarWhereASwitchBlockBelongs(t *testing.T) {
	refused := SchemaDefinition(Root(Attributes(String("id")),
		DefineGroup("things", DefineIter([]*Attribute{
			String("id", Required()),
			String("engine", Path("engine", Either("postgres", "mysql")), Captures()),
		}))))
	_, err := processFiles(t, refused, nil, nil, map[string]string{"/things/one.yaml": "id: one\nengine: postgres\n"})
	assert.ErrorContains(t, err, "empty key")

	allowed := SchemaDefinition(Root(Attributes(String("id")),
		DefineGroup("things", DefineIter([]*Attribute{
			String("id", Required()),
			String("source", Path("source", Either("git", "path")), Captures()),
			String("at", Path("source")),
		}))))
	flat, err := processFiles(t, allowed, nil, nil, map[string]string{"/things/one.yaml": "id: one\nsource: agents/support\n"})
	assert.NilError(t, err)
	assert.Equal(t, thing(t, flat, "one")["at"], "agents/support")
}

func TestARequiredValueMayNotBeBlank(t *testing.T) {
	schema := SchemaDefinition(Root(Attributes(String("id")),
		DefineGroup("things", DefineIter([]*Attribute{
			String("id", Required()),
			String("name", Path("meta", "name"), Required()),
			StringSlice("tags", Required()),
		}))))
	_, err := processFiles(t, schema, nil, nil, map[string]string{"/things/one.yaml": "id: one\nmeta:\n    name: \"\"\ntags: [a]\n"})
	assert.ErrorContains(t, err, "required attribute 'meta/name' is empty")
	_, err = processFiles(t, schema, nil, nil, map[string]string{"/things/one.yaml": "id: one\nmeta:\n    name: n\ntags: []\n"})
	assert.ErrorContains(t, err, "required attribute 'tags' is empty")
	flat, err := processFiles(t, schema, nil, nil, map[string]string{"/things/one.yaml": "id: one\nmeta:\n    name: n\ntags: [a]\n"})
	assert.NilError(t, err)
	assert.Equal(t, thing(t, flat, "one")["name"], "n")
}

func TestAFloatIsReadFromEitherNotation(t *testing.T) {
	schema := SchemaDefinition(Root(Attributes(String("id")),
		DefineGroup("things", DefineIter([]*Attribute{String("id"), Float("ratio")}))))
	for doc, want := range map[string]float64{"id: one\nratio: 0.5\n": 0.5, "id: one\nratio: \"0.25\"\n": 0.25} {
		flat, err := processFiles(t, schema, nil, nil, map[string]string{"/things/one.yaml": doc})
		assert.NilError(t, err)
		assert.Equal(t, thing(t, flat, "one")["ratio"], want)
	}
	_, err := processFiles(t, schema, nil, nil, map[string]string{"/things/one.yaml": "id: one\nratio: half\n"})
	assert.ErrorContains(t, err, "attribute 'ratio' has a value of the wrong type")
}

// An attribute and a group may share a key: the attribute writes model/name, the group
// holds one entry per target under model. Neither may read the other's keys, and
// whichever loads second must not replace the first.
func sharedKeySchema() Schema {
	return SchemaDefinition(Root(Attributes(String("id")),
		DefineGroup("things", DefineIter([]*Attribute{
			String("id", Required()),
			String("modelName", Path("model", "name"), Varies("alpha")),
			String("deep", Path("model", "inner", "x"), Varies("alpha")),
			String("dyn", Path("model", Either("p", "q"))),
		}, Nested(DefineGroup("model", DefineIter([]*Attribute{StringSlice("ops", Required())})))))).With(Varying("alpha")))
}

func TestAnAttributeAndAGroupShareAKey(t *testing.T) {
	doc := "id: one\nmodel:\n    name: opus\n    name_a1: haiku\n    inner:\n        x: y\n    reader:\n        ops: [read]\n"
	values := map[string][]string{"alpha": {"a1"}}

	flat, err := processFiles(t, sharedKeySchema(), values, nil, map[string]string{"/things/one.yaml": doc})
	assert.NilError(t, err)
	one := thing(t, flat, "one")
	assert.Equal(t, one["modelName"], "opus")
	model := one["model"].(map[string]any)
	reader, ok := model["reader"].(map[string]any)
	assert.Assert(t, ok, "the group's entry survives the attribute writing beside it: %v", model)
	assert.DeepEqual(t, reader["ops"], []string{"read"})
	assert.Equal(t, len(model), 1, "name, name_a1 and inner are the attributes' keys, not entries: %v", model)
	assert.Equal(t, one["deep"], "y")

	flat, err = processFiles(t, sharedKeySchema(), values, Bound{"alpha": "a1"}, map[string]string{"/things/one.yaml": doc})
	assert.NilError(t, err)
	assert.Equal(t, thing(t, flat, "one")["modelName"], "haiku")
}

func TestMergeKeepsWhatWasThereFirst(t *testing.T) {
	into := map[string]any{"name": "first", "nested": map[string]any{"a": 1}}
	from := map[string]any{"name": "second", "extra": "x", "nested": map[string]any{"a": 2, "b": 3}}

	got := mergeMaps(t, into, from)
	assert.Equal(t, got["name"], "first")
	assert.Equal(t, got["extra"], "x")
	assert.DeepEqual(t, got["nested"], map[string]any{"a": 1, "b": 3})
}

func objectOf(t *testing.T, m map[string]any) object.Object[object.Refrence] {
	t.Helper()
	o := object.New[object.Refrence]()
	fill(t, o, m)
	return o
}

func fill(t *testing.T, o object.Object[object.Refrence], m map[string]any) {
	t.Helper()
	for k, v := range m {
		if sub, ok := v.(map[string]any); ok {
			child, err := o.CreatePath(k)
			assert.NilError(t, err)
			fill(t, child, sub)
			continue
		}
		o.Set(k, v)
	}
}

func mergeMaps(t *testing.T, into, from map[string]any) map[string]any {
	t.Helper()
	o := objectOf(t, into)
	mergeInto(o, objectOf(t, from))
	return o.Flat()
}

func TestAKeyedAttributeAndAGroupMergeIntoOneBlock(t *testing.T) {
	schema := SchemaDefinition(Root(Attributes(String("id")),
		DefineGroup("things", DefineIter([]*Attribute{
			String("id", Required()),
			String(Key("model", "name")),
		}, Nested(DefineGroup("model", DefineIter([]*Attribute{StringSlice("ops", Required())})))))))
	flat, err := processFiles(t, schema, nil, nil, map[string]string{
		"/things/one.yaml": "id: one\nmodel:\n    name: opus\n    reader:\n        ops: [read]\n",
	})
	assert.NilError(t, err)
	model := thing(t, flat, "one")["model"].(map[string]any)
	assert.Equal(t, model["name"], "opus")
	assert.DeepEqual(t, model["reader"], map[string]any{"ops": []string{"read"}})
}

// A kind that is both a resource and a container keeps its own entries in its document
// and the resources it holds in directories, under one key. The dump has to put each
// back where it was read from, which only the identity tells apart.
func hybridSchema() Schema {
	return SchemaDefinition(Root(Attributes(String("id")),
		DefineGroup("scopes", DefineIterGroup([]*Attribute{String("id", Required()), String("name")},
			DefineGroup("things", DefineIter([]*Attribute{String("id", Required()), String("name")})),
		).With(Leaf("scope"), Nested(
			DefineGroup("things", DefineIter([]*Attribute{StringSlice("ops", Required())})),
		))),
	).With(Leaf("config"), Identity("id", "name")))
}

func TestAHybridContainerRoundTrips(t *testing.T) {
	src := afero.NewMemMapFs()
	files := map[string]string{
		"/config.yaml":                  "id: root\n",
		"/scopes/shop/scope.yaml":       "id: s1\nthings:\n    elsewhere:\n        ops:\n            - read\n",
		"/scopes/shop/things/cart.yaml": "id: c1\n",
	}
	for p, body := range files {
		assert.NilError(t, afero.WriteFile(src, p, []byte(body), 0o644))
	}
	in, err := New(hybridSchema(), yaseer.VirtualFS(src, "/"))
	assert.NilError(t, err)
	obj, err := in.Process()
	assert.NilError(t, err)
	things := obj.Flat()["scopes"].(map[string]any)["shop"].(map[string]any)["things"].(map[string]any)
	assert.Equal(t, things["cart"].(map[string]any)["id"], "c1")
	assert.DeepEqual(t, things["elsewhere"], map[string]any{"ops": []string{"read"}})

	dst := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(dst, "/config.yaml", []byte(""), 0o644))
	out, err := New(hybridSchema(), yaseer.VirtualFS(dst, "/"))
	assert.NilError(t, err)
	assert.NilError(t, out.Dump(obj))

	scope, err := afero.ReadFile(dst, "/scopes/shop/scope.yaml")
	assert.NilError(t, err)
	assert.Equal(t, string(scope), files["/scopes/shop/scope.yaml"], "the entry stays in the scope's document")
	cart, err := afero.ReadFile(dst, "/scopes/shop/things/cart.yaml")
	assert.NilError(t, err)
	assert.Equal(t, string(cart), "id: c1\n")
	_, err = afero.ReadFile(dst, "/scopes/shop/things/elsewhere.yaml")
	assert.Assert(t, err != nil, "an entry is not a resource and gets no document of its own")
}
