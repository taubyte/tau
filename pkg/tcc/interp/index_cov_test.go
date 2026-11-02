package interp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/object"
	"gotest.tools/v3/assert"
)

// covVocab is a small vocabulary with every cross-resource feature: a db kind, a fn kind
// that references dbs, and a `space` scope that holds both and spaces of its own. fnIndex and dbIndex are the index footprints.
type covVocab struct {
	fnIndex, dbIndex Indexer
	fnAttrs          []*engine.Attribute
	fnExtra          []any
}

func covIdentity(attrs ...*engine.Attribute) []*engine.Attribute {
	return append([]*engine.Attribute{engine.String("id", engine.Required(), engine.Generated(engine.GenCID)), engine.String("name")}, attrs...)
}

func (v covVocab) resources() []*engine.Node {
	db := engine.DefineIter(covIdentity(), engine.Resource("db", "Db", "Db", "db"))
	if v.dbIndex != nil {
		Indexes(1, v.dbIndex)(db)
	}
	fnAttrs := covIdentity(
		engine.String("store", engine.Ref("db")),
		engine.StringSlice("reads", engine.Ref("db")),
		engine.StringSlice("labels"),
		engine.String("host", engine.EmitValidation("dns", "hostCheck")),
	)
	fn := engine.DefineIter(append(fnAttrs, v.fnAttrs...), append([]any{engine.Resource("fn", "Fn", "Fn", "fn")}, v.fnExtra...)...)
	if v.fnIndex != nil {
		Indexes(0, v.fnIndex)(fn)
	}
	return []*engine.Node{
		engine.DefineGroup("db", db),
		engine.DefineGroup("fn", fn),
	}
}

func (v covVocab) space(depth int) *engine.Node {
	children := v.resources()
	if depth > 1 {
		children = append(children, v.space(depth-1))
	}
	return engine.DefineGroup("space", engine.DefineIterGroup(covIdentity(), children...).With(engine.Leaf("meta")))
}

func (v covVocab) root() *engine.Node {
	return engine.Root([]*engine.Attribute{engine.String("id")}, append(v.resources(), v.space(2))...).
		With(engine.Leaf("project"), engine.Identity("id", "name"))
}

// covProject is one project: dbs and fns at the root, in a space, and in a space inside it.
var covProject = map[string]string{
	"/project.yaml":                  "id: root\n",
	"/db/orders.yaml":                "id: d1\n",
	"/fn/a.yaml":                     "id: f1\nstore: orders\nreads: [orders]\nlabels: ['tag:hot', 'x']\nhost: a.com\n",
	"/space/shop/meta.yaml":          "id: s1\n",
	"/space/shop/db/local.yaml":      "id: d2\n",
	"/space/shop/fn/b.yaml":          "id: f2\nstore: local\nreads: [orders, local]\nhost: b.com\n",
	"/space/shop/space/eu/meta.yaml": "id: s2\n",
	"/space/shop/space/eu/fn/c.yaml": "id: f3\nreads: [orders, local]\n",
	"/space/empty/meta.yaml":         "id: s3\n",
}

func covCompile(t *testing.T, v covVocab, files map[string]string, opts ...Option) (map[string]any, []NextValidation, error) {
	t.Helper()
	root := v.root()
	fs := afero.NewMemMapFs()
	for p, s := range files {
		assert.NilError(t, afero.WriteFile(fs, p, []byte(s), 0o644))
	}
	c, err := New(engine.SchemaDefinition(root), root, append([]Option{WithVirtual(fs, "/")}, opts...)...)
	assert.NilError(t, err)
	obj, vals, err := c.Compile(context.Background())
	if err != nil {
		return nil, vals, err
	}
	return obj.Flat(), vals, nil
}

func covWith(files map[string]string, extra map[string]string) map[string]string {
	out := map[string]string{}
	for k, s := range files {
		out[k] = s
	}
	for k, s := range extra {
		out[k] = s
	}
	return out
}

func scopeKey(ic IndexCtx) string {
	parts := make([]string, 0, len(ic.Scope))
	for _, l := range ic.Scope {
		parts = append(parts, l.Kind+":"+l.ID)
	}
	return strings.Join(parts, ",")
}

func TestIndexDriverWritesWhatTheVocabularyReturns(t *testing.T) {
	v := covVocab{
		fnIndex: func(ic IndexCtx) ([]IndexEntry, error) {
			reads, err := ic.Each("reads", "db", "name")
			if err != nil {
				return nil, err
			}
			notList, err := ic.Each("store", "db", "name")
			assert.NilError(t, err)
			assert.Assert(t, notList == nil)
			return []IndexEntry{
				Link("by-name/"+ic.Scope.IDs()+"/"+ic.Name, ic.ID),
				Link("by-name/"+ic.Scope.IDs()+"/"+ic.Name, ic.ID),
				Link("all", ic.Kind+"/"+ic.ID),
				Set("who/"+ic.ID, ic.Project+"|"+ic.Branch+"|"+scopeKey(ic)),
				Set("reads/"+ic.ID, strings.Join(reads, ",")),
			}, nil
		},
		dbIndex: func(ic IndexCtx) ([]IndexEntry, error) {
			return []IndexEntry{Link("all", ic.Kind+"/"+ic.ID)}, nil
		},
	}
	flat, vals, err := covCompile(t, v, covProject, WithBranch("main"))
	assert.NilError(t, err)

	idx := flat[IndexesKey].(map[string]any)
	assert.DeepEqual(t, idx["by-name//a"], []string{"f1"})
	assert.DeepEqual(t, idx["by-name/s1/b"], []string{"f2"})
	assert.DeepEqual(t, idx["by-name/s1/s2/c"], []string{"f3"})

	assert.Equal(t, idx["who/f1"], "root|main|")
	assert.Equal(t, idx["who/f2"], "root|main|space:s1")
	assert.Equal(t, idx["who/f3"], "root|main|space:s1,space:s2")

	// a scope's own db shadows nothing it does not have: "orders" is found at the root
	assert.Equal(t, idx["reads/f1"], "orders")
	assert.Equal(t, idx["reads/f2"], "orders,local")
	// s2 has no db group of its own: local is found in its parent, orders at the project
	assert.Equal(t, idx["reads/f3"], "orders,local")

	// fn at position 0 is indexed, in every scope, before db at position 1
	all := idx["all"].([]string)
	assert.Equal(t, all[0], "fn/f1")
	assert.Equal(t, all[len(all)-1], "db/d2")
	assert.Equal(t, strings.Join(all, " "), "fn/f1 fn/f2 fn/f3 db/d1 db/d2")

	assert.DeepEqual(t, vals, []NextValidation{
		engine.NewNextValidation("dns", "a.com", "hostCheck", map[string]any{"project": "root"}),
		engine.NewNextValidation("dns", "b.com", "hostCheck", map[string]any{"project": "root", "app": "s1"}),
	})

	_, ok := flat[ProjectKey].(map[string]any)
	assert.Assert(t, ok, "an indexed compile is chrooted under %q", ProjectKey)
}

func TestIndexDriverReportsAnIndexerError(t *testing.T) {
	v := covVocab{fnIndex: func(ic IndexCtx) ([]IndexEntry, error) {
		if ic.ID == "f2" {
			return nil, errors.New("boom")
		}
		return nil, nil
	}}
	_, _, err := covCompile(t, v, covProject)
	assert.ErrorContains(t, err, "indexing fn f2 failed with boom")
}

func TestIndexEachRefusesWhatItCannotRead(t *testing.T) {
	missing := covVocab{fnIndex: func(ic IndexCtx) ([]IndexEntry, error) {
		_, err := ic.Each("labels", "db", "name")
		return nil, err
	}}
	_, _, err := covCompile(t, missing, covProject)
	assert.ErrorContains(t, err, "db/tag:hot, which labels references, is not in the compiled configuration")

	notString := covVocab{fnIndex: func(ic IndexCtx) ([]IndexEntry, error) {
		_, err := ic.Each("reads", "db", "nothing")
		return nil, err
	}}
	_, _, err = covCompile(t, notString, covProject)
	assert.ErrorContains(t, err, "nothing of db/d1 is not a string")
}

func TestIndexLookupPrefersTheScope(t *testing.T) {
	var got []string
	v := covVocab{fnIndex: func(ic IndexCtx) ([]IndexEntry, error) {
		for _, id := range []string{"d1", "d2", "nope"} {
			o, ok := ic.Lookup("db", id)
			if !ok {
				got = append(got, ic.ID+":"+id+"=missing")
				continue
			}
			name, err := o.GetString("name")
			assert.NilError(t, err)
			got = append(got, ic.ID+":"+id+"="+name)
		}
		return nil, nil
	}}
	_, _, err := covCompile(t, v, covProject)
	assert.NilError(t, err)
	assert.DeepEqual(t, got, []string{
		"f1:d1=orders", "f1:d2=missing", "f1:nope=missing",
		"f2:d1=orders", "f2:d2=local", "f2:nope=missing",
		"f3:d1=orders", "f3:d2=local", "f3:nope=missing",
	})
}

func TestScopeChainIDs(t *testing.T) {
	assert.Equal(t, ScopeChain(nil).IDs(), "")
	assert.Equal(t, ScopeChain{{Kind: "space", ID: "a"}, {Kind: "made", ID: "b"}}.IDs(), "a/b")
}

func TestIndexEntryConstructors(t *testing.T) {
	assert.DeepEqual(t, Link("p", 1), IndexEntry{Path: "p", Value: 1, Append: true})
	assert.DeepEqual(t, Set("p", "v"), IndexEntry{Path: "p", Value: "v"})
}

func TestIndexAllStopsAtTheFirstError(t *testing.T) {
	calls := 0
	ok := func(IndexCtx) ([]IndexEntry, error) { calls++; return []IndexEntry{Set("a", 1)}, nil }
	bad := func(IndexCtx) ([]IndexEntry, error) { calls++; return nil, errors.New("no") }
	out, err := indexAll(ok, ok)(IndexCtx{})
	assert.NilError(t, err)
	assert.Equal(t, len(out), 2)
	calls = 0
	out, err = indexAll(ok, bad, ok)(IndexCtx{})
	assert.ErrorContains(t, err, "no")
	assert.Assert(t, out == nil)
	assert.Equal(t, calls, 2)
}

func TestIndexedGroupsOrderByPosition(t *testing.T) {
	none := func(IndexCtx) ([]IndexEntry, error) { return nil, nil }
	root := covVocab{fnIndex: none, dbIndex: none}.root()
	root.Children = append(root.Children, &engine.Node{Group: true, Match: "empty"})
	assert.DeepEqual(t, indexedGroups(root), []string{"fn", "db"})
	assert.Assert(t, findGroup(root, "db") != nil)
	assert.Assert(t, findGroup(root, "nope") == nil)
	_, found := indexerOf(nil)
	assert.Assert(t, !found)
}

func TestAppendLinkKeepsOneOfEach(t *testing.T) {
	o := object.New[object.Refrence]()
	appendLink(o, "k", "a")
	appendLink(o, "k", "b")
	appendLink(o, "k", "a")
	assert.DeepEqual(t, o.Get("k"), []string{"a", "b"})
	o.Set("n", 5)
	appendLink(o, "n", "x")
	assert.DeepEqual(t, o.Get("n"), []string{"x"})
}

func covObj(t *testing.T, m map[string]any) object.Object[object.Refrence] {
	t.Helper()
	o := object.New[object.Refrence]()
	for k, v := range m {
		if sub, ok := v.(map[string]any); ok {
			assert.NilError(t, o.Child(k).Add(covObj(t, sub)))
			continue
		}
		o.Set(k, v)
	}
	return o
}

func TestScopeOfFindsTheChain(t *testing.T) {
	root := covObj(t, map[string]any{
		"space": map[string]any{
			"s1": map[string]any{"space": map[string]any{"s2": map[string]any{"name": "eu"}}},
		},
		"made": map[string]any{"m1": map[string]any{"name": "x"}},
	})
	s1, err := root.Fetch("space", "s1")
	assert.NilError(t, err)
	s2, err := s1.Fetch("space", "s2")
	assert.NilError(t, err)
	m1, err := root.Fetch("made", "m1")
	assert.NilError(t, err)

	containers := []string{"missing", "space", "made"}
	assert.DeepEqual(t, scopeOf(root, s1, containers), ScopeChain{{"space", "s1"}})
	assert.DeepEqual(t, scopeOf(root, s2, containers), ScopeChain{{"space", "s1"}, {"space", "s2"}})
	assert.DeepEqual(t, scopeOf(root, m1, containers), ScopeChain{{"made", "m1"}})
	assert.Assert(t, scopeOf(root, object.New[object.Refrence](), containers) == nil)
}

func TestAttrValuesAndString(t *testing.T) {
	inst := covObj(t, map[string]any{
		"one":   "x",
		"empty": "",
		"many":  []string{"a", "b"},
		"mixed": []any{"a", 3, "", "b"},
		"num":   7,
		"deep":  map[string]any{"leaf": "y", "list": []string{"z"}},
	})
	attr := func(name string, segs ...string) *engine.Attribute {
		return &engine.Attribute{Name: name, Segments: segs}
	}
	type testCase struct {
		a    *engine.Attribute
		vals []string
		str  string
	}
	cases := []testCase{
		{attr("one"), []string{"x"}, "x"},
		{attr("empty"), nil, ""},
		{attr("many"), []string{"a", "b"}, ""},
		{attr("mixed"), []string{"a", "b"}, ""},
		{attr("num"), nil, ""},
		{attr("absent"), nil, ""},
		{attr("deep-leaf", "deep", "leaf"), []string{"y"}, "y"},
		{attr("deep-list", "deep", "list"), []string{"z"}, ""},
		{attr("nowhere", "nowhere", "leaf"), nil, ""},
	}
	for _, c := range cases {
		t.Run(c.a.Name, func(t *testing.T) {
			assert.DeepEqual(t, attrValues(inst, c.a), c.vals)
			s, ok := attrString(inst, c.a)
			assert.Equal(t, s, c.str)
			assert.Equal(t, ok, c.str != "")
		})
	}
}

func TestEmitAttrValidationsPerValue(t *testing.T) {
	v := covVocab{
		fnIndex: func(IndexCtx) ([]IndexEntry, error) { return nil, nil },
		fnAttrs: []*engine.Attribute{engine.StringSlice("aliases", engine.EmitValidation("alias", "aliasCheck"))},
	}
	files := covWith(covProject, map[string]string{
		"/fn/a.yaml": "id: f1\naliases: [p.com, q.com]\n",
	})
	_, vals, err := covCompile(t, v, files)
	assert.NilError(t, err)
	var got []string
	for _, nv := range vals {
		got = append(got, fmt.Sprint(nv.Key, "=", nv.Value, "@", nv.Context["app"]))
	}
	assert.DeepEqual(t, got, []string{"alias=p.com@<nil>", "alias=q.com@<nil>", "dns=b.com@s1"})
}
