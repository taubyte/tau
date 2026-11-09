//go:build dreaming

package dream_test

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	nodeDream "github.com/taubyte/tau/instance/dream"
	"github.com/taubyte/tau/p2p/rpc"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
	"github.com/taubyte/tau/services/auth/projects"
)

// TestTwoNodesRacingForOneProjectIdAgreeOnOneOwner: two auth nodes each accept a
// claim on the same project id from a different configuration repository at the
// same moment. Both writes land — they are different keys — and once replication
// settles both nodes name the same owner, the loser's configuration repository
// belongs to nobody, and a later claim is refused.
func TestTwoNodesRacingForOneProjectIdAgreeOnOneOwner(t *testing.T) {
	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Copies:   2,
		Services: []string{instance.Stats, instance.Auth},
	})
	require.NoError(t, err)
	defer u.Close()
	pids := u.Pids()
	require.Len(t, pids, 2)

	// Each node calls the other's, so every call genuinely crosses a stream.
	dial := func(i int) authv2connect.ProjectServiceClient {
		target, err := libp2ppeer.Decode(pids[i])
		require.NoError(t, err)
		c, err := rpc.NewClient(u.Peers()[1-i].Host(), target, projects.Protocol)
		require.NoError(t, err)
		t.Cleanup(c.Close)
		return authv2connect.NewProjectServiceClient(c.HTTP(), c.URL())
	}
	toA, toB := dial(0), dial(1)

	const id = "QmRacedProjectId"
	configs := []string{"github.com/a/config", "github.com/b/config"}
	errs := make(chan error, 2)
	for i, c := range []authv2connect.ProjectServiceClient{toA, toB} {
		go func() {
			_, err := c.Link(t.Context(), connect.NewRequest(&authv2.LinkRequest{
				Project: id, ConfigRepository: configs[i], Claimant: "github.com/" + configs[i],
			}))
			errs <- err
		}()
	}
	accepted := 0
	for range 2 {
		if <-errs == nil {
			accepted++
		}
	}
	require.Positive(t, accepted)

	var owner string
	require.Eventually(t, func() bool {
		var seen []string
		for _, c := range []authv2connect.ProjectServiceClient{toA, toB} {
			res, err := c.Project(t.Context(), connect.NewRequest(&authv2.ProjectRequest{Project: id}))
			if err != nil {
				return false
			}
			seen = append(seen, res.Msg.GetConfigRepository())
		}
		owner = seen[0]
		// Both claims must have replicated before agreement means anything.
		for _, c := range []authv2connect.ProjectServiceClient{toA, toB} {
			for _, cfg := range configs {
				if accepted == 2 {
					res, err := c.Repository(t.Context(), connect.NewRequest(&authv2.RepositoryRequest{Repository: cfg}))
					if (cfg == seen[0]) != (err == nil) || (err == nil && res.Msg.GetProject() != id) {
						return false
					}
				}
			}
		}
		return seen[0] == seen[1]
	}, 30*time.Second, 250*time.Millisecond, "both nodes resolve one owner")

	loser := configs[0]
	if owner == loser {
		loser = configs[1]
	}
	for _, c := range []authv2connect.ProjectServiceClient{toA, toB} {
		_, err := c.Link(t.Context(), connect.NewRequest(&authv2.LinkRequest{
			Project: id, ConfigRepository: loser, Claimant: "github.com/late",
		}))
		require.Equal(t, connect.CodeAlreadyExists, connect.CodeOf(err))
		require.Contains(t, err.Error(), "already claimed by "+owner)
	}
}
