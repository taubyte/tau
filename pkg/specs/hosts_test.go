package specs_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/specs"
)

func TestEveryNameTheProjectAnswersOn(t *testing.T) {
	hosts := load(t).Hosts()
	require.NotEmpty(t, hosts)

	by := map[string]specs.Host{}
	for _, h := range hosts {
		by[h.Fqdn] = h
	}

	// A zone the project declares: the name is complete.
	shop, ok := by["shop.example.com"]
	require.True(t, ok, "%+v", hosts)
	require.False(t, shop.Generated)
	require.Equal(t, "gateway", shop.Kind)
	require.Equal(t, "shop", shop.Name)
	require.Empty(t, shop.Scope)

	require.Contains(t, by, "api.example.com")
	require.Contains(t, by, "ask.example.com")

	// A generated name: what is here is the project's own portion, and it says
	// the cloud's fqdn is still missing rather than presenting a partial name
	// as a whole one.
	var generated []specs.Host
	for _, h := range hosts {
		if h.Generated {
			generated = append(generated, h)
		}
	}
	require.NotEmpty(t, generated)
	for _, h := range generated {
		require.NotContains(t, h.Fqdn, "example.com")
		require.Contains(t, h.Fqdn, ".", "a label plus the project's own")
	}

	// The proxy answers on a name too, which is what makes a postgres
	// reachable from outside at all.
	var kinds []string
	for _, h := range hosts {
		kinds = append(kinds, h.Kind)
	}
	require.Contains(t, kinds, "proxy")
}

func TestAHostAddressesSomethingThatCanBeLookedUp(t *testing.T) {
	p := load(t)

	for _, h := range p.Hosts() {
		r, _, err := p.Resource(h.Scope, h.Kind, h.Name)
		require.NoError(t, err, "%+v", h)
		require.Equal(t, h.Name, r.String("name"))
	}
}

// The ids below are real-looking CIDs: tcc decides what is a resource from the id,
// so a made-up one is walked past and the object reads as empty.

func TestANameWhoseZoneIsUnknownIsNotReported(t *testing.T) {
	p, err := specs.Load([]byte(`{
		"object":{"id":"QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR","gateway":{"QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY":{
			"name":"g",
			"host":{"kind":"zone","name":"shop","zone":{"in":{"name":"a-zone-not-in-this-project"}}}
		}}},
		"indexes":{"projects/QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR/gateway/g/links":[
			"branches/main/projects/QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR/gateway/QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY"]}}`))
	require.NoError(t, err)
	require.Empty(t, p.Hosts())
}

func TestAHostInsideANamespaceReportsTheScopeByName(t *testing.T) {
	p, err := specs.Load([]byte(`{
		"object":{"id":"QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR",
			"namespace":{"QmWATWQ7fVPP2EFGu71UkfnqhYXDYH6EFTM8XCXWzTHDeS":{"name":"shop","gateway":{"QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY":{
				"name":"inner","host":{"kind":"generated","name":"inner"}}}}}},
		"indexes":{
			"projects/QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR/namespace/shop/links":[
				"branches/main/projects/QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR/namespace/QmWATWQ7fVPP2EFGu71UkfnqhYXDYH6EFTM8XCXWzTHDeS"],
			"projects/QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR/namespace/QmWATWQ7fVPP2EFGu71UkfnqhYXDYH6EFTM8XCXWzTHDeS/gateway/inner/links":[
				"branches/main/projects/QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR/namespace/QmWATWQ7fVPP2EFGu71UkfnqhYXDYH6EFTM8XCXWzTHDeS/gateway/QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY"]}}`))
	require.NoError(t, err)

	hosts := p.Hosts()
	require.Len(t, hosts, 1)
	require.Equal(t, []string{"shop"}, hosts[0].Scope)
	require.True(t, hosts[0].Generated)

	_, _, err = p.Resource(hosts[0].Scope, hosts[0].Kind, hosts[0].Name)
	require.NoError(t, err)
}
