package project_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/project"
)

// malformed_test.go is the half that cannot come out of a real compile.
//
// project_test.go reads what tcc actually produces, which is the right test for
// the shape. What it cannot produce is a truncated object, an index entry
// pointing at nothing, or a value of the wrong type — and those are exactly the
// cases where a reader that assumed instead of checking would panic on the
// request path.

func TestBytesThatAreNotACompiledProject(t *testing.T) {
	_, err := project.Load([]byte("{not json"))
	require.Error(t, err)

	_, err = project.Load([]byte(`{"indexes":{}}`))
	require.ErrorIs(t, err, project.ErrNoObject)

	_, err = project.Load([]byte(`{"object":{"name":"shop"}}`))
	require.ErrorIs(t, err, project.ErrNoID, "an object with no id cannot be indexed under one")

	// An object with no index at all loads: it is a project with nothing in it,
	// which is different from a broken one.
	p, err := project.Load([]byte(`{"object":{"id":"p1","name":"shop"}}`))
	require.NoError(t, err)
	require.Equal(t, "p1", p.ID())
	require.Equal(t, "shop", p.Name())

	_, _, err = p.Resource(nil, "function", "anything")
	require.ErrorIs(t, err, project.ErrNotFound)
}

// TestAnIndexEntryThatNamesNothing. Both halves have to be checked: a link that
// is not a well-formed path, and one that is but points where the object has
// nothing.
func TestAnIndexEntryThatNamesNothing(t *testing.T) {
	for _, c := range []struct {
		name string
		doc  string
	}{
		{"not a path at all", `{"object":{"id":"p1"},"indexes":{"projects/p1/function/f/links":["nonsense"]}}`},
		{"the wrong prefix", `{"object":{"id":"p1"},"indexes":{"projects/p1/function/f/links":["a/b/c/d/e"]}}`},
		{"a path into an empty object", `{"object":{"id":"p1"},"indexes":{"projects/p1/function/f/links":["branches/main/projects/p1/function/rid"]}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, err := project.Load([]byte(c.doc))
			require.NoError(t, err)

			_, _, err = p.Resource(nil, "function", "f")
			require.ErrorIs(t, err, project.ErrBadIndex)
		})
	}
}

// TestAMalformedNamespaceLink. A scope that cannot be resolved is not a scope,
// and it must not silently become the project root — a lookup there would
// answer with somebody else's resource.
func TestAMalformedNamespaceLink(t *testing.T) {
	p, err := project.Load([]byte(`{"object":{"id":"p1"},"indexes":{"projects/p1/namespace/shop/links":[""]}}`))
	require.NoError(t, err)

	_, _, err = p.Resource([]string{"shop"}, "function", "f")
	require.ErrorIs(t, err, project.ErrBadIndex)
}

// TestAnIndexEntryWithNoLinks is dropped rather than kept as an empty bucket, so
// a lookup is a miss instead of an entry that resolves to nothing.
func TestAnIndexEntryWithNoLinks(t *testing.T) {
	p, err := project.Load([]byte(`{"object":{"id":"p1"},"indexes":{
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

// TestTheLastLinkIsTheCurrentOne. The bucket accumulates one entry per publish;
// serving the first would serve whatever was published first, forever.
func TestTheLastLinkIsTheCurrentOne(t *testing.T) {
	p, err := project.Load([]byte(`{
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

// TestABranchWithASlashInItStaysOneSegment. tcc percent-encodes it; a path
// walked without that assumption would be off by one and land on the wrong
// resource silently.
func TestABranchWithASlashInItStaysOneSegment(t *testing.T) {
	p, err := project.Load([]byte(`{
		"object":{"id":"p1","function":{"rid":{"name":"f"}}},
		"indexes":{"projects/p1/function/f/links":["branches/release%2F1.2/projects/p1/function/rid"]}}`))
	require.NoError(t, err)

	_, id, err := p.Resource(nil, "function", "f")
	require.NoError(t, err)
	require.Equal(t, "rid", id)
}

func TestTheAccessorsRefuseTheWrongType(t *testing.T) {
	p, err := project.Load([]byte(`{
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
