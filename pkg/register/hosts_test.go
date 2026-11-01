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

func TestANameIsMatchedHoweverItWasTyped(t *testing.T) {
	r, _ := newRegister(t)
	publish(t, r, "shop", "prod", 1, "Shop.Example.COM")

	for _, typed := range []string{"shop.example.com", "SHOP.EXAMPLE.COM", " Shop.example.com "} {
		project, _, err := r.ResolveHost(ctx, typed)
		require.NoError(t, err, typed)
		require.Equal(t, "shop", project)
	}
}

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

func TestOneEnvironmentDoesNotTakeAnothersName(t *testing.T) {
	r, _ := newRegister(t)
	publish(t, r, "shop", "prod", 1, "shop.example.com")

	_, err := r.Publish(ctx, Publication{
		Project: "shop", Environment: "staging", Revision: 1,
		Compiled: []byte("x"), Hosts: []string{"shop.example.com"},
	})
	require.ErrorIs(t, err, ErrHostClaimed)
}

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

func TestPublishingNoNamesIsOrdinary(t *testing.T) {
	r, _ := newRegister(t)
	publish(t, r, "shop", "prod", 1)

	p, err := r.Resolve(ctx, "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, uint64(1), p.Revision)
}

func TestBlankNamesAreDropped(t *testing.T) {
	r, store := newRegister(t)
	publish(t, r, "shop", "prod", 1, "", "   ", "real.example.com")

	for _, k := range store.Keys() {
		require.NotEqual(t, "h/", k)
	}
	_, _, err := r.ResolveHost(ctx, "real.example.com")
	require.NoError(t, err)
}

func TestAClaimRowThatIsNotOneIsNotAnAnswer(t *testing.T) {
	r, store := newRegister(t)
	require.NoError(t, store.Put(ctx, hostKey("shop.example.com"), []byte("nonsense")))

	_, _, err := r.ResolveHost(ctx, "shop.example.com")
	require.ErrorIs(t, err, ErrMalformedPointer)
}

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
