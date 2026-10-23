package node

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"connectrpc.com/connect"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/raft"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/build/artifact"
	"github.com/taubyte/tau/services/build/frontend"
	"github.com/taubyte/tau/services/worker"
)

// ── the one combination a node refuses ───────────────────────────────────

// THE PUBLIC HALF EXISTS SO THAT THE PROCESS A STRANGER'S POST REACHES HOLDS
// NOTHING WORTH STEALING. One node running both would put the queue's writer,
// every repository's deploy key and the register's writer in exactly that
// process, and reduce the split to a directory layout.
func TestANodeRefusesBothHalvesOfTheBuildService(t *testing.T) {
	_, err := Config{Services: []string{BuildFrontend, BuildBackend}}.withDefaults()
	require.ErrorIs(t, err, ErrBothHalves)
	require.ErrorContains(t, err, BuildFrontend)
	require.ErrorContains(t, err, BuildBackend)

	// Either one alone is fine.
	for _, only := range []string{BuildFrontend, BuildBackend} {
		_, err := Config{Services: []string{only, Discovery}}.withDefaults()
		require.NoError(t, err, only)
	}
}

// THE DEFAULT SHAPE MUST NOT BE THE REFUSED ONE, which is why the public half
// is not in All: an operator who names no services gets a development cloud,
// and a development cloud that would not start is not one.
func TestTheDefaultShapeStarts(t *testing.T) {
	require.NotContains(t, All, BuildFrontend)
	require.Contains(t, All, BuildBackend)
	require.Contains(t, All, Auth)

	cfg, err := Config{}.withDefaults()
	require.NoError(t, err)
	require.Equal(t, All, cfg.Services)
}

// The names come from the build service, not from a second spelling here: a
// rename there would otherwise leave an operator's config naming a service
// nothing starts.
func TestTheRoleNamesAreTheBuildServicesOwn(t *testing.T) {
	require.Equal(t, "build-frontend", BuildFrontend)
	require.Equal(t, "build-backend", BuildBackend)
}

// ── what each role cannot be built without ───────────────────────────────

func TestTheBackendNeedsARaft(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	_, err := New(Config{Services: []string{BuildBackend, Discovery}},
		WithHost(h), WithPubSub(newPubSub(t, h)), WithDatabases(&memFactory{}))
	require.ErrorIs(t, err, ErrNoRaft)

	// And a nil opener is refused where it is given rather than where it is
	// used, so the failure names the wiring and not the first build.
	require.ErrorIs(t, WithRaft(nil)(&options{}), ErrNoRaft)
}

// ── a node runs them, and says it does ───────────────────────────────────

func TestANodeAdvertisesTheRolesItRuns(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Discovery, Auth, BuildBackend}})
	require.NoError(t, n.Start(t.Context()))

	serving := n.serving()
	require.Contains(t, serving, Auth)
	require.Contains(t, serving, BuildBackend)
	require.NotContains(t, serving, BuildFrontend)

	require.NotNil(t, n.auth)
	require.NotNil(t, n.buildBackend)
	require.Nil(t, n.buildFrontend)
}

// THE PUBLIC HALF DIALS THE PRIVATE ONE AND AUTH, both over the swarm, so a
// node that runs only it holds neither.
func TestTheFrontendHoldsNeitherStore(t *testing.T) {
	// THE PUBLIC ROLE BINDS ITS OWN LISTENER and there is no default port for
	// it — a port baked into a service is why sibling worktrees collide — so an
	// operator names one.
	n, _, _ := makeNode(t, Config{
		Services:      []string{Discovery, BuildFrontend},
		BuildFrontend: frontend.Config{Listen: "127.0.0.1:0"},
	})
	require.NoError(t, n.Start(t.Context()))

	require.NotNil(t, n.buildFrontend)
	require.Nil(t, n.auth, "the public role does not hold auth's store")
	require.Nil(t, n.buildBackend, "nor the queue's writer")
	require.NotNil(t, n.buildFrontend.Handler(), "and it is a handler somebody serves")
	require.NotNil(t, n.BuildFrontendAddr(), "listening where a CLI can reach it")
}

func TestANodeWithNoFrontendHasNoFrontendAddress(t *testing.T) {
	require.Nil(t, (&Node{}).BuildFrontendAddr())
}

// ── finding who runs what ────────────────────────────────────────────────

// THERE IS ONE ANSWER TO "WHO DOES THIS" and it is the view every other locator
// on this node reads.
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
	require.Equal(t, holder.opts.host.ID(), peers[0])

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

// A service nobody runs is a wait that ends with the caller's deadline and says
// what it was looking for.
func TestTheResolverGivesUp(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Discovery}})
	require.NoError(t, n.Start(t.Context()))

	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()

	_, err := (&fromDiscovery{node: n, name: BuildBackend}).Any(ctx)
	require.ErrorContains(t, err, BuildBackend)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// A node with no discovery can locate nothing, and says so rather than blocking
// until a deadline that will never produce an answer.
func TestTheResolverOnANodeWithNoStats(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register}})

	from := &fromDiscovery{node: n, name: Auth}
	require.Empty(t, from.Known())

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	_, err := from.Any(ctx)
	require.Error(t, err)
}

// A row whose peer does not decode is skipped rather than failing the lookup,
// so one bad row cannot hide the good ones beside it.
func TestARowThatIsNotAPeerIsSkipped(t *testing.T) {
	require.Equal(t, []libp2ppeer.ID(nil), (&fromDiscovery{node: &Node{}, name: Auth}).Known())
}

// ── the register's one writer ────────────────────────────────────────────

// A DEPLOY IS THE ONLY THING IN THIS COMPOSITION ALLOWED TO PUBLISH, and on a
// node running its own register that is a local call rather than a stream to
// itself.
func TestThePublisherIsLocalWhenThisNodeHasTheRegister(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Discovery}})

	publisher, err := n.publisher(t.Context())
	require.NoError(t, err)
	require.Equal(t, n.register.Local(), publisher)

	// With no register anywhere, it fails rather than silently dropping a
	// deploy.
	none, _, _ := makeNode(t, Config{Services: []string{Discovery}})
	require.NoError(t, none.Start(t.Context()))
	_, err = none.publisher(t.Context())
	require.ErrorIs(t, err, ErrNoRegister)
}

// ── the code build's read of what is deployed ────────────────────────────

// IT RESOLVES PER CALL for the same reason the deploy's publisher does: which
// node answers is a routing question, and one held from construction would be
// whichever node this dialled at boot, forever.
func TestTheBuildRegisterReadsLocallyWhenThisNodeHasOne(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Discovery}})
	require.NoError(t, n.Start(t.Context()))

	r := buildRegister{n}

	// A project nobody published has no deployments, and asking is not an
	// error.
	got, err := r.Environments(t.Context(), "shop")
	require.NoError(t, err)
	require.Empty(t, got)

	_, err = n.register.Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1,
		Compiled: []byte(`{"object":{"id":"QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zd","name":"shop"}}`),
	})
	require.NoError(t, err)

	got, err = r.Environments(t.Context(), "shop")
	require.NoError(t, err)
	require.Equal(t, []string{"prod"}, got)

	pointer, err := r.Resolve(t.Context(), "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, uint64(1), pointer.Revision)

	compiled, err := r.Object(t.Context(), pointer.Object)
	require.NoError(t, err)
	require.Contains(t, string(compiled), "shop")
}

// A NODE WITH NO REGISTER ANYWHERE cannot read what is deployed, and every one
// of the three reads says so rather than answering emptily — an empty answer
// would look like a project that was never deployed.
func TestTheBuildRegisterWithNoRegisterAnywhere(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Discovery}})
	require.NoError(t, n.Start(t.Context()))

	r := buildRegister{n}

	_, err := r.Environments(t.Context(), "shop")
	require.ErrorIs(t, err, ErrNoRegister)

	_, err = r.Resolve(t.Context(), "shop", "prod")
	require.ErrorIs(t, err, ErrNoRegister)

	_, err = r.Object(t.Context(), "some-object")
	require.ErrorIs(t, err, ErrNoRegister)
}

// A NODE THAT CANNOT RUN A CONTAINER CANNOT BUILD CODE, and it says so on the
// push rather than starting and refusing every one. The two halves of what a
// push can be are independent: a node with no container backend still deploys
// configuration, which is most of what a small cloud does.
func TestABuildBackendWithNoContainerBackend(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Discovery, BuildBackend}})
	require.NoError(t, n.Start(t.Context()))
	require.NotNil(t, n.buildBackend, "it still runs, and still deploys configuration")
}

// A CONTAINER BACKEND NAMED AND NOT BUILDABLE IS REFUSED AT BOOT rather than at
// the first push, which is the same rule the worker's tier follows.
func TestABuildBackendWithABackendThatIsNotOne(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	_, err := New(Config{
		Services:  []string{Discovery, BuildBackend},
		Container: ContainerConfig{Backend: "not-a-backend"},
	},
		WithHost(h), WithPubSub(newPubSub(t, h)), WithDatabases(&memFactory{}),
		WithRaft(func(string) (raft.Cluster, error) { return raft.NewMockCluster(), nil }),
	)
	require.ErrorIs(t, err, ErrUnknownBackend)
}

// A node given an asset store hands it to the build backend, which is the
// difference between a job that records what it produced and one whose logs and
// artifacts live and die with the run.
func TestWithAssets(t *testing.T) {
	require.ErrorContains(t, WithAssets(nil)(&options{}), "asset store")

	store := &countingAssets{}
	n, _, _ := makeNode(t, Config{Services: []string{Discovery, BuildBackend}}, WithAssets(store))
	require.NoError(t, n.Start(t.Context()))
	require.NotNil(t, n.buildBackend)
}

// A SITE NEEDS NO DAEMON AND NO OPERATOR CHOICE: its cold start is a fetch and
// an unpack, so a node that can reach the asset store can serve one. That is
// what lets a static website run on the smallest possible cloud, which is most
// of what an imported project is.
func TestAnAssetStoreIsEnoughToServeASite(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	// No container backend, no injected runtime — and a worker still starts,
	// with both tiers that need only the asset store.
	n, err := New(Config{Services: []string{Discovery, Worker}},
		WithHost(h), WithPubSub(newPubSub(t, h)), WithDatabases(&memFactory{}),
		WithRaft(func(string) (raft.Cluster, error) { return raft.NewMockCluster(), nil }),
		WithAssets(&countingAssets{}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })
	require.NotNil(t, n.worker)

	// Both of them: a static site and a wasm component are what the smallest
	// possible cloud runs, and neither needs a daemon.
	require.ElementsMatch(t, []string{"static", "wasm"}, tierNames(n))

	// And with neither, it refuses rather than starting a worker that can run
	// nothing.
	_, err = New(Config{Services: []string{Discovery, Worker}},
		WithHost(newHost(t, mn)), WithPubSub(newPubSub(t, h)), WithDatabases(&memFactory{}),
		WithRaft(func(string) (raft.Cluster, error) { return raft.NewMockCluster(), nil }),
	)
	require.ErrorContains(t, err, "no compute tier")
}

// countingAssets is both halves, because this node is both: the build backend
// puts and the static tier gets.
type countingAssets struct {
	puts int
	gets int
}

func (c *countingAssets) Put(context.Context, io.Reader) (string, error) {
	c.puts++
	return "bafy", nil
}

func (c *countingAssets) Get(context.Context, string) (io.ReadSeekCloser, error) {
	c.gets++
	return nil, errors.New("nothing stored")
}

// A CONTAINER BACKEND IS BUILT ONCE AND SHARED, because the tier that runs
// workloads and the engine that runs builds must be the same daemon — a build
// that auto-detected its own would run on one nobody configured while the
// workloads ran on the one they did.
func TestTheContainerBackendIsTheOperatorsChoice(t *testing.T) {
	_, err := containerBackend(ContainerConfig{Backend: "not-a-backend"})
	require.ErrorIs(t, err, ErrUnknownBackend)
	require.ErrorContains(t, err, BackendDocker)
	require.ErrorContains(t, err, BackendContainerd)

	// A NAMED ONE IS BUILT RATHER THAN DETECTED, and the two backends differ in
	// when they find out whether the daemon is there: docker builds a client
	// and asks later, containerd checks its socket now. Either way the answer
	// arrives at BOOT rather than at the first push, which is this
	// composition's rule.
	_, err = containerBackend(ContainerConfig{Backend: BackendDocker})
	require.NoError(t, err)

	_, err = containerBackend(ContainerConfig{Backend: BackendContainerd, Socket: "/nowhere/containerd.sock"})
	if err != nil {
		require.ErrorContains(t, err, "containerd",
			"the message names the socket it looked for, so an operator sees which path was tried")
	}
}

// ── what a built resource runs ───────────────────────────────────────────

// THE ANSWER IS CACHED, briefly, because a worker resolves it per invocation
// and the row only moves when a build finishes. The TTL is the deploy
// propagation budget — how long a node may keep running the previous artifact —
// which is the same thing the register's pointer TTL is.
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

// A FAILED LOOKUP IS NOT CACHED. "Nothing has built this yet" ends the moment a
// build finishes, and remembering it would keep a node refusing a resource for
// a whole TTL after it became runnable.
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

// A CONSUMER READING THE RAW TTL GETS ZERO whenever an operator left it unset,
// and zero means "ask every time" — the opposite of what an unset field asks
// for. It is the same shape that made every register in the fleet look absent.
func TestTheDeployBudgetHasAnEffectiveValue(t *testing.T) {
	require.Equal(t, worker.DefaultPointerTTL, worker.Config{}.Pointer())
	require.Equal(t, worker.DefaultPointerTTL, worker.Config{PointerTTL: -1}.Pointer())
	require.Equal(t, time.Minute, worker.Config{PointerTTL: time.Minute}.Pointer())
}

// A NODE WIRES THE BUILD SERVICE AS ITS ARTIFACT RESOLVER, unless a composer
// injected one — an injected one wins because it is the more specific
// statement.
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
func tierNames(n *Node) []string {
	var out []string
	for _, rt := range n.tiers() {
		out = append(out, rt.Name())
	}
	return out
}

// AN INJECTED TIER WINS OVER A BUILT ONE, by name. Somebody who handed this
// node a wasm runtime meant that one — and building a second under the same
// name is not a choice between them, it is a pool that refuses to open.
func TestAnInjectedTierWins(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	n, err := New(Config{Services: []string{Discovery, Worker}},
		WithHost(h), WithPubSub(newPubSub(t, h)), WithDatabases(&memFactory{}),
		WithRaft(func(string) (raft.Cluster, error) { return raft.NewMockCluster(), nil }),
		WithAssets(&countingAssets{}),
		WithRuntimes(&tier{name: "wasm"}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })

	// One wasm, and it is the injected one; static is still built, because
	// nothing else answers for it.
	require.ElementsMatch(t, []string{"wasm", "static"}, tierNames(n))
	require.Same(t, n.opts.runtimes[0], n.tiers()[0])
}

// A FUNCTION'S println GOES SOMEWHERE. A logger takes a message and a writer
// takes bytes, and a guest writes whenever its buffer flushes — so without
// line assembly one println arrives as three log entries and two printlns as
// one.
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

// BOTH STREAMS GO TO THE SAME PLACE and the level is the difference, so an
// operator filtering on level sees a guest saying something went wrong.
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

// TestAWorkerAsksTheBuildBackendOnAnotherNode. The lookup is the same rule the
// register and the vault keep — run it here, use it directly; otherwise ask the
// view who does and dial that one — and here it crosses the wire: the worker
// node runs no build service at all.
func TestAWorkerAsksTheBuildBackendOnAnotherNode(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register, Discovery, BuildBackend}},
		Config{Services: []string{Worker, Discovery}},
	)
	compute := nodes[1]

	sees(t, compute, BuildBackend)

	a := &artifacts{node: compute, seen: map[string]builtAt{}}

	// NOTHING HAS BUILT THIS, and the answer comes from the backend rather than
	// from the view — which is the difference between "no build service is
	// reachable" and "the build service says there is no artifact".
	_, err := a.Artifact(t.Context(), "shop", "prod", "function", "checkout")
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNoBuildBackend, "a backend answered; it just had nothing")

	// AND THE FAILURE IS NOT CACHED. "Nothing has built this yet" ends the
	// moment a build finishes.
	require.Empty(t, a.seen)
}

// WHERE A PROJECT RUNS IS THE COMPOSER'S ANSWER: a node given Regions hands them to
// its deploys, and a nil one is refused rather than read as "the default".
func TestWithRegions(t *testing.T) {
	var o options
	require.Error(t, WithRegions(nil)(&o))
	require.NoError(t, WithRegions(schema.WithNoRegions())(&o))
	require.Equal(t, schema.WithNoRegions(), o.regions)
}

// WHICH VIEW A NODE READS: its destination's, DefaultRegion unless it is told another —
// so an OSS node needs no region in its configuration to read the one view its deploy
// published.
func TestAsDestination(t *testing.T) {
	var o options
	require.Error(t, AsDestination("")(&o))
	require.Equal(t, schema.DefaultRegion, (&Node{opts: o}).destination())
	require.NoError(t, AsDestination("region-1")(&o))
	require.Equal(t, "region-1", (&Node{opts: o}).destination())
}

// A DEVELOPMENT CLOUD PUTS ITS OWN SEAMS IN FRONT OF THE NODE'S: it is handed
// the clone the node would have used — and, on a node that runs no containers,
// no engine — and what it hands back is what the backend reads and runs with.
func TestBuildSeamsAreLayeredOverTheNodesOwn(t *testing.T) {
	require.Error(t, WithBuildSeams(nil)(&options{}))
	require.Error(t, WithUploads(nil)(&options{}))

	var handed BuildSeams
	n, _, _ := makeNode(t, Config{Services: []string{Discovery, BuildBackend}}, WithBuildSeams(func(s BuildSeams) BuildSeams {
		handed = s
		s.Engine = engineFunc(func(context.Context, artifact.Spec, io.Writer) (io.ReadCloser, error) {
			return nil, errors.New("not run here")
		})
		return s
	}))
	require.NoError(t, n.Start(t.Context()))
	require.NotNil(t, n.buildBackend)
	require.NotNil(t, handed.Checkout)
	require.NotNil(t, handed.Confirm)
	require.Nil(t, handed.Engine, "this node runs no containers")
}

type engineFunc func(context.Context, artifact.Spec, io.Writer) (io.ReadCloser, error)

func (f engineFunc) Make(ctx context.Context, s artifact.Spec, log io.Writer) (io.ReadCloser, error) {
	return f(ctx, s, log)
}

// WHAT A DEPLOYMENT RUNS reads back as the object its pointer names.
func TestTheBuildRegisterReadsAPublishedObjectBack(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Discovery}})
	require.NoError(t, n.Start(t.Context()))

	compiled := []byte(`{"object":{"id":"QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zd","name":"shop"}}`)
	_, err := n.register.Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled,
	})
	require.NoError(t, err)

	got, err := buildRegister{n}.Compiled(t.Context(), "shop", "prod")
	require.NoError(t, err)
	require.JSONEq(t, string(compiled), string(got))

	_, err = buildRegister{n}.Compiled(t.Context(), "shop", "preview")
	require.Error(t, err)
}

// A NODE THAT RUNS THE BUILD BACKEND READS ITS OWN, in-process: a host does not
// dial itself, so a single-node cloud asking the swarm found only itself and
// never resolved anything. Its answer stands — here, that nothing was built.
func TestANodeReadsItsOwnBuildBackend(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Discovery, BuildBackend, Worker}})
	require.NoError(t, n.Start(t.Context()))

	_, err := n.BuildArtifacts().Artifact(t.Context(), "shop", "prod", "function", "QmNothing")
	require.ErrorIs(t, err, ErrNotBuilt)
	require.NotEqual(t, connect.CodeNotFound, connect.CodeOf(err), "a site missing its artifact is broken, not unknown")
	require.ErrorContains(t, err, "QmNothing")
}

// following is a backend's store that never leads.
type following struct{ *raft.MockCluster }

func (following) IsLeader() bool { return false }

// A FOLLOWER ASKS THE FLEET, as before: only the leader answers.
func TestAFollowerAsksTheFleetForAnArtifact(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{BuildBackend}},
		WithRaft(func(string) (raft.Cluster, error) { return following{raft.NewMockCluster()}, nil }))
	require.NoError(t, n.Start(t.Context()))

	_, err := n.BuildArtifacts().Artifact(t.Context(), "shop", "prod", "function", "QmNothing")
	require.ErrorIs(t, err, ErrNoBuildBackend)
}
