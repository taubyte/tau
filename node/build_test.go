package node

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/raft"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/build/frontend"
	authService "github.com/taubyte/tau/services/v2/auth"
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
		_, err := Config{Services: []string{only, Stats}}.withDefaults()
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

	_, err := New(Config{Services: []string{BuildBackend, Stats}},
		WithHost(h), WithPubSub(newPubSub(t, h)), WithDatabases(&memFactory{}))
	require.ErrorIs(t, err, ErrNoRaft)

	// And a nil opener is refused where it is given rather than where it is
	// used, so the failure names the wiring and not the first build.
	require.ErrorIs(t, WithRaft(nil)(&options{}), ErrNoRaft)
}

func TestAuthNeedsSomewhereToKeepThings(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	_, err := New(Config{Services: []string{Auth}}, WithHost(h))
	require.ErrorContains(t, err, "projects, repositories and hooks")
}

// A CLOUD THAT HAS NEVER BEEN GIVEN A KEY PAIR STILL STARTS. The alternative is
// a key baked into the binary, and then every cloud could sign for every other
// cloud's domains.
func TestAuthMintsAKeyPairWhenItHasNone(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Auth}})
	require.NotNil(t, n.auth)

	// The operator's own pair is used unchanged when there is one.
	pair, err := authService.GenerateKeyPair()
	require.NoError(t, err)
	require.NotEmpty(t, pair.Private)
	require.NotEmpty(t, pair.Public)
	require.NotEqual(t, pair.Private, pair.Public)

	given, _, _ := makeNode(t, Config{Services: []string{Auth}, Auth: authService.Config{DomainValidation: pair}})
	require.NotNil(t, given.auth)

	// Two mints are two different keys, which is exactly why the log says
	// what a minted one costs.
	second, err := authService.GenerateKeyPair()
	require.NoError(t, err)
	require.NotEqual(t, pair.Private, second.Private)
}

// ── a node runs them, and says it does ───────────────────────────────────

func TestANodeAdvertisesTheRolesItRuns(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Stats, Auth, BuildBackend}})
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
		Services:      []string{Stats, BuildFrontend},
		BuildFrontend: frontend.Config{Listen: "127.0.0.1:0"},
	})
	require.NoError(t, n.Start(t.Context()))

	require.NotNil(t, n.buildFrontend)
	require.Nil(t, n.auth, "the public role does not hold auth's store")
	require.Nil(t, n.buildBackend, "nor the queue's writer")
	require.NotNil(t, n.buildFrontend.Handler(), "and it is a handler somebody serves")
}

// ── finding who runs what ────────────────────────────────────────────────

// THERE IS ONE ANSWER TO "WHO DOES THIS" and it is the view every other locator
// on this node reads.
func TestTheResolverReadsTheSameViewAsEverythingElse(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Stats, Auth}},
		Config{Services: []string{Stats}},
	)
	holder, seeker := nodes[0], nodes[1]

	from := &fromStats{node: seeker, name: Auth}

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
	n, _, _ := makeNode(t, Config{Services: []string{Stats}})
	require.NoError(t, n.Start(t.Context()))

	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()

	_, err := (&fromStats{node: n, name: BuildBackend}).Any(ctx)
	require.ErrorContains(t, err, BuildBackend)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// A node with no stats can locate nothing, and says so rather than blocking
// until a deadline that will never produce an answer.
func TestTheResolverOnANodeWithNoStats(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register}})

	from := &fromStats{node: n, name: Auth}
	require.Empty(t, from.Known())

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	_, err := from.Any(ctx)
	require.Error(t, err)
}

// A row whose peer does not decode is skipped rather than failing the lookup,
// so one bad row cannot hide the good ones beside it.
func TestARowThatIsNotAPeerIsSkipped(t *testing.T) {
	require.Equal(t, []libp2ppeer.ID(nil), (&fromStats{node: &Node{}, name: Auth}).Known())
}

// ── the register's one writer ────────────────────────────────────────────

// A DEPLOY IS THE ONLY THING IN THIS COMPOSITION ALLOWED TO PUBLISH, and on a
// node running its own register that is a local call rather than a stream to
// itself.
func TestThePublisherIsLocalWhenThisNodeHasTheRegister(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Stats}})

	publisher, err := n.publisher(t.Context())
	require.NoError(t, err)
	require.Equal(t, n.register.Local(), publisher)

	// With no register anywhere, it fails rather than silently dropping a
	// deploy.
	none, _, _ := makeNode(t, Config{Services: []string{Stats}})
	require.NoError(t, none.Start(t.Context()))
	_, err = none.publisher(t.Context())
	require.ErrorIs(t, err, ErrNoRegister)
}

// ── the code build's read of what is deployed ────────────────────────────

// IT RESOLVES PER CALL for the same reason the deploy's publisher does: which
// node answers is a routing question, and one held from construction would be
// whichever node this dialled at boot, forever.
func TestTheBuildRegisterReadsLocallyWhenThisNodeHasOne(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Stats}})
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
	n, _, _ := makeNode(t, Config{Services: []string{Stats}})
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
	n, _, _ := makeNode(t, Config{Services: []string{Stats, BuildBackend}})
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
		Services:  []string{Stats, BuildBackend},
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
	n, _, _ := makeNode(t, Config{Services: []string{Stats, BuildBackend}}, WithAssets(store))
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

	// No container backend, no injected runtime — and a worker still starts.
	n, err := New(Config{Services: []string{Stats, Worker}},
		WithHost(h), WithPubSub(newPubSub(t, h)), WithDatabases(&memFactory{}),
		WithRaft(func(string) (raft.Cluster, error) { return raft.NewMockCluster(), nil }),
		WithAssets(&countingAssets{}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })
	require.NotNil(t, n.worker)

	// And with neither, it refuses rather than starting a worker that can run
	// nothing.
	_, err = New(Config{Services: []string{Stats, Worker}},
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
		Config{Services: []string{Stats}, Worker: worker.Config{PointerTTL: time.Minute}},
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
	n, _, _ := makeNode(t, Config{Services: []string{Stats}})
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
	n, _, _ := makeNode(t, Config{Services: []string{Stats, Worker}})
	require.NotNil(t, n.worker)

	injected := artifactsFunc(func(context.Context, string, string, string, string) (string, error) {
		return "bafyinjected", nil
	})
	with, _, _ := makeNode(t, Config{Services: []string{Stats, Worker}}, WithArtifacts(injected))
	require.NotNil(t, with.worker)
}

type artifactsFunc func(ctx context.Context, project, environment, kind, resource string) (string, error)

func (f artifactsFunc) Artifact(ctx context.Context, project, environment, kind, resource string) (string, error) {
	return f(ctx, project, environment, kind, resource)
}
