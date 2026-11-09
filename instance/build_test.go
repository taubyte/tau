package instance

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/taubyte/tau/core/service"
	authService "github.com/taubyte/tau/services/auth"

	"github.com/taubyte/tau/services/builder"
	"github.com/taubyte/tau/services/plm"
	registerService "github.com/taubyte/tau/services/register"

	"github.com/taubyte/tau/pkg/containers"

	"connectrpc.com/connect"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/services"

	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/stats"
	"github.com/taubyte/tau/services/plm/ingress"
	"github.com/taubyte/tau/services/plm/jobs"
	workerHosted "github.com/taubyte/tau/services/worker/hosted"
)

func TestANodeRunsPLMAndTheBuilderTogetherWhenItsConfigSaysSo(t *testing.T) {
	for _, shape := range [][]string{{PLM, Builder}, {PLM, Stats}, {Builder, Stats}} {
		_, err := Config{Services: shape}.withDefaults(services.Names()...)
		require.NoError(t, err, "%v", shape)
	}
}

func TestTheDefaultShapeStarts(t *testing.T) {
	require.NotContains(t, services.Default(), PLM)
	require.Contains(t, services.Names(), Builder, "the builder is a service tau ships")
	require.Contains(t, services.Default(), Auth, "auth is a service tau ships, started from the catalog")

	cfg, err := Config{}.withDefaults(services.Names()...)
	require.NoError(t, err)
	require.Equal(t, services.Default(), cfg.Services, "and every service tau ships but plm")
	require.NotContains(t, cfg.Services, PLM, "which listens where its config says, and has no default")
}

func TestTheServiceNamesArePLMAndBuilder(t *testing.T) {
	require.Equal(t, "plm", PLM)
	require.Equal(t, "builder", Builder)
}

func TestANodeAdvertisesTheRolesItRuns(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Stats, Auth, Builder}})
	require.NoError(t, n.Start(t.Context()))

	serving := reported(t, n, func(row stats.Node) bool { return slices.Contains(row.Serving, Builder) }).Serving
	require.Contains(t, serving, Auth)
	require.NotContains(t, serving, PLM)

	_, ok := service.Local[*authService.Service](n.HostOf(Auth), authService.Protocol)
	require.True(t, ok, "the node does not serve auth's protocol with the auth it runs")
	_, built := jobsOf(n)
	require.False(t, built, "a builder holds no queue")
	require.Nil(t, n.PLMAddr(), "and no build listens")
}

func TestBuildHoldsTheQueueAndNotAuthsStore(t *testing.T) {
	// PLM BINDS ITS OWN LISTENER and there is no default port for it — a port
	// baked into a service is why sibling worktrees collide — so an operator names
	// one.
	n, _, _ := makeNode(t, Config{
		Services: []string{Stats, PLM},
		Sections: map[string]any{PLM: ingress.Config{Listen: "127.0.0.1:0"}},
	})
	require.NoError(t, n.Start(t.Context()))

	_, ok := service.Local[*authService.Service](n.HostOf(PLM), authService.Protocol)
	require.False(t, ok, "plm does not hold auth's store")
	_, built := jobsOf(n)
	require.True(t, built, "it holds the queue")
	require.NotNil(t, n.PLMAddr(), "it listens where a CLI can reach it")
}

func TestANodeRunningNoBuildHasNoBuildAddress(t *testing.T) {
	require.Nil(t, (&Instance{Node: mockPeer(t)}).PLMAddr())
}

// A PEER IS FOUND BY WHAT IT SERVES, through p2p discovery, on a node that runs no
// stats service at all.
func TestTheFinderFindsAPeerByWhatItServes(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register}},
		Config{Services: []string{Secrets}},
	)
	holder, seeker := nodes[0], nodes[1]

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	from := seeker.finder.Find(registerService.Protocol)
	peers, err := from.Peers(ctx, 4)
	require.NoError(t, err)
	require.Equal(t, []libp2ppeer.ID{holder.Host().ID()}, peers)
	require.Same(t, from, seeker.finder.Find(registerService.Protocol), "one discovery per protocol")
}

func TestTheFinderGivesUp(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Secrets}})
	require.NoError(t, n.Start(t.Context()))

	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()

	_, err := n.finder.Find(registerService.Protocol).Any(ctx)
	require.Error(t, err)
}

func TestABuilderWithNoContainerDaemon(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Stats, Builder}})
	require.NoError(t, n.Start(t.Context()))
	require.Contains(t, n.Services(), Builder, "it still runs, and still deploys configuration")
}

func TestABuilderWithADaemonThatIsNotOne(t *testing.T) {
	n, _, _ := makeNode(t, Config{
		Services:  []string{Stats, Builder},
		Container: containers.Declared{Backend: "not-a-backend"},
	})
	require.ErrorIs(t, n.Start(t.Context()), containers.ErrUnknownBackend, "the node's one daemon is the build's")
}

func TestAWorkerReadsBuildsRecordsOnItsOwnNode(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register, Stats, PLM}, Sections: map[string]any{PLM: ingress.Config{Listen: "127.0.0.1:0"}}},
		Config{Services: []string{Worker, Stats}},
	)
	compute := nodes[1]

	finds(t, compute, plm.Protocol)

	// NOTHING HAS BUILT THIS, read from the records this node holds, with no plm node
	// asked.
	_, err := compute.PLMArtifacts().Artifact(t.Context(), "shop", "prod", "function", "checkout")
	require.ErrorIs(t, err, workerHosted.ErrNotBuilt)
}

func TestTheRegionANodeReadsIsTheOneItsConfigNames(t *testing.T) {
	ctx := t.Context()
	n, _, _ := makeNode(t, Config{Services: []string{Register}, Region: "region-1"})
	require.NoError(t, n.Start(ctx))
	require.Equal(t, "region-1", (&added{node: n}).Region(), "the region its services are told")

	_, err := n.register.Local().Publish(ctx, pkgregister.Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("whole"),
		Views: []pkgregister.View{{Destination: "region-1", Compiled: []byte("region-1")}, {Destination: schema.DefaultRegion, Compiled: []byte("default")}}})
	require.NoError(t, err)
	views := registerService.ViewsOn(n.HostOf(""))
	t.Cleanup(func() { views.Close() })
	p, err := views.Resolve(ctx, "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, pkgregister.IDOf([]byte("region-1")), p.Object, "and the view it reads")
}

func TestADevelopmentCloudsBuilderIsConfigured(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "uploads")
	section := builder.Config{Uploads: dir, Mirror: "file://" + t.TempDir()}
	cfg := Config{Services: []string{Stats, Builder}, Sections: map[string]any{Builder: section}}
	n, _, _ := makeNode(t, cfg)
	require.NoError(t, n.Start(t.Context()))
	require.Contains(t, n.Services(), Builder)
	require.DirExists(t, dir, "the uploads directory is the builder's to make")

	// A DIRECTORY IT CANNOT MAKE refuses the node, rather than a door that fails
	// on the first upload.
	file := filepath.Join(t.TempDir(), "a file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	section.Uploads = filepath.Join(file, "below")
	refused, _, _ := makeNode(t, Config{Services: []string{Stats, Builder}, Sections: map[string]any{Builder: section}})
	require.ErrorContains(t, refused.Start(t.Context()), "uploads")
}

func TestANodeReadsItsOwnBuild(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Stats, PLM, Worker},
		Sections: map[string]any{PLM: ingress.Config{Listen: "127.0.0.1:0"}}})
	require.NoError(t, n.Start(t.Context()))
	built, ok := jobsOf(n)
	require.True(t, ok)
	require.NoError(t, built.WaitForLeader(t.Context()))

	_, err := n.PLMArtifacts().Artifact(t.Context(), "shop", "prod", "function", "QmNothing")
	require.ErrorIs(t, err, workerHosted.ErrNotBuilt)
	require.NotEqual(t, connect.CodeNotFound, connect.CodeOf(err), "a site missing its artifact is broken, not unknown")
	require.ErrorContains(t, err, "QmNothing")
}

// jobsOf is the build jobs this node runs, if it runs build.
func jobsOf(n *Instance) (jobs.Service, bool) {
	return service.Get[jobs.Service](&added{node: n})
}
