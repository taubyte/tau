package specs_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/specs"
)

// What a real compile cannot produce: a truncated object, an index entry pointing at
// nothing, a value of the wrong type. A reader must not panic on them.

func TestBytesThatAreNotACompiledProject(t *testing.T) {
	_, err := specs.Load([]byte("{not json"))
	require.Error(t, err)

	_, err = specs.Load([]byte(`{"indexes":{}}`))
	require.ErrorIs(t, err, specs.ErrNoObject)

	_, err = specs.Load([]byte(`{"object":{"name":"shop"}}`))
	require.ErrorIs(t, err, specs.ErrNoID, "an object with no id cannot be indexed under one")

	// An object with no index at all loads: it is a project with nothing in it,
	// which is different from a broken one.
	p, err := specs.Load([]byte(`{"object":{"id":"p1","name":"shop"}}`))
	require.NoError(t, err)
	require.Equal(t, "p1", p.ID())
	require.Equal(t, "shop", p.Name())

	_, _, err = p.Resource(nil, "function", "anything")
	require.ErrorIs(t, err, specs.ErrNotFound)
}

func TestAnIndexEntryThatNamesNothing(t *testing.T) {
	type testCase struct {
		name string
		doc  string
	}
	for _, c := range []testCase{
		{"not a path at all", `{"object":{"id":"p1"},"indexes":{"projects/p1/function/f/links":["nonsense"]}}`},
		{"the wrong prefix", `{"object":{"id":"p1"},"indexes":{"projects/p1/function/f/links":["a/b/c/d/e"]}}`},
		{"a path into an empty object", `{"object":{"id":"p1"},"indexes":{"projects/p1/function/f/links":["branches/main/projects/p1/function/rid"]}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, err := specs.Load([]byte(c.doc))
			require.NoError(t, err)

			_, _, err = p.Resource(nil, "function", "f")
			require.ErrorIs(t, err, specs.ErrBadIndex)
		})
	}
}

func TestAMalformedNamespaceLink(t *testing.T) {
	p, err := specs.Load([]byte(`{"object":{"id":"p1"},"indexes":{"projects/p1/namespace/shop/links":[""]}}`))
	require.NoError(t, err)

	_, _, err = p.Resource([]string{"shop"}, "function", "f")
	require.ErrorIs(t, err, specs.ErrBadIndex)
}

func TestAnIndexEntryWithNoLinks(t *testing.T) {
	p, err := specs.Load([]byte(`{"object":{"id":"p1"},"indexes":{
		"projects/p1/function/f/links": [],
		"projects/p1/function/g/links": [17],
		"projects/p1/function/i/links": "not a list",
		"projects/p1/function/h":       ["branches/main/projects/p1/function/rid"]
	}}`))
	require.NoError(t, err)

	names, err := p.Names(nil, "function")
	require.NoError(t, err)
	require.Empty(t, names, "an entry with nothing usable in it is not a resource")
}

func TestTheLastLinkIsTheCurrentOne(t *testing.T) {
	p, err := specs.Load([]byte(`{
		"object":{"id":"p1","function":{"old":{"name":"f","source":{"image":"v1"}},"new":{"name":"f","source":{"image":"v2"}}}},
		"indexes":{"projects/p1/function/f/links":[
			"branches/main/projects/p1/function/old",
			"branches/main/projects/p1/function/new"
		]}}`))
	require.NoError(t, err)

	r, id, err := p.Resource(nil, "function", "f")
	require.NoError(t, err)
	require.Equal(t, "new", id)
	require.Equal(t, "v2", r.String("source", "image"))
}

func TestABranchWithASlashInItStaysOneSegment(t *testing.T) {
	p, err := specs.Load([]byte(`{
		"object":{"id":"p1","function":{"rid":{"name":"f"}}},
		"indexes":{"projects/p1/function/f/links":["branches/release%2F1.2/projects/p1/function/rid"]}}`))
	require.NoError(t, err)

	_, id, err := p.Resource(nil, "function", "f")
	require.NoError(t, err)
	require.Equal(t, "rid", id)
}

func TestTheAccessorsRefuseTheWrongType(t *testing.T) {
	p, err := specs.Load([]byte(`{
		"object":{"id":"p1","function":{"rid":{
			"name":"f",
			"execution":{"memory":128,"timeout":3000000000,"call":"handle"},
			"enabled":true,
			"egress":["a.example.com", 7, "b.example.com"],
			"ports":{"http":{"number":8080}}
		}}},
		"indexes":{"projects/p1/function/f/links":["branches/main/projects/p1/function/rid"]}}`))
	require.NoError(t, err)

	r, _, err := p.Resource(nil, "function", "f")
	require.NoError(t, err)

	require.True(t, r.Has("execution", "memory"))
	require.False(t, r.Has("execution", "nope"))
	require.False(t, r.Has("name", "deeper", "than", "a", "string"))

	mem, ok := r.Int("execution", "memory")
	require.True(t, ok)
	require.Equal(t, int64(128), mem)

	_, ok = r.Int("execution", "call")
	require.False(t, ok, "a string is not a number")

	_, ok = r.Int("execution", "missing")
	require.False(t, ok)

	d, ok := r.Duration("execution", "timeout")
	require.True(t, ok)
	require.Equal(t, 3*time.Second, d)

	_, ok = r.Duration("execution", "call")
	require.False(t, ok)

	on, ok := r.Bool("enabled")
	require.True(t, ok)
	require.True(t, on)

	_, ok = r.Bool("name")
	require.False(t, ok)

	// A list with something that is not a string in it keeps the strings and
	// drops the rest, rather than refusing the whole list or carrying a value
	// no consumer can use.
	require.Equal(t, []string{"a.example.com", "b.example.com"}, r.Strings("egress"))
	require.Nil(t, r.Strings("name"), "a string is not a list of them")
	require.Nil(t, r.Strings("missing"))

	require.Equal(t, []string{"http"}, r.Keys("ports"))
	require.Empty(t, r.Keys("missing"))
	require.Empty(t, r.Sub("name"), "a string has no sub-block")
	require.Equal(t, "", r.String("execution", "memory"))
}

func TestAListIsNotAMap(t *testing.T) {
	p, err := specs.Load([]byte(`{
		"object":{"id":"p1","gateway":{"rid":{
			"name":"g",
			"transformers":[
				{"name":"redact","fail":"closed"},
				{"name":"tier_header","fail":"open","namespace":"/shop"},
				"not a block"
			],
			"ports":{"http":{"number":8080}},
			"name_only":"a string"
		}}},
		"indexes":{"projects/p1/gateway/g/links":["branches/main/projects/p1/gateway/rid"]}}`))
	require.NoError(t, err)

	r, _, err := p.Resource(nil, "gateway", "g")
	require.NoError(t, err)

	list := r.List("transformers")
	require.Len(t, list, 2, "the entry that is not a block is dropped, the blocks are kept in order")
	require.Equal(t, "redact", list[0].String("name"))
	require.Equal(t, "closed", list[0].String("fail"))
	require.Equal(t, "/shop", list[1].String("namespace"))

	require.Empty(t, r.Keys("transformers"), "which is exactly why List exists")
	require.Nil(t, r.List("ports"), "a map is not a list either")
	require.Nil(t, r.List("name_only"))
	require.Nil(t, r.List("missing"))
}
