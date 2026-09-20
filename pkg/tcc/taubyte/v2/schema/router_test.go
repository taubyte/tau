package schema_test

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// EITHER A ROUTER OR ROUTES, and inside a route either a handler or a router.
//
// A gateway with a `router` has no static routes at all: the function is handed every
// request and says where it goes. That is what makes one gateway able to serve a site,
// where a static route serves one path.
//
// Nothing checks this afterwards. It is the Either()+Captures() switch the vocabulary
// already uses for a storage's type and a database's engine — the loader refuses a
// document that opens two branches or none, so "exactly one" is a property of the
// declaration rather than a finding about the result.
func gatewayWith(t *testing.T, body string) error {
	t.Helper()
	fs := afero.NewMemMapFs()
	for path, doc := range map[string]string{
		"/project.yaml":       "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nname: p\nregions: [eu]\n",
		"/function/edge.yaml": "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\nimplements: gateway-router\nexecution:\n    call: route\n    timeout: 1s\n    memory: 16MB\n",
		"/function/api.yaml":  "id: QmTaCZBnrxvJm5xkLQKN4o3TFhBqHfLYCUuCkFTGiT8s8t\nexecution:\n    call: api\n    timeout: 1s\n    memory: 16MB\n",
		"/gateway/g.yaml":     "id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zg\nhost:\n    generated:\n        name: shop\n" + body,
	} {
		assert.NilError(t, afero.WriteFile(fs, path, []byte(doc), 0o644))
	}
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	return err
}

func TestAGatewayRoutesOneWay(t *testing.T) {
	// a function routes everything: no path, no match, no handler
	assert.NilError(t, gatewayWith(t, "router:\n    name: edge\n"))

	// or one static route, ending at a handler
	assert.NilError(t, gatewayWith(t, "route:\n    path: /\n    handler:\n        function:\n            name: api\n"))

	// or one static route that hands the decision on
	assert.NilError(t, gatewayWith(t, "route:\n    path: /\n    router:\n        name: edge\n"))

	// BOTH is two answers and no rule saying which wins, and the loader says which two
	err := gatewayWith(t, "router:\n    name: edge\nroute:\n    path: /\n    handler:\n        function:\n            name: api\n")
	assert.ErrorContains(t, err, "route and router each match, and only one may be set")

	// NEITHER is a name that answers with nothing behind it
	assert.ErrorContains(t, gatewayWith(t, ""), "attribute routing has an empty key")

	// and a route that names a handler AND a router
	err = gatewayWith(t, "route:\n    path: /\n    handler:\n        function:\n            name: api\n    router:\n        name: edge\n")
	assert.ErrorContains(t, err, "handler and router each match, and only one may be set")
}

// A ROUTER IS BUILT AGAINST ITS OWN WORLD: it answers with a destination, not a
// response, and a gateway's router is handed a parsed request where a proxy's gets
// bytes. Naming an ordinary handler as a router compiles and fails on live traffic.
func TestARouterIsTypedByItsWorld(t *testing.T) {
	assert.NilError(t, gatewayWith(t, "router:\n    name: edge\n"))

	found := false
	for _, f := range auditGateway(t, "router:\n    name: api\n") {
		if f.Severity == schema.Broken {
			found = true
		}
	}
	assert.Assert(t, found, "a plain handler named as a router is not reported")
}

func auditGateway(t *testing.T, body string) []schema.Finding {
	t.Helper()
	fs := afero.NewMemMapFs()
	for path, doc := range map[string]string{
		"/project.yaml":       "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nname: p\nregions: [eu]\n",
		"/function/edge.yaml": "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\nimplements: gateway-router\nexecution:\n    call: route\n    timeout: 1s\n    memory: 16MB\n",
		"/function/api.yaml":  "id: QmTaCZBnrxvJm5xkLQKN4o3TFhBqHfLYCUuCkFTGiT8s8t\nexecution:\n    call: api\n    timeout: 1s\n    memory: 16MB\n",
		"/gateway/g.yaml":     "id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zg\nhost:\n    generated:\n        name: shop\n" + body,
	} {
		assert.NilError(t, afero.WriteFile(fs, path, []byte(doc), 0o644))
	}
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	return schema.Audit(obj.Flat(), nil)
}

// A ROUTER LIVES SOMEWHERE, and the namespace beside its name is how the gateway says
// where. Without it the name resolves outward from the gateway's own scope and quietly
// finds a different function, or none — the failure a `{namespace, name}` block exists
// to prevent, and the one a `<kind>/<name>` string could not even express.
func TestARouterCanLiveInAnotherNamespace(t *testing.T) {
	fs := afero.NewMemMapFs()
	for path, doc := range map[string]string{
		"/project.yaml": "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nname: p\nregions: [eu]\n",
		// same NAME in two scopes: only the namespace tells them apart
		"/function/edge.yaml":                "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\nexecution:\n    call: nope\n    timeout: 1s\n    memory: 16MB\n",
		"/namespace/shop/meta.yaml":          "id: QmQPeNsJPyVWPFDVHb77w8G42Fvo15z4bmmjRoBHfW1YZv\n",
		"/namespace/shop/function/edge.yaml": "id: QmTaCZBnrxvJm5xkLQKN4o3TFhBqHfLYCUuCkFTGiT8s8t\nimplements: gateway-router\nexecution:\n    call: route\n    timeout: 1s\n    memory: 16MB\n",
		"/gateway/g.yaml":                    "id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zg\nhost:\n    generated:\n        name: shop\nrouter:\n    namespace: /shop\n    name: edge\n",
	} {
		assert.NilError(t, afero.WriteFile(fs, path, []byte(doc), 0o644))
	}
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	// it resolved to the one in /shop, not the root one that implements `handler`
	var to string
	for _, rel := range schema.Relations(obj.Flat()) {
		if rel.Class == schema.Handoff {
			for _, r := range schema.Resources(obj.Flat()) {
				if r.ID == rel.To {
					to = r.Scope + ":" + r.Kind + "/" + r.Name
				}
			}
		}
	}
	assert.Equal(t, to, "shop:function/edge", "the namespace decides which `edge` routes")

	// and the world was checked against THAT one, so the root's plain handler is not
	// mistaken for a router
	for _, f := range schema.Audit(obj.Flat(), nil) {
		assert.Assert(t, f.Severity != schema.Broken, "%v", f)
	}
}

// EVERY REFERENCE SAYS WHERE. A block that names a resource and cannot name its
// namespace resolves outward from the declarer's own scope and silently finds a
// different resource — which is the failure `<kind>/<name>` could not even express, and
// the reason a reference is a block at all.
//
// Seventeen of them had only a name: the conversion added `{name}` and stopped. Three
// more had a namespace the discovery could not see, because it looked for the sibling by
// trimming a "name" suffix that `source/component` does not have.
func TestEveryReferenceCanSayWhere(t *testing.T) {
	var missing []string
	for _, r := range schema.References() {
		if r.Namespace == "" {
			missing = append(missing, r.On+"."+r.Field)
		}
	}
	assert.DeepEqual(t, missing, []string(nil))
}
