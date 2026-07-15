package config

import (
	"testing"

	"github.com/taubyte/tau/core/p2p/keypair"
	"golang.org/x/exp/slices"
	"gotest.tools/v3/assert"
)

func TestHostBindings(t *testing.T) {
	c, err := New(WithHosts(map[string]string{
		"admin.example.com": "gateway",
		"id.example.com":    "gateway",
		"api.example.com":   "auth",
	}))
	assert.NilError(t, err)

	// domain -> service (seer / httpsvc direction)
	svc, ok := c.ServiceForHost("admin.example.com")
	assert.Assert(t, ok)
	assert.Equal(t, svc, "gateway")

	_, ok = c.ServiceForHost("unknown.example.com")
	assert.Assert(t, !ok)

	// service -> domains (the direction a service reads to register its routes)
	gatewayHosts := c.HostsForService("gateway")
	assert.Equal(t, len(gatewayHosts), 2)
	assert.Assert(t, slices.Contains(gatewayHosts, "admin.example.com"))
	assert.Assert(t, slices.Contains(gatewayHosts, "id.example.com"))

	assert.Equal(t, len(c.HostsForService("nobody")), 0)
}

func TestRouteHosts(t *testing.T) {
	// Dev: host-agnostic (nil), regardless of fqdn.
	dev, err := New(WithDevMode(true), WithNetworkFqdn("example.com"))
	assert.NilError(t, err)
	assert.Equal(t, len(dev.RouteHosts("auth")), 0)

	// Prod: <svc>.tau.<fqdn> + <svc>.tau.<alias> + domains.hosts bindings.
	c, err := New(
		WithDevMode(false),
		WithPrivateKey(keypair.NewRaw()),
		WithNetworkFqdn("example.com"),
		WithHosts(map[string]string{
			"admin.example.com": "auth",
			"api.example.com":   "gateway", // bound to another service, excluded
		}),
	)
	assert.NilError(t, err)
	c.(*config).aliasDomains = []string{"example.net"}

	hosts := c.RouteHosts("auth")
	assert.Assert(t, slices.Contains(hosts, "auth.tau.example.com"), "canonical host")
	assert.Assert(t, slices.Contains(hosts, "auth.tau.example.net"), "alias host")
	assert.Assert(t, slices.Contains(hosts, "admin.example.com"), "domains.hosts binding")
	assert.Assert(t, !slices.Contains(hosts, "api.example.com"), "other service's binding excluded")
	assert.Equal(t, len(hosts), 3)

	// Memoized: a later mutation to the inputs doesn't change the cached result.
	c.(*config).aliasDomains = []string{"changed.com"}
	assert.Equal(t, len(c.RouteHosts("auth")), 3, "second call returns the cached set")
}
