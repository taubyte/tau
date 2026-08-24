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

// WHAT A RESOURCE IS is the schema's answer, and a consumer that walks the compiled
// object itself gets a different one: an access block is a map of maps keyed by a kind,
// exactly like a nested group, so `cache: { orders: {ops: [get]} }` reads as a cache
// called orders unless you know the schema. Resources exists so nobody has to.
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
	// would break: `orders` is a cache, a channel and a database in this fixture, and
	// every resource that reaches one would add another copy.
	seen := map[string]bool{}
	for _, r := range all {
		key := r.Scope + "\x00" + r.Kind + "\x00" + r.Name
		assert.Assert(t, !seen[key], "%s/%s appears twice in %q", r.Kind, r.Name, r.Scope)
		seen[key] = true
	}
}

// A RELATION POINTS AT RESOURCES, both ends, always. An id that resolves to nothing is a
// relation a consumer silently drops — which is how a missing edge looks in a drawing:
// like a config that does not use anything there.
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

// A NAMESPACE BESIDE A NAME IS RELATIVE. `notify` lives in `shop` and reaches `relay`
// with `namespace: eu`, which means `shop/eu` — a resolver that starts looking at `eu`
// finds nothing, and the use disappears without a word.
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
			assert.Equal(t, to.Scope, "shop/eu")
			assert.Equal(t, string(rel.Class), string(schema.Use))
			found = true
		}
	}
	assert.Assert(t, found, "notify -> relay, declared with a relative namespace, resolved to nothing")
}

// A DELIVERED VERB RUNS THE OTHER WAY. `subscribe` is declared by the consumer and the
// work travels toward it: drawn from the declaration, a subscriber has an arrow out and
// none in, and reads as a workload nothing ever starts.
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
		assert.Equal(t, from.Kind, "channel", "subscribe runs from what delivers")
		assert.Equal(t, to.Kind, "function", "...toward what it is delivered to")
		found = true
	}
	assert.Assert(t, found)
}

// A NAMED ENTRY'S REFERENCE IS AN EDGE, and it says which entry it came from.
//
// A reference declared inside a nested group was only ever walked when the group was an
// ordered LIST, so every `variables: {NAME: {secret: {name: …}}}` drew nothing on any of
// the six kinds that carry a variables block — while the schema said out loud that naming
// a secret there is a use of it. Silent, and the drawing was the only place it would have
// shown: nothing else reads that reference.
func TestANamedEntryReferenceDrawsAnEdge(t *testing.T) {
	obj := compiledExample(t)
	byID := map[string]schema.Instance{}
	for _, r := range schema.Resources(obj) {
		byID[r.ID] = r
	}

	var found []string
	for _, rel := range schema.Relations(obj) {
		if byID[rel.From].Kind != "secret" || byID[rel.To].Kind != "application" {
			continue
		}
		if !strings.HasPrefix(rel.Verb, "variables ") {
			continue // the same secret is also read directly and pulls an image
		}
		found = append(found, byID[rel.From].Name+" -"+rel.Verb+"-> "+byID[rel.To].Name)
	}

	// The arrow runs FROM the secret, because that reference is Inbound(): a secret's
	// value travels out of it, and there is no verb that writes one from a workload. The
	// entry is named by the author, so the edge is named by the entry rather than by a
	// position, which a map does not have.
	assert.DeepEqual(t, found, []string{"stripe -variables STRIPE_KEY-> api"})
}
