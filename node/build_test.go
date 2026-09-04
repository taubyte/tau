package node

import (
	"context"
	"testing"
	"time"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/services/build/frontend"
	authService "github.com/taubyte/tau/services/v2/auth"
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
