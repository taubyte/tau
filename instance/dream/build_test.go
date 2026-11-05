//go:build dreaming

package dream_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	nodeDream "github.com/taubyte/tau/instance/dream"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	storageService "github.com/taubyte/tau/services/storage"
)

// placementID is with/placement's own id, from its project.yaml.
const placementID = "QmPigwAetxyyVvHj453JLsLhrWNGV4mW9ZG9yvjd7kUmMk"

// A PUSH IS BUILT WHERE THE UNIVERSE BUILDS: by its build node when it runs one,
// else alone on a node running a runner, and either way it ends deployed in the
// register, cloned from a folder that is never written.
func TestAPushIsBuiltWhereTheUniverseBuilds(t *testing.T) {
	shared := []string{instance.Discovery, instance.Register, storageService.Descriptor.Name}
	for name, cfg := range map[string]nodeDream.Config{
		"alone on a runner": {Services: append([]string{instance.Runner}, shared...)},
		"through the build node": {Copies: 2, Configure: func(index int, c *instance.Config) {
			c.Services = append([]string{instance.Runner}, shared...)
			if index == 0 {
				c.Services = append([]string{instance.Build}, shared...)
			}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			placed := cfg.Configure
			cfg.Configure = func(index int, c *instance.Config) {
				if placed != nil {
					placed(index, c)
				}
				c.Region = "region-1"
			}
			u, err := nodeDream.Start(ctx, t.Name(), cfg)
			require.NoError(t, err)
			t.Cleanup(u.Close)

			const repository = "github.com/acme/placement"
			commit, err := u.Commit(repository, "main", projectDir("with/placement"))
			require.NoError(t, err)
			require.NoError(t, u.Link(ctx, placementID, repository))

			out, err := u.Build(ctx, nodeDream.Push{Repository: repository, Ref: "refs/heads/main", Commit: commit})
			require.NoError(t, err)
			require.Equal(t, buildv2.JobStatus_JobStatusSuccess, out.Plan.GetStatus(), "%s", out.Plan.GetReason())
			require.Len(t, out.Environments, 1)
			require.Equal(t, buildv2.JobStatus_JobStatusSuccess, out.Environments[0].GetStatus(), "%s", out.Environments[0].GetReason())
			require.Equal(t, "prod", out.Environments[0].GetEnvironment())

			_, err = nodeOf(t, u.Pids()[0]).Register().Local().Resolve(ctx, placementID, "prod")
			require.NoError(t, err, "what it built is what the register serves")
		})
	}
}

func TestAPushOfWhatTheMirrorDoesNotHoldFails(t *testing.T) {
	ctx := t.Context()
	u, err := nodeDream.Start(ctx, t.Name(), nodeDream.Config{Services: []string{instance.Runner, instance.Discovery, instance.Register, storageService.Descriptor.Name}})
	require.NoError(t, err)
	t.Cleanup(u.Close)

	const repository = "github.com/acme/placement"
	_, err = u.Commit(repository, "main", projectDir("with/placement"))
	require.NoError(t, err)
	require.NoError(t, u.Link(ctx, placementID, repository))

	out, err := u.Build(ctx, nodeDream.Push{Repository: repository, Ref: "refs/heads/main", Commit: "1111111111111111111111111111111111111111"})
	require.NoError(t, err)
	require.Equal(t, buildv2.JobStatus_JobStatusFailed, out.Plan.GetStatus())
	require.Contains(t, out.Plan.GetReason(), "does not hold")
	require.Empty(t, out.Environments)
}
