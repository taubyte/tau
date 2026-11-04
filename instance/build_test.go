package instance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/taubyte/tau/services/build/runner"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/pkg/containers"

	"connectrpc.com/connect"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/services"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/raft"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/build/backend"
	"github.com/taubyte/tau/services/build/frontend"
	"github.com/taubyte/tau/services/worker"
)

func TestANodeRefusesBothHalvesOfTheBuildService(t *testing.T) {
	_, err := Config{Services: []string{Build, Runner}}.withDefaults(services.Names()...)
	require.ErrorIs(t, err, ErrBuildAndRunner)
	require.ErrorContains(t, err, Build)
	require.ErrorContains(t, err, Runner)

	// Either one alone is fine.
	for _, only := range []string{Build, Runner} {
		_, err := Config{Services: []string{only, Discovery}}.withDefaults(services.Names()...)
		require.NoError(t, err, only)
	}
}

func TestTheDefaultShapeStarts(t *testing.T) {
	require.NotContains(t, All, Build)
	require.Contains(t, services.Names(), Runner, "the runner is a service tau ships")
	require.Contains(t, All, Auth)

	cfg, err := Config{}.withDefaults(services.Names()...)
	require.NoError(t, err)
	require.Equal(t, append(slices.Clone(All), services.Default()...), cfg.Services, "and every service tau ships but build")
	require.NotContains(t, cfg.Services, Build, "which may not run beside the runner")
}

func TestTheRoleNamesAreTheBuildServicesOwn(t *testing.T) {
	require.Equal(t, "build", Build)
	require.Equal(t, "runner", Runner)
}

func TestANodeAdvertisesTheRolesItRuns(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Discovery, Auth, Runner}})
	require.NoError(t, n.Start(t.Context()))

	serving := n.serving()
	require.Contains(t, serving, Auth)
	require.Contains(t, serving, Runner)
	require.NotContains(t, serving, Build)

	require.NotNil(t, n.auth)
	_, built := backendOf(n)
	require.False(t, built, "a runner holds no queue")
	require.Nil(t, n.BuildAddr(), "and no build listens")
}

func TestBuildHoldsTheQueueAndNotAuthsStore(t *testing.T) {
	// BUILD BINDS ITS OWN LISTENER and there is no default port for it — a port
	// baked into a service is why sibling worktrees collide — so an operator names
	// one.
	n, _, _ := makeNode(t, Config{
		Services: []string{Discovery, Build},
		Sections: map[string]any{Build: frontend.Config{Listen: "127.0.0.1:0"}},
	})
	require.NoError(t, n.Start(t.Context()))

	require.Nil(t, n.auth, "build does not hold auth's store")
	_, built := backendOf(n)
	require.True(t, built, "it holds the queue")
	require.NotNil(t, n.BuildAddr(), "it listens where a CLI can reach it")
}

func TestANodeRunningNoBuildHasNoBuildAddress(t *testing.T) {
	require.Nil(t, (&Instance{Node: mockPeer(t)}).BuildAddr())
}

func TestTheResolverReadsTheSameViewAsEverythingElse(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Discovery, Auth}},
		Config{Services: []string{Discovery}},
	)
	holder, seeker := nodes[0], nodes[1]

	from := &fromDiscovery{node: seeker, name: Auth}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	peers, err := from.Peers(ctx, 4)
	require.NoError(t, err)
	require.Len(t, peers, 1)
	require.Equal(t, holder.Host().ID(), peers[0])

	one, err := from.Any(ctx)
	require.NoError(t, err)
	require.Equal(t, peers[0], one)

	require.Equal(t, peers, from.Known())

	// A DIAL THAT FAILED IS NOT EVIDENCE THE NODE STOPPED RUNNING THE SERVICE.
	// Dropping a peer here would let one bad connection remove the only node
	// running a service from every lookup after it.
	from.Failed(peers[0])
	require.Equal(t, peers, from.Known(), "the view decides who runs what, not a failed dial")
}

func TestTheResolverGivesUp(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Discovery}})
	require.NoError(t, n.Start(t.Context()))

	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()

	_, err := (&fromDiscovery{node: n, name: Runner}).Any(ctx)
	require.ErrorContains(t, err, Runner)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestTheResolverOnANodeWithNoStats(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register}})

	from := &fromDiscovery{node: n, name: Auth}
	require.Empty(t, from.Known())

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	_, err := from.Any(ctx)
	require.Error(t, err)
}

func TestARowThatIsNotAPeerIsSkipped(t *testing.T) {
	require.Equal(t, []libp2ppeer.ID(nil), (&fromDiscovery{node: &Instance{Node: mockPeer(t)}, name: Auth}).Known())
}

func TestARunnerWithNoContainerDaemon(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Discovery, Runner}})
	require.NoError(t, n.Start(t.Context()))
	require.Contains(t, n.serving(), Runner, "it still runs, and still deploys configuration")
}

func TestARunnerWithADaemonThatIsNotOne(t *testing.T) {
	n, _, _ := makeNode(t, Config{
		Services:  []string{Discovery, Runner},
		Container: containers.Declared{Backend: "not-a-backend"},
	})
	require.ErrorIs(t, n.Start(t.Context()), containers.ErrUnknownBackend, "the node's one daemon is the build's")
}

func TestAnAssetStoreIsEnoughToRunCode(t *testing.T) {
	// No container backend, no injected runtime — and a worker still starts,
	// with every tier that needs only the asset store.
	n, err := New(mockPeer(t), rooted(t, Config{Services: []string{Discovery, Worker}}), withKVDB(&memFactory{}), withRaft(func(string) (raft.Cluster, error) { return raft.NewMockCluster(), nil }))
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
		Config{Services: []string{Discovery}, Worker: worker.Config{PointerTTL: time.Minute}},
		WithClock(func() time.Time { return now }))

	a := &artifacts{node: n, seen: map[string]builtAt{}}
	a.seen["shop\x00prod\x00res"] = builtAt{cid: "bafyone", when: now}

	cid, err := a.Artifact(t.Context(), "shop", "prod", "function", "res")
	require.NoError(t, err)
	require.Equal(t, "bafyone", cid)

	// Past the budget it is asked again — and with no build backend anywhere,
	// asking waits for one until the caller gives up rather than serving
	// something stale forever.
	now = now.Add(2 * time.Minute)

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	_, err = a.Artifact(ctx, "shop", "prod", "function", "res")
	require.Error(t, err)
}

func TestAFailedArtifactLookupIsNotRemembered(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Discovery}})
	require.NoError(t, n.Start(t.Context()))

	a := &artifacts{node: n, seen: map[string]builtAt{}}

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	_, err := a.Artifact(ctx, "shop", "prod", "function", "res")
	require.Error(t, err)
	require.Empty(t, a.seen)
}

func TestTheDeployBudgetHasAnEffectiveValue(t *testing.T) {
	require.Equal(t, worker.DefaultPointerTTL, worker.Config{}.Pointer())
	require.Equal(t, worker.DefaultPointerTTL, worker.Config{PointerTTL: -1}.Pointer())
	require.Equal(t, time.Minute, worker.Config{PointerTTL: time.Minute}.Pointer())
}

func TestTheWorkerResolvesArtifactsThroughTheBuildService(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Discovery, Worker}})
	require.NotNil(t, n.worker)

	injected := artifactsFunc(func(context.Context, string, string, string, string) (string, error) {
		return "bafyinjected", nil
	})
	with, _, _ := makeNode(t, Config{Services: []string{Discovery, Worker}}, WithArtifacts(injected))
	require.NotNil(t, with.worker)
}

type artifactsFunc func(ctx context.Context, project, environment, kind, resource string) (string, error)

func (f artifactsFunc) Artifact(ctx context.Context, project, environment, kind, resource string) (string, error) {
	return f(ctx, project, environment, kind, resource)
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
	n, err := New(mockPeer(t), rooted(t, Config{Services: []string{Discovery, Worker}}), withKVDB(&memFactory{}), withRaft(func(string) (raft.Cluster, error) { return raft.NewMockCluster(), nil }), WithRuntimes(&tier{name: "wasm"}))
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

func TestAWorkerAsksBuildOnAnotherNode(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register, Discovery, Build}, Sections: map[string]any{Build: frontend.Config{Listen: "127.0.0.1:0"}}},
		Config{Services: []string{Worker, Discovery}},
	)
	compute := nodes[1]

	sees(t, compute, Build)

	a := &artifacts{node: compute, seen: map[string]builtAt{}}

	// NOTHING HAS BUILT THIS, and the answer comes from the backend rather than
	// from the view — which is the difference between "no build service is
	// reachable" and "the build service says there is no artifact".
	_, err := a.Artifact(t.Context(), "shop", "prod", "function", "checkout")
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNoBuild, "a runner answered; it just had nothing")

	// AND THE FAILURE IS NOT CACHED. "Nothing has built this yet" ends the
	// moment a build finishes.
	require.Empty(t, a.seen)
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

func TestADevelopmentCloudsRunnerIsConfigured(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "uploads")
	section := runner.Config{Uploads: dir, Local: map[string]string{"github.com/acme/app": t.TempDir()}}
	cfg := Config{Services: []string{Discovery, Runner}, Sections: map[string]any{Runner: section}}
	n, _, _ := makeNode(t, cfg)
	require.NoError(t, n.Start(t.Context()))
	require.Contains(t, n.serving(), Runner)
	require.DirExists(t, dir, "the uploads directory is the runner's to make")

	// A DIRECTORY IT CANNOT MAKE refuses the node, rather than a door that fails
	// on the first upload.
	file := filepath.Join(t.TempDir(), "a file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	section.Uploads = filepath.Join(file, "below")
	refused, _, _ := makeNode(t, Config{Services: []string{Discovery, Runner}, Sections: map[string]any{Runner: section}})
	require.ErrorContains(t, refused.Start(t.Context()), "uploads")
}

func TestANodeReadsItsOwnBuild(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Discovery, Build, Worker},
		Sections: map[string]any{Build: frontend.Config{Listen: "127.0.0.1:0"}}})
	require.NoError(t, n.Start(t.Context()))
	built, ok := backendOf(n)
	require.True(t, ok)
	require.NoError(t, built.WaitForLeader(t.Context()))

	_, err := n.BuildArtifacts().Artifact(t.Context(), "shop", "prod", "function", "QmNothing")
	require.ErrorIs(t, err, ErrNotBuilt)
	require.NotEqual(t, connect.CodeNotFound, connect.CodeOf(err), "a site missing its artifact is broken, not unknown")
	require.ErrorContains(t, err, "QmNothing")
}

// following is a backend beside the worker that does not lead its queue.
type following struct{ backend.Service }

func (following) Built(string, string, string) (string, error) {
	return "", connect.NewError(connect.CodeUnavailable, errors.New("not the leader"))
}

func TestAFollowerAsksTheFleetForAnArtifact(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Discovery}})
	require.NoError(t, n.Start(t.Context()))
	n.provide(Runner, following{})

	_, err := n.BuildArtifacts().Artifact(t.Context(), "shop", "prod", "function", "QmNothing")
	require.ErrorIs(t, err, ErrNoBuild, "a follower beside it sends the question to the fleet")
}

// backendOf is the build backend this node runs, if it runs one.
func backendOf(n *Instance) (backend.Service, bool) {
	return service.Get[backend.Service](&added{node: n})
}
