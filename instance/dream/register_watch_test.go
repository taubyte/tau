//go:build dreaming

package dream_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	nodeDream "github.com/taubyte/tau/instance/dream"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

// TestAGatewayElsewhereFollowsANameAsItMoves is a gateway on a node with no
// register, reading two register nodes through what it keeps: a name nobody claims
// is kept as absent, and it follows the name as it is claimed and released, within a
// moment rather than a TTL, with requests arriving all along.
func TestAGatewayElsewhereFollowsANameAsItMoves(t *testing.T) {
	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Copies:   3,
		Services: []string{instance.Register, instance.Discovery, instance.Gateway, instance.Worker},
		Configure: func(index int, c *instance.Config) {
			if index == 2 {
				c.Services = []string{instance.Discovery, instance.Gateway, instance.Worker}
			}
		},
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)

	var registers []*instance.Instance
	var edge *instance.Instance
	for _, id := range u.Pids() {
		n := nodeOf(t, id)
		if n.Register() == nil {
			edge = n
		} else {
			registers = append(registers, n)
		}
	}
	require.NotNil(t, edge, "one node runs no register")
	require.Len(t, registers, 2)

	const name = "moving.example.com"
	ask := func() string {
		w := httptest.NewRecorder()
		edge.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://"+name+"/", nil))
		return w.Body.String()
	}
	unclaimed := func() bool { return strings.Contains(ask(), "no site answers on "+name) }

	// The edge finds the registers through discovery once both have reported.
	require.Eventually(t, unclaimed, time.Minute, 50*time.Millisecond, "the edge never answered for an unclaimed name: %s", ask())
	for range 50 {
		require.True(t, unclaimed(), "an unclaimed name is kept as absent")
	}

	follows := func(publish func(), want func() bool, what string) time.Duration {
		t.Helper()
		start := time.Now()
		publish()
		require.Eventually(t, want, 10*time.Second, 5*time.Millisecond, "the edge never saw the name %s", what)
		took := time.Since(start)
		t.Logf("the edge saw the name %s %s after it was published", what, took)
		return took
	}

	// Claimed on one register node, released through the other: either way the edge's
	// kept answer goes, whichever node it was kept through.
	follows(func() {
		_, err := registers[0].Register().Local().Publish(t.Context(), pkgregister.Publication{
			Project: "first", Environment: "prod", Revision: 1, Compiled: []byte("first"), Hosts: []string{name},
		})
		require.NoError(t, err)
	}, func() bool { return !unclaimed() }, "claimed")

	require.Eventually(t, func() bool {
		project, _, err := registers[1].Register().Local().ResolveHost(t.Context(), name)
		return err == nil && project == "first"
	}, 30*time.Second, 10*time.Millisecond, "the claim never reached the other register node")
	follows(func() {
		_, err := registers[1].Register().Local().Publish(t.Context(), pkgregister.Publication{
			Project: "first", Environment: "prod", Revision: 2, Compiled: []byte("first, without the name"),
		})
		require.NoError(t, err)
	}, unclaimed, "released")
}
