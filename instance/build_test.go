package instance

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/taubyte/tau/services/builder"
	"github.com/taubyte/tau/services/plm"
	registerService "github.com/taubyte/tau/services/register"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/pkg/containers"

	"connectrpc.com/connect"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/services"

	"github.com/taubyte/tau/pkg/invoke"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/plm/ingress"
	"github.com/taubyte/tau/services/plm/jobs"
	"github.com/taubyte/tau/services/worker"
)

func TestANodeRunsPLMAndTheBuilderTogetherWhenItsConfigSaysSo(t *testing.T) {
	for _, shape := range [][]string{{PLM, Builder}, {PLM, Stats}, {Builder, Stats}} {
		_, err := Config{Services: shape}.withDefaults(services.Names()...)
		require.NoError(t, err, "%v", shape)
	}
}

func TestTheDefaultShapeStarts(t *testing.T) {
	require.NotContains(t, All, PLM)
	require.Contains(t, services.Names(), Builder, "the builder is a service tau ships")
	require.Contains(t, All, Auth)

	cfg, err := Config{}.withDefaults(services.Names()...)
	require.NoError(t, err)
	require.Equal(t, append(slices.Clone(All), services.Default()...), cfg.Services, "and every service tau ships but plm")
	require.NotContains(t, cfg.Services, PLM, "which listens where its config says, and has no default")
}

func TestTheServiceNamesArePLMAndBuilder(t *testing.T) {
	require.Equal(t, "plm", PLM)
	require.Equal(t, "builder", Builder)
}

func TestANodeAdvertisesTheRolesItRuns(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Stats, Auth, Builder}})
	require.NoError(t, n.Start(t.Context()))

	serving := n.serving()
	require.Contains(t, serving, Auth)
	require.Contains(t, serving, Builder)
	require.NotContains(t, serving, PLM)

	require.NotNil(t, n.auth)
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

	require.Nil(t, n.auth, "plm does not hold auth's store")
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
	require.Contains(t, n.serving(), Builder, "it still runs, and still deploys configuration")
}

func TestABuilderWithADaemonThatIsNotOne(t *testing.T) {
	n, _, _ := makeNode(t, Config{
		Services:  []string{Stats, Builder},
		Container: containers.Declared{Backend: "not-a-backend"},
	})
	require.ErrorIs(t, n.Start(t.Context()), containers.ErrUnknownBackend, "the node's one daemon is the build's")
}

func TestAnAssetStoreIsEnoughToRunCode(t *testing.T) {
	// No container backend, no injected runtime — and a worker still starts,
	// with every tier that needs only the asset store.
	n, err := New(mockPeer(t), rooted(t, Config{Services: []string{Stats, Worker}}), withKVDB(&memFactory{}), withRaft(mockGroups{}))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })
	require.NotNil(t, n.worker)

	// Both: a wasm component and a js function are what the smallest possible cloud
	// runs, and neither needs a daemon. A site's files are its gateway's to serve.
	require.ElementsMatch(t, []string{"wasm", "warp"}, tierNames(n))
}

func TestTheArtifactLookupIsCachedForTheDeployBudget(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	n, _, _ := makeNode(t,
		Config{Services: []string{Stats}, Worker: worker.Config{PointerTTL: time.Minute}},
		WithClock(func() time.Time { return now }))

	a := &artifacts{node: n}
	a.remember(builtKey{project: "shop", environment: "prod", resource: "res"}, "bafyone")

	cid, err := a.Artifact(t.Context(), "shop", "prod", "function", "res")
	require.NoError(t, err)
	require.Equal(t, "bafyone", cid)

	// Past the budget the records are read again, and they hold nothing for it,
	// rather than something stale being served forever.
	now = now.Add(2 * time.Minute)
	_, err = a.Artifact(t.Context(), "shop", "prod", "function", "res")
	require.ErrorIs(t, err, ErrNotBuilt)
}

func TestAFailedArtifactLookupIsNotRemembered(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Stats}})
	require.NoError(t, n.Start(t.Context()))

	a := &artifacts{node: n}
	_, err := a.Artifact(t.Context(), "shop", "prod", "function", "res")
	require.ErrorIs(t, err, ErrNotBuilt)
	_, held := a.seen.Load(builtKey{project: "shop", environment: "prod", resource: "res"})
	require.False(t, held)
}

func TestTheDeployBudgetHasAnEffectiveValue(t *testing.T) {
	require.Equal(t, worker.DefaultPointerTTL, worker.Config{}.Pointer())
	require.Equal(t, worker.DefaultPointerTTL, worker.Config{PointerTTL: -1}.Pointer())
	require.Equal(t, time.Minute, worker.Config{PointerTTL: time.Minute}.Pointer())
}

func TestTheWorkerResolvesArtifactsThroughBuildsRecords(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Stats, Worker}})
	require.NotNil(t, n.worker)

	with, _, _ := makeNode(t, Config{Services: []string{Stats, Worker}}, WithArtifacts(everythingBuilt{}))
	require.NotNil(t, with.worker)

	_, err := New(mockPeer(t), rooted(t, Config{Services: []string{Stats, Worker}}), WithArtifacts(nil))
	require.Error(t, err)
}

// tierNames is what this node's pool can run.
func tierNames(n *Instance) []string {
	var out []string
	for _, rt := range n.tiers() {
		out = append(out, rt.Name())
	}
	return out
}

func TestAnInjectedTierWins(t *testing.T) {
	n, err := New(mockPeer(t), rooted(t, Config{Services: []string{Stats, Worker}}), withKVDB(&memFactory{}), withRaft(mockGroups{}), WithRuntimes(&tier{name: "wasm"}))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })

	// One wasm, and it is the injected one; js is still built, because nothing
	// else answers for it.
	require.ElementsMatch(t, []string{"wasm", "warp"}, tierNames(n))
	require.Same(t, n.opts.runtimes[0], n.tiers()[0])
}

func TestAWorkloadsOutputIsAssembledIntoLines(t *testing.T) {
	var lines []string
	w := logLine(func(line string) { lines = append(lines, line) })

	// One line arriving in three writes is one line.
	for _, part := range []string{"star", "ting the ", "handler\n"} {
		n, err := w.Write([]byte(part))
		require.NoError(t, err)
		require.Equal(t, len(part), n, "a writer reports what it took, not what it emitted")
	}
	require.Equal(t, []string{"starting the handler"}, lines)

	// Two lines in one write are two.
	lines = nil
	_, err := w.Write([]byte("one\ntwo\n"))
	require.NoError(t, err)
	require.Equal(t, []string{"one", "two"}, lines)

	// A carriage return is the line ending's, not the message's.
	lines = nil
	_, err = w.Write([]byte("windows\r\n"))
	require.NoError(t, err)
	require.Equal(t, []string{"windows"}, lines)

	// A blank line is not a log entry, and an unterminated one waits rather
	// than being emitted half-written.
	lines = nil
	_, err = w.Write([]byte("\n\nstill typ"))
	require.NoError(t, err)
	require.Empty(t, lines)

	_, err = w.Write([]byte("ing\n"))
	require.NoError(t, err)
	require.Equal(t, []string{"still typing"}, lines)
}

func TestAWorkloadsStreamsAreNamedByTheWorkload(t *testing.T) {
	stdout, stderr := workloadLog(invoke.Spec{Name: "shop/prod/function/checkout"})
	require.NotNil(t, stdout)
	require.NotNil(t, stderr)
	require.NotSame(t, stdout, stderr)

	// Writing to either is safe and reports what it took.
	n, err := stdout.Write([]byte("hello\n"))
	require.NoError(t, err)
	require.Equal(t, 6, n)

	n, err = stderr.Write([]byte("something broke\n"))
	require.NoError(t, err)
	require.Equal(t, 16, n)
}

func TestAWorkerReadsBuildsRecordsOnItsOwnNode(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register, Stats, PLM}, Sections: map[string]any{PLM: ingress.Config{Listen: "127.0.0.1:0"}}},
		Config{Services: []string{Worker, Stats}},
	)
	compute := nodes[1]

	finds(t, compute, plm.Protocol)

	a := &artifacts{node: compute}

	// NOTHING HAS BUILT THIS, read from the records this node holds, with no
	// plm node asked.
	_, err := a.Artifact(t.Context(), "shop", "prod", "function", "checkout")
	require.ErrorIs(t, err, ErrNotBuilt)

	// AND THE FAILURE IS NOT CACHED. "Nothing has built this yet" ends the
	// moment a build finishes.
	_, held := a.seen.Load(builtKey{project: "shop", environment: "prod", resource: "checkout"})
	require.False(t, held)
}

func TestTheRegionANodeReadsIsTheOneItsConfigNames(t *testing.T) {
	ctx := t.Context()
	n, _, _ := makeNode(t, Config{Services: []string{Register}, Region: "region-1"})
	require.NoError(t, n.Start(ctx))
	require.Equal(t, "region-1", (&added{node: n}).Region(), "the region its services are told")

	_, err := n.register.Local().Publish(ctx, pkgregister.Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("whole"),
		Views: []pkgregister.View{{Destination: "region-1", Compiled: []byte("region-1")}, {Destination: schema.DefaultRegion, Compiled: []byte("default")}}})
	require.NoError(t, err)
	p, err := n.views.Resolve(ctx, "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, pkgregister.IDOf([]byte("region-1")), p.Object, "and the view it reads")
}

func TestADevelopmentCloudsBuilderIsConfigured(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "uploads")
	section := builder.Config{Uploads: dir, Mirror: "file://" + t.TempDir()}
	cfg := Config{Services: []string{Stats, Builder}, Sections: map[string]any{Builder: section}}
	n, _, _ := makeNode(t, cfg)
	require.NoError(t, n.Start(t.Context()))
	require.Contains(t, n.serving(), Builder)
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
	require.ErrorIs(t, err, ErrNotBuilt)
	require.NotEqual(t, connect.CodeNotFound, connect.CodeOf(err), "a site missing its artifact is broken, not unknown")
	require.ErrorContains(t, err, "QmNothing")
}

// jobsOf is the build jobs this node runs, if it runs build.
func jobsOf(n *Instance) (jobs.Service, bool) {
	return service.Get[jobs.Service](&added{node: n})
}
