package interp

import (
	"testing"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/object"
	"gotest.tools/v3/assert"
)

func covFn(t *testing.T, flat map[string]any, path ...string) map[string]any {
	t.Helper()
	var cur any = flat
	if inner, ok := flat[ProjectKey]; ok {
		cur = inner
	}
	for _, p := range path {
		m, ok := cur.(map[string]any)
		assert.Assert(t, ok, "%v at %q", path, p)
		cur = m[p]
	}
	m, ok := cur.(map[string]any)
	assert.Assert(t, ok, "%v is %#v", path, cur)
	return m
}

func TestRefsResolveInTheirScopeThenOutward(t *testing.T) {
	flat, _, err := covCompile(t, covVocab{}, covWith(covProject, map[string]string{
		"/space/shop/space/eu/fn/c.yaml": "id: f3\nreads: [local]\n",
	}))
	assert.NilError(t, err)

	a := covFn(t, flat, "fn", "f1")
	assert.Equal(t, a["store"], "d1")
	assert.DeepEqual(t, a["reads"], []string{"d1"})

	b := covFn(t, flat, "space", "s1", "fn", "f2")
	assert.Equal(t, b["store"], "d2", "the scope's own db wins")
	assert.DeepEqual(t, b["reads"], []string{"d1", "d2"})

	c := covFn(t, flat, "space", "s1", "space", "s2", "fn", "f3")
	assert.DeepEqual(t, c["reads"], []string{"d2"})
}

func TestRefToANameThatDoesNotExist(t *testing.T) {
	for doc, want := range map[string]string{
		"id: f1\nstore: ghost\n":   "resolving db names to IDs failed",
		"id: f1\nreads: [ghost]\n": "resolving db names to IDs failed",
	} {
		_, _, err := covCompile(t, covVocab{}, covWith(covProject, map[string]string{"/fn/a.yaml": doc}))
		assert.ErrorContains(t, err, want, doc)
	}
}

func TestRefBlankAndByName(t *testing.T) {
	v := covVocab{fnAttrs: []*engine.Attribute{
		engine.String("blank", engine.Ref("db")),
		engine.StringSlice("named", engine.Ref("db", engine.ByName())),
	}}
	files := covWith(covProject, map[string]string{
		"/fn/a.yaml": "id: f1\nblank: ''\nnamed: [orders]\n",
	})
	flat, _, err := covCompile(t, v, files)
	assert.NilError(t, err)
	a := covFn(t, flat, "fn", "f1")
	assert.Equal(t, a["blank"], "", "an empty reference names nothing")
	assert.DeepEqual(t, a["named"], []string{"orders"})
}

func TestRefAtASegmentedPath(t *testing.T) {
	v := covVocab{fnAttrs: []*engine.Attribute{
		engine.String(engine.Key("conn", "db"), engine.Path("conn", "db"), engine.Ref("db")),
	}}
	flat, _, err := covCompile(t, v, covWith(covProject, map[string]string{
		"/fn/a.yaml": "id: f1\nconn:\n    db: orders\n",
	}))
	assert.NilError(t, err)
	got := covFn(t, flat, "fn", "f1")
	conn, ok := got["conn"].(map[string]any)
	assert.Assert(t, ok, "%#v", got)
	assert.Equal(t, conn["db"], "d1")

	flat, _, err = covCompile(t, v, covProject)
	assert.NilError(t, err)
	_, has := covFn(t, flat, "fn", "f1")["conn"]
	assert.Assert(t, !has, "nothing to resolve when the parent block is absent")
}

func TestRefInsideANestedGroup(t *testing.T) {
	v := covVocab{fnExtra: []any{engine.Nested(engine.DefineGroup("uses", engine.DefineIter([]*engine.Attribute{
		engine.String("db", engine.Ref("db")),
	})))}}
	flat, _, err := covCompile(t, v, covWith(covProject, map[string]string{
		"/space/shop/fn/b.yaml": "id: f2\nuses:\n    main:\n        db: local\n    fallback:\n        db: orders\n",
	}))
	assert.NilError(t, err)
	uses := covFn(t, flat, "space", "s1", "fn", "f2", "uses")
	assert.Equal(t, uses["main"].(map[string]any)["db"], "d2")
	assert.Equal(t, uses["fallback"].(map[string]any)["db"], "d1")

	_, _, err = covCompile(t, v, covWith(covProject, map[string]string{
		"/fn/a.yaml": "id: f1\nuses:\n    main:\n        db: ghost\n",
	}))
	assert.ErrorContains(t, err, "resolving db names to IDs failed")
}

func TestCollectRefAttrs(t *testing.T) {
	attrs := []*engine.Attribute{
		engine.String("plain"),
		engine.String("one", engine.Ref("db")),
		engine.StringSlice("many", engine.Ref("db")),
		engine.StringSlice("byname", engine.Ref("db", engine.ByName())),
		engine.String(engine.Key("conn", "db"), engine.Path("conn", "db"), engine.Ref("db")),
	}
	var got []string
	for _, r := range collectRefAttrs(attrs) {
		kind := "one"
		if r.list {
			kind = "list"
		}
		got = append(got, r.name()+":"+kind+":"+r.spec.Group)
	}
	assert.DeepEqual(t, got, []string{"one:one:db", "many:list:db", "conn/db:one:db"})
}

func TestRefOfTheWrongShape(t *testing.T) {
	o := covObj(t, map[string]any{"one": []string{"x"}, "many": "x"})
	one := refAttr{segments: []string{"one"}, spec: engine.RefSpec{Group: "db"}}
	many := refAttr{segments: []string{"many"}, list: true, spec: engine.RefSpec{Group: "db"}}
	missing := refAttr{segments: []string{"no", "where"}, spec: engine.RefSpec{Group: "db"}}
	sel := covSel(t, o)
	assert.ErrorContains(t, resolveRefAttr(nil, sel, one, "name"), "one is not a string")
	assert.ErrorContains(t, resolveRefAttr(nil, sel, many, "name"), "many is not a []string")
	assert.NilError(t, resolveRefAttr(nil, sel, missing, "name"), "an absent parent is nothing to resolve")
}

func covSel(t *testing.T, o object.Object[object.Refrence]) object.Selector[object.Refrence] {
	t.Helper()
	parent := object.New[object.Refrence]()
	assert.NilError(t, parent.Child("x").Add(o))
	return parent.Child("x")
}
