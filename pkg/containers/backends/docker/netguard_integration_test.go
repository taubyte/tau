//go:build docker_integration

package docker

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"testing"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/netguard"
)

// TestRestrictedEgress_Integration is the end-to-end proof of the container half
// of netguard on this backend. A restricted container must not reach a
// node-local service, must still reach the public internet, and must still
// resolve names. An unrestricted container must be left alone — the rule matches
// tau's own bridge, so nothing else on the host is collateral.
//
// It needs root: programming nftables takes CAP_NET_ADMIN, and a rootless docker
// puts its bridge in a user network namespace the host's netfilter never sees
// (there the installer fails closed instead, which is the correct outcome but
// not what this test measures).
func TestRestrictedEgress_Integration(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("programming nftables needs CAP_NET_ADMIN; run this test as root")
	}

	backend := newTestBackend(t)
	ctx := context.Background()
	require.NoError(t, backend.Image(conformanceImage).Pull(ctx))

	// The node-local service an untrusted workload would go after. It binds the
	// docker bridge gateway rather than loopback: a container has its own
	// netns, so 127.0.0.1 there is its own, and the address that actually
	// reaches host services is the gateway one — which is also what the policy
	// denies (RFC1918).
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	require.NoError(t, err)
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)

	gateway := restrictedGateway(t, backend)

	script := fmt.Sprintf(`
nc -w 3 %s %s </dev/null >/dev/null 2>&1 && echo LOCAL_REACHED || echo LOCAL_BLOCKED
nc -w 5 1.1.1.1 443 </dev/null >/dev/null 2>&1 && echo PUBLIC_REACHED || echo PUBLIC_BLOCKED
nslookup example.com >/dev/null 2>&1 && echo DNS_OK || echo DNS_FAIL
`, gateway, port)

	t.Cleanup(func() {
		if err := netguard.Uninstall(); err != nil {
			t.Logf("removing the egress table: %v", err)
		}
	})

	run := func(restrict bool) string {
		t.Helper()

		config := &core.ContainerConfig{
			Image:   conformanceImage,
			Command: []string{"/bin/sh", "-c", script},
		}
		if restrict {
			config.Network = &core.NetworkConfig{RestrictEgress: true}
		}
		// Unrestricted containers get docker's default bridge — the point of the
		// comparison: tau's rule matches its own bridge, so the rest of the host
		// keeps working.

		id, err := backend.Create(ctx, config)
		require.NoError(t, err, "create (restricted=%v) must succeed", restrict)
		defer backend.Remove(ctx, id)

		require.NoError(t, backend.Start(ctx, id))
		require.NoError(t, backend.Wait(ctx, id))

		logs, err := backend.Logs(ctx, id)
		require.NoError(t, err)
		defer logs.Close()

		var stdout, stderr bytes.Buffer
		_, err = stdcopy.StdCopy(&stdout, &stderr, logs)
		require.NoError(t, err)
		return stdout.String()
	}

	open := run(false)
	t.Logf("unrestricted baseline: %q", open)
	require.Contains(t, open, "LOCAL_REACHED",
		"the canary must be reachable with no firewall, otherwise a later block proves nothing (got %q)", open)
	require.Contains(t, open, "DNS_OK",
		"this host cannot resolve names even unrestricted, so the DNS assertion below would be meaningless (got %q)", open)

	closed := run(true)
	assert.Contains(t, closed, "LOCAL_BLOCKED",
		"a restricted container reached a node-local service (got %q)", closed)
	assert.Contains(t, closed, "PUBLIC_REACHED",
		"the firewall cut the restricted container off from the public internet (got %q)", closed)
	assert.Contains(t, closed, "DNS_OK",
		"a restricted container cannot resolve names, so it cannot fetch anything (got %q)", closed)

	// With the filter installed, a container on docker's default bridge must be
	// untouched: the rule matches tau's own bridge, so every other container on
	// the host — tau's own and the operator's — is not collateral.
	after := run(false)
	assert.Contains(t, after, "LOCAL_REACHED",
		"the firewall caught a container on the default bridge (got %q)", after)
}

// restrictedGateway returns the host address containers on tau's restricted
// network reach host services on, creating the network if this is the first run.
func restrictedGateway(t *testing.T, backend *DockerBackend) string {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, backend.ensureRestrictedNetwork(ctx))

	result, err := backend.client.NetworkInspect(ctx, restrictedNetwork, client.NetworkInspectOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, result.Network.IPAM.Config, "the restricted network must have an IPAM config")

	gateway := result.Network.IPAM.Config[0].Gateway
	require.True(t, gateway.IsValid(), "the restricted network must have a gateway address")
	return gateway.String()
}
