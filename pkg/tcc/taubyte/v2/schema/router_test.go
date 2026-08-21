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
		"/project.yaml":       "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nname: p\n",
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
		"/project.yaml":       "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nname: p\n",
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
