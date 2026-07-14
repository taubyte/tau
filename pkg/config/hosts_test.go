package config

import (
	"testing"

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
