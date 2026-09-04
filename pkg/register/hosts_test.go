package register

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func publish(t *testing.T, r *Register, project, environment string, revision uint64, hosts ...string) {
	t.Helper()
	_, err := r.Publish(ctx, Publication{
		Project: project, Environment: environment, Revision: revision,
		Compiled: []byte(project + "/" + environment), Hosts: hosts,
	})
	require.NoError(t, err)
}

// TestAGatewayAsksWhoseNameThisIs. Everything in this file exists for that one
// read: a request arrives carrying a Host header and something has to say which
// project's it is before anything else can happen.
func TestAGatewayAsksWhoseNameThisIs(t *testing.T) {
	r, _ := newRegister(t)
	publish(t, r, "shop", "prod", 1, "shop.example.com", "api.example.com")

	project, environment, err := r.ResolveHost(ctx, "shop.example.com")
	require.NoError(t, err)
	require.Equal(t, "shop", project)
	require.Equal(t, "prod", environment)

	_, _, err = r.ResolveHost(ctx, "nobody.example.com")
	require.ErrorIs(t, err, ErrNoHost)

	_, _, err = r.ResolveHost(ctx, "")
	require.ErrorIs(t, err, ErrNoHost)
}

// TestANameIsMatchedHoweverItWasTyped. DNS is case-insensitive and a Host
// header carries whatever the client typed, so a table keyed by what the author
// wrote would miss half the requests for the same name.
func TestANameIsMatchedHoweverItWasTyped(t *testing.T) {
	r, _ := newRegister(t)
	publish(t, r, "shop", "prod", 1, "Shop.Example.COM")

	for _, typed := range []string{"shop.example.com", "SHOP.EXAMPLE.COM", " Shop.example.com "} {
		project, _, err := r.ResolveHost(ctx, typed)
		require.NoError(t, err, typed)
		require.Equal(t, "shop", project)
	}
}

// TestFirstClaimWins. Two projects on one cloud can both write
// `shop.example.com` into their config, and whichever deployed second would
// otherwise take the first one's traffic — a takeover performed by an ordinary
// deploy.
func TestFirstClaimWins(t *testing.T) {
	r, _ := newRegister(t)
	publish(t, r, "shop", "prod", 1, "shop.example.com")

	_, err := r.Publish(ctx, Publication{
		Project: "impostor", Environment: "prod", Revision: 1,
		Compiled: []byte("x"), Hosts: []string{"shop.example.com"},
	})
	require.ErrorIs(t, err, ErrHostClaimed)

	// And the refusal left nothing behind: the impostor's environment is not
	// published, and the name still answers for its owner.
	_, err = r.Resolve(ctx, "impostor", "prod")
	require.ErrorIs(t, err, ErrNotPublished)

	project, _, err := r.ResolveHost(ctx, "shop.example.com")
	require.NoError(t, err)
	require.Equal(t, "shop", project)
}

// TestOneEnvironmentDoesNotTakeAnothersName, even inside one project. One name
// has one destination, and prod and staging are two.
func TestOneEnvironmentDoesNotTakeAnothersName(t *testing.T) {
	r, _ := newRegister(t)
	publish(t, r, "shop", "prod", 1, "shop.example.com")

	_, err := r.Publish(ctx, Publication{
		Project: "shop", Environment: "staging", Revision: 1,
		Compiled: []byte("x"), Hosts: []string{"shop.example.com"},
	})
	require.ErrorIs(t, err, ErrHostClaimed)
}

// TestRepublishingKeepsWhatItStillClaims and lets go of what it does not — or a
// gateway that was renamed would hold its old name forever and nobody could
// ever use it again.
func TestARepublishReleasesWhatItDropped(t *testing.T) {
	r, _ := newRegister(t)
	publish(t, r, "shop", "prod", 1, "shop.example.com", "old.example.com")
	publish(t, r, "shop", "prod", 2, "shop.example.com", "new.example.com")

	project, _, err := r.ResolveHost(ctx, "shop.example.com")
	require.NoError(t, err)
	require.Equal(t, "shop", project)

	_, _, err = r.ResolveHost(ctx, "new.example.com")
	require.NoError(t, err)

	_, _, err = r.ResolveHost(ctx, "old.example.com")
	require.ErrorIs(t, err, ErrNoHost, "the name it dropped is free")

	// Free enough that somebody else can take it, which is the point.
	publish(t, r, "blog", "prod", 1, "old.example.com")
	project, _, err = r.ResolveHost(ctx, "old.example.com")
	require.NoError(t, err)
	require.Equal(t, "blog", project)
}

// TestNoNameIsTakenIfOneIsRefused. A publish that took half the names and then
// hit one somebody else holds would leave the cloud answering for a deploy that
// did not happen.
func TestNoNameIsTakenIfOneIsRefused(t *testing.T) {
	r, _ := newRegister(t)
	publish(t, r, "shop", "prod", 1, "taken.example.com")

	_, err := r.Publish(ctx, Publication{
		Project: "blog", Environment: "prod", Revision: 1, Compiled: []byte("x"),
		Hosts: []string{"free.example.com", "taken.example.com"},
	})
	require.ErrorIs(t, err, ErrHostClaimed)

	_, _, err = r.ResolveHost(ctx, "free.example.com")
	require.ErrorIs(t, err, ErrNoHost, "nothing was claimed")
}

// TestPublishingNoNamesIsOrdinary. Most environments have none — a project of
// functions reached through somebody else's gateway, an environment nothing
// points at yet.
func TestPublishingNoNamesIsOrdinary(t *testing.T) {
	r, _ := newRegister(t)
	publish(t, r, "shop", "prod", 1)

	p, err := r.Resolve(ctx, "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, uint64(1), p.Revision)
}

// TestBlankNamesAreDropped rather than claimed. An empty Host key would be a
// row every lookup for a missing name would find.
func TestBlankNamesAreDropped(t *testing.T) {
	r, store := newRegister(t)
	publish(t, r, "shop", "prod", 1, "", "   ", "real.example.com")

	for _, k := range store.Keys() {
		require.NotEqual(t, "h/", k)
	}
	_, _, err := r.ResolveHost(ctx, "real.example.com")
	require.NoError(t, err)
}

// TestAClaimRowThatIsNotOneIsNotAnAnswer.
func TestAClaimRowThatIsNotOneIsNotAnAnswer(t *testing.T) {
	r, store := newRegister(t)
	require.NoError(t, store.Put(ctx, hostKey("shop.example.com"), []byte("nonsense")))

	_, _, err := r.ResolveHost(ctx, "shop.example.com")
	require.ErrorIs(t, err, ErrMalformedPointer)
}

// TestAnUnreachableStoreDoesNotReadAsAFreeName. Answering "nobody holds this"
// while the store is down would let the next publish take somebody else's name.
func TestAnUnreachableStoreDoesNotReadAsAFreeName(t *testing.T) {
	r, err := New(WithStore(&unreadableStore{NewMemStore()}))
	require.NoError(t, err)

	_, _, err = r.ResolveHost(ctx, "shop.example.com")
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNoHost)

	_, err = r.Publish(ctx, Publication{
		Project: "shop", Environment: "prod", Revision: 1,
		Compiled: []byte("x"), Hosts: []string{"shop.example.com"},
	})
	require.Error(t, err)
}

// TestAFailedClaimWriteMovesNothing. The names go in before the pointer, so a
// store that refused one leaves the environment on the revision it was already
// running rather than on a deploy whose names never took.
func TestAFailedClaimWriteMovesNothing(t *testing.T) {
	for _, prefix := range []string{hostPrefix, claimsPrefix} {
		store := &failingStore{MemStore: NewMemStore(), failOn: prefix}
		r, err := New(WithStore(store))
		require.NoError(t, err)

		_, err = r.Publish(ctx, Publication{
			Project: "shop", Environment: "prod", Revision: 1,
			Compiled: []byte("x"), Hosts: []string{"shop.example.com"},
		})
		require.Error(t, err, prefix)

		_, err = r.Resolve(ctx, "shop", "prod")
		require.ErrorIs(t, err, ErrNotPublished, prefix)
	}
}

// TestAFailedReleaseIsReported rather than leaving a name pointing at a deploy
// that is no longer there.
func TestAFailedReleaseIsReported(t *testing.T) {
	store := &sometimesFailingStore{MemStore: NewMemStore()}
	r, err := New(WithStore(store))
	require.NoError(t, err)

	publish(t, r, "shop", "prod", 1, "old.example.com")

	store.fail = true
	_, err = r.Publish(ctx, Publication{
		Project: "shop", Environment: "prod", Revision: 2,
		Compiled: []byte("x"), Hosts: []string{"new.example.com"},
	})
	require.Error(t, err)
}
