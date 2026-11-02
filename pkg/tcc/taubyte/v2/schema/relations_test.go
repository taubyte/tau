package schema_test

import (
	"context"
	"strings"
	"testing"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

func compiledExample(t *testing.T) map[string]any {
	t.Helper()
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	return obj.Flat()
}

func TestResourcesAreResourcesOnly(t *testing.T) {
	all := schema.Resources(compiledExample(t))
	assert.Assert(t, len(all) > 0)

	for _, r := range all {
		// every one is a compiled resource: keyed by its id, carrying its own name
		assert.Assert(t, strings.HasPrefix(r.ID, "Qm"), "%s/%s has id %q, which is not a compiled id", r.Kind, r.Name, r.ID)
		assert.Assert(t, r.Name != "", "%s %s has no name", r.Kind, r.ID)
		name, _ := r.Fields["name"].(string)
		assert.Equal(t, name, r.Name)
	}

	// ...and there is exactly one of each, which an access entry drawn as a resource
	// would break: `orders` is a cache, a stream and a database in this fixture, and
	// every resource that reaches one would add another copy.
	seen := map[string]bool{}
	for _, r := range all {
		key := r.Scope + "\x00" + r.Kind + "\x00" + r.Name
		assert.Assert(t, !seen[key], "%s/%s appears twice in %q", r.Kind, r.Name, r.Scope)
		seen[key] = true
	}
}

func TestRelationsResolveBothEnds(t *testing.T) {
	obj := compiledExample(t)
	byID := map[string]schema.Instance{}
	for _, r := range schema.Resources(obj) {
		byID[r.ID] = r
	}

	rels := schema.Relations(obj)
	assert.Assert(t, len(rels) > 0)
	for _, rel := range rels {
		_, from := byID[rel.From]
		_, to := byID[rel.To]
		assert.Assert(t, from, "relation %+v starts at no resource", rel)
		assert.Assert(t, to, "relation %+v ends at no resource", rel)
		assert.Assert(t, rel.From != rel.To, "%s points at itself: %+v", byID[rel.From].Kind, rel)
	}
}

func TestRelationsResolveARelativeNamespace(t *testing.T) {
	obj := compiledExample(t)
	byID := map[string]schema.Instance{}
	for _, r := range schema.Resources(obj) {
		byID[r.ID] = r
	}

	found := false
	for _, rel := range schema.Relations(obj) {
		from, to := byID[rel.From], byID[rel.To]
		if from.Name == "notify" && to.Name == "relay" && rel.Verb == "invoke" {
			assert.Equal(t, to.Scope, "shop/local")
			assert.Equal(t, string(rel.Class), string(schema.Use))
			found = true
		}
	}
	assert.Assert(t, found, "notify -> relay, declared with a relative namespace, resolved to nothing")
}

func TestRelationsTurnDeliveredVerbsAround(t *testing.T) {
	obj := compiledExample(t)
	byID := map[string]schema.Instance{}
	for _, r := range schema.Resources(obj) {
		byID[r.ID] = r
	}

	found := false
	for _, rel := range schema.Relations(obj) {
		if rel.Verb != "subscribe" {
			continue
		}
		from, to := byID[rel.From], byID[rel.To]
		assert.Equal(t, from.Kind, "stream", "subscribe runs from what delivers")
		assert.Equal(t, to.Kind, "function", "...toward what it is delivered to")
		found = true
	}
	assert.Assert(t, found)
}

func TestANamedEntryReferenceDrawsAnEdge(t *testing.T) {
	obj := compiledExample(t)
	byID := map[string]schema.Instance{}
	for _, r := range schema.Resources(obj) {
		byID[r.ID] = r
	}

	var found []string
	for _, rel := range schema.Relations(obj) {
		if byID[rel.From].Kind != "package" || byID[rel.To].Kind != "package" {
			continue
		}
		found = append(found, byID[rel.From].Name+" -"+rel.Verb+"-> "+byID[rel.To].Name)
	}

	// A build input is an entry named by the author, so the edge is named by the entry
	// rather than by a position, which a map does not have. It sits in a block, and is
	// addressed through it.
	assert.DeepEqual(t, found, []string{"shop_site -build/inputs pay-> paylib"})
}
