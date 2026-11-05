//go:build dreaming

package dream_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	nodeDream "github.com/taubyte/tau/instance/dream"
	"github.com/taubyte/tau/pkg/proto/codec"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	dreamv2 "github.com/taubyte/tau/pkg/proto/gen/tau/dream/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/dream/v2/dreamv2connect"
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

// A TOOL OUTSIDE THE PROCESS BUILDS AS A TEST DOES: DreamService is the universe's
// own Commit, Link, Upload and Build, over the wire.
func TestAUniverseBuildsForAToolOverDreamService(t *testing.T) {
	ctx := t.Context()
	u, err := nodeDream.Start(ctx, t.Name(), nodeDream.Config{
		Services:  []string{instance.Runner, instance.Discovery, instance.Register, storageService.Descriptor.Name},
		Configure: func(_ int, c *instance.Config) { c.Region = "region-1" },
		Listen:    "127.0.0.1:0",
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)
	require.NotNil(t, u.Addr())
	dream := dreamv2connect.NewDreamServiceClient(http.DefaultClient, "http://"+u.Addr().String(), codec.Option())

	const repository = "github.com/acme/placement"
	dir, err := filepath.Abs(projectDir("with/placement"))
	require.NoError(t, err)
	_, err = dream.Build(ctx, connect.NewRequest(&dreamv2.Push{Repository: repository, Ref: "refs/heads/main", Commit: "1111111111111111111111111111111111111111"}))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "a repository no project links is the build's own refusal: %v", err)
	require.ErrorContains(t, err, "not registered with this cloud")

	pushed, err := dream.Commit(ctx, connect.NewRequest(&dreamv2.CommitRequest{Repository: repository, Branch: "main", Dir: dir}))
	require.NoError(t, err)
	_, err = dream.Link(ctx, connect.NewRequest(&dreamv2.LinkRequest{Project: placementID, Config: repository}))
	require.NoError(t, err)
	out, err := dream.Build(ctx, connect.NewRequest(pushed.Msg))
	require.NoError(t, err)
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, out.Msg.GetPlan().GetStatus(), "%s", out.Msg.GetPlan().GetReason())
	require.Len(t, out.Msg.GetEnvironments(), 1)

	_, err = dream.Upload(ctx, connect.NewRequest(&dreamv2.UploadRequest{Repository: repository, Branch: "main", Dir: dir, Built: map[string]string{"nobody": "x"}}))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	uploaded, err := dream.Upload(ctx, connect.NewRequest(&dreamv2.UploadRequest{Repository: repository, Branch: "main", Dir: dir}))
	require.NoError(t, err)
	require.Equal(t, "refs/heads/main", uploaded.Msg.GetRef())
	_, err = dream.Commit(ctx, connect.NewRequest(&dreamv2.CommitRequest{Repository: "not a key", Branch: "main", Dir: dir}))
	require.Error(t, err)
	_, err = dream.Link(ctx, connect.NewRequest(&dreamv2.LinkRequest{Project: placementID}))
	require.Error(t, err, "a link names the repository it is")
}

func TestAUniverseThatCannotListenDoesNotStart(t *testing.T) {
	_, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{Services: []string{instance.Discovery}, Listen: "nowhere:at:all"})
	require.ErrorContains(t, err, "DreamService")

	u, err := nodeDream.Start(t.Context(), t.Name()+"-quiet", nodeDream.Config{Services: []string{instance.Discovery}})
	require.NoError(t, err)
	t.Cleanup(u.Close)
	require.Nil(t, u.Addr(), "a universe named no Listen answers nowhere")
}

func TestACommitIsTheFolderAsItIsNow(t *testing.T) {
	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{Services: []string{instance.Discovery}})
	require.NoError(t, err)
	t.Cleanup(u.Close)
	require.Equal(t, strings.ToLower(t.Name()), u.Name(), "the cloud a universe is, named as a cloud is")
	require.NotNil(t, u.CA())

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "a", ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a", "one"), []byte("1"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a", ".git", "HEAD"), []byte("not the tree"), 0o644))
	first, err := u.Commit("github.com/acme/app", "main", dir)
	require.NoError(t, err)

	require.NoError(t, os.RemoveAll(filepath.Join(dir, "a")))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "two"), []byte("2"), 0o644))
	second, err := u.Commit("github.com/acme/app", "main", dir)
	require.NoError(t, err)
	require.NotEqual(t, first, second)

	held := filepath.Join(u.Mirror(), "github.com", "acme", "app")
	_, err = os.Stat(filepath.Join(held, "a"))
	require.True(t, os.IsNotExist(err), "what the folder no longer holds, the commit does not")
	got, err := os.ReadFile(filepath.Join(held, "two"))
	require.NoError(t, err)
	require.Equal(t, "2", string(got))

	_, err = u.Commit("github.com/acme/app", "main", filepath.Join(dir, "gone"))
	require.Error(t, err, "a folder that is not there")
	_, err = u.Commit("not a key", "main", dir)
	require.ErrorContains(t, err, "is not a repository")

	require.NoError(t, os.WriteFile(filepath.Join(u.Mirror(), "github.com", "acme", "file"), nil, 0o644))
	_, err = u.Commit("github.com/acme/file", "main", dir)
	require.ErrorContains(t, err, "the mirror's github.com/acme/file", "a mirror holding a file where a repository goes")
}

func TestAUniverseWithNothingThatBuildsSaysSo(t *testing.T) {
	ctx := t.Context()
	u, err := nodeDream.Start(ctx, t.Name(), nodeDream.Config{Services: []string{instance.Discovery}})
	require.NoError(t, err)
	t.Cleanup(u.Close)

	_, err = u.Build(ctx, nodeDream.Push{Repository: "github.com/acme/app", Ref: "refs/heads/main", Commit: "1111111111111111111111111111111111111111"})
	require.ErrorContains(t, err, "no node runs a build or a runner")
	require.ErrorContains(t, u.Link(ctx, "QmProject", "github.com/acme/app"), "no node runs the register")
	_, err = u.Upload("github.com/acme/code", "main", t.TempDir(), nil)
	require.ErrorContains(t, err, "no node runs the register", "a code repository's recipes are what its project publishes")
}

func TestACodeRepositoryNoProjectLinksIsNotUploaded(t *testing.T) {
	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{Services: []string{instance.Discovery, instance.Register}})
	require.NoError(t, err)
	t.Cleanup(u.Close)
	_, err = u.Upload("github.com/acme/code", "main", t.TempDir(), nil)
	require.ErrorContains(t, err, "is no project's")
}

func TestAFolderThatCannotBeReadIsNeitherCommittedNorUploaded(t *testing.T) {
	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{Services: []string{instance.Discovery}})
	require.NoError(t, err)
	t.Cleanup(u.Close)

	dir := t.TempDir()
	require.NoError(t, os.CopyFS(dir, os.DirFS(projectDir("with/placement"))))
	require.NoError(t, os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, "dangling")))
	_, err = u.Commit("github.com/acme/placement", "main", dir)
	require.ErrorContains(t, err, "dangling", "a file the folder names and cannot give")
	_, err = u.Upload("github.com/acme/placement", "main", dir, nil)
	require.ErrorContains(t, err, "dangling")
}
