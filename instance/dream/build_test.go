//go:build dreaming

package dream_test

import (
	"net/http"
	"path/filepath"
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
		// The register and storage on the build node alone, so the one the runner
		// publishes to is the one read.
		"through the build node": {Copies: 2, Configure: func(index int, c *instance.Config) {
			c.Services = []string{instance.Runner, instance.Discovery}
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

// AN UPLOADED BUILD IS WHAT RUNS: the tree and its product go through the upload
// door, the build records the product live, and the worker reads it from the
// records to answer the request.
func TestAnUploadedBuildIsWhatTheFunctionRuns(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)
	ctx := t.Context()
	u, err := nodeDream.Start(ctx, t.Name(), nodeDream.Config{Services: []string{
		instance.Runner, instance.Register, instance.Worker, instance.Discovery, instance.Gateway,
		instance.Database, instance.Primitives, storageService.Descriptor.Name,
	}})
	require.NoError(t, err)
	t.Cleanup(u.Close)

	const repository = "github.com/acme/wasm"
	push, err := u.Upload(repository, "main", projectDir("with/function/wasm"), map[string]string{
		"hello_code": filepath.Join("..", "..", "pkg", "invoke", "wasm", "fixtures", "handler.wasm"),
	})
	require.NoError(t, err)
	require.NoError(t, u.Link(ctx, "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR", repository))
	out, err := u.Build(ctx, push)
	require.NoError(t, err)
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, out.Plan.GetStatus(), "%s", out.Plan.GetReason())
	require.Len(t, out.Environments, 1)
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, out.Environments[0].GetStatus(), "%s", out.Environments[0].GetReason())

	r := &running{node: nodeOf(t, u.Pids()[0]), host: "hello.example.com"}
	got := r.ask(t, http.MethodGet, "/")
	require.Equal(t, 200, got.Code, "%s", got.Body.String())
	require.Equal(t, "/", got.Header().Get("x-path"), "the handler built, answering")
}

func TestAnUploadNamingAPackageTheTreeDoesNotBuildIsRefused(t *testing.T) {
	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{Services: []string{instance.Runner, instance.Discovery}})
	require.NoError(t, err)
	t.Cleanup(u.Close)
	_, err = u.Upload("github.com/acme/wasm", "main", projectDir("with/function/wasm"), map[string]string{"nobody": "x"})
	require.ErrorContains(t, err, "builds no package/nobody")
	_, err = u.Upload("github.com/acme/wasm", "main", t.TempDir(), nil)
	require.Error(t, err, "a tree with no project")
}
