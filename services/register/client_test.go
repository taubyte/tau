package register

import (
	"errors"
	"testing"

	"connectrpc.com/connect"

	"github.com/stretchr/testify/require"

	pkgregister "github.com/taubyte/tau/pkg/register"
)

// TestTheClientIsTheSameRegister — every sentinel a local register returns
// comes back from a remote one, so a consumer holds one interface and one error
// path whether the register is in this process or across a stream.
func TestTheClientIsTheSameRegister(t *testing.T) {
	h := newHarness(t, pkgregister.NewMemStore())
	c := newClient(t, h)

	id, err := c.Publish(t.Context(), pkgregister.Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("memory: 64MB")})
	require.NoError(t, err)

	p, err := c.Resolve(t.Context(), "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, id, p.Object)
	require.Equal(t, uint64(1), p.Revision)

	compiled, err := c.Object(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, []byte("memory: 64MB"), compiled)
}

func TestEverySentinelSurvivesTheWire(t *testing.T) {
	h := newHarness(t, pkgregister.NewMemStore())
	c := newClient(t, h)

	_, err := c.Resolve(t.Context(), "shop", "prod")
	require.ErrorIs(t, err, pkgregister.ErrNotPublished)

	_, err = c.Object(t.Context(), pkgregister.IDOf([]byte("never published")))
	require.ErrorIs(t, err, pkgregister.ErrNoObject, "and not ErrNotPublished, which shares its status code")

	_, err = c.Publish(t.Context(), pkgregister.Publication{Project: "", Environment: "prod", Revision: 1, Compiled: []byte("x")})
	require.ErrorIs(t, err, pkgregister.ErrNoProject)

	_, err = c.Publish(t.Context(), pkgregister.Publication{Project: "shop", Environment: "", Revision: 1, Compiled: []byte("x")})
	require.ErrorIs(t, err, pkgregister.ErrNoEnvironment)

	_, err = c.Publish(t.Context(), pkgregister.Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: nil})
	require.ErrorIs(t, err, pkgregister.ErrEmptyObject)

	_, err = c.Publish(t.Context(), pkgregister.Publication{Project: "a/b", Environment: "prod", Revision: 1, Compiled: []byte("x")})
	require.ErrorIs(t, err, pkgregister.ErrSeparator)

	_, err = c.Publish(t.Context(), pkgregister.Publication{Project: "shop", Environment: "prod", Revision: 5, Compiled: []byte("x")})
	require.NoError(t, err)
	_, err = c.Publish(t.Context(), pkgregister.Publication{Project: "shop", Environment: "prod", Revision: 2, Compiled: []byte("late")})
	require.ErrorIs(t, err, pkgregister.ErrStaleRevision)
}

func TestACorruptedPointerReachesTheClientAsOne(t *testing.T) {
	store := pkgregister.NewMemStore()
	h := newHarness(t, store)
	c := newClient(t, h)

	_, err := c.Publish(t.Context(), pkgregister.Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("compiled")})
	require.NoError(t, err)

	for _, k := range store.Keys() {
		if k[0] == 'p' {
			require.NoError(t, store.Put(t.Context(), k, []byte("garbage")))
		}
	}

	_, err = c.Resolve(t.Context(), "shop", "prod")
	require.ErrorIs(t, err, pkgregister.ErrMalformedPointer)
}

// TestAStoreFailureIsNotASentinel. There is no sentinel for "the store is
// unreachable", and inventing one here would be this package deciding what a
// store failure means. It arrives as the connect error it is.
func TestAStoreFailureIsNotASentinel(t *testing.T) {
	h := newHarness(t, brokenStore{pkgregister.NewMemStore()})
	c := newClient(t, h)

	_, err := c.Resolve(t.Context(), "shop", "prod")
	require.Error(t, err)
	require.NotErrorIs(t, err, pkgregister.ErrNotPublished)
	require.NotErrorIs(t, err, pkgregister.ErrMalformedPointer)
}

func TestNewClientRefusesNonsense(t *testing.T) {
	h := newHarness(t, pkgregister.NewMemStore())
	_, err := NewClient(h.CallerHost, "")
	require.Error(t, err)
}

// TestAStatusThatMatchesNoSentinelIsLeftAlone. `wire` maps two sentinels onto
// NotFound today, so nothing on the wire produces a third — but a status this
// package did not build is exactly what arrives when the other end is a
// different version, and inventing a sentinel for it would be worse than
// passing it through.
func TestAStatusThatMatchesNoSentinelIsLeftAlone(t *testing.T) {
	for _, code := range []connect.Code{connect.CodeNotFound, connect.CodeInvalidArgument} {
		err := unwire(connect.NewError(code, errors.New("something this package never wrote")))

		var cerr *connect.Error
		require.ErrorAs(t, err, &cerr)
		require.Equal(t, code, cerr.Code())
	}

	// And something that is not a connect error at all — a dial that never
	// reached a server — passes through untouched.
	plain := errors.New("no route to peer")
	require.Equal(t, plain, unwire(plain))
}

// TestTheHostTableIsTheSameThroughTheClient — the sentinels for a name nobody
// holds and a name somebody else holds survive the wire like the rest.
func TestTheHostTableIsTheSameThroughTheClient(t *testing.T) {
	h := newHarness(t, pkgregister.NewMemStore())
	c := newClient(t, h)

	_, err := c.Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1,
		Compiled: []byte("compiled"), Hosts: []string{"shop.example.com"},
	})
	require.NoError(t, err)

	project, environment, err := c.ResolveHost(t.Context(), "shop.example.com")
	require.NoError(t, err)
	require.Equal(t, "shop", project)
	require.Equal(t, "prod", environment)

	_, _, err = c.ResolveHost(t.Context(), "nobody.example.com")
	require.ErrorIs(t, err, pkgregister.ErrNoHost)

	_, err = c.Publish(t.Context(), pkgregister.Publication{
		Project: "impostor", Environment: "prod", Revision: 1,
		Compiled: []byte("compiled"), Hosts: []string{"shop.example.com"},
	})
	require.ErrorIs(t, err, pkgregister.ErrHostClaimed)
}

// A DESTINATION'S READS CROSS THE WIRE UNCHANGED: its view, its assignments, and the
// difference between a destination not listed and a deployment not published.
func TestViewsCrossTheWire(t *testing.T) {
	h := newHarness(t, pkgregister.NewMemStore())
	c := newClient(t, h)

	_, err := c.Publish(t.Context(), pkgregister.Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("whole"),
		Views: []pkgregister.View{{Destination: "region-1", Compiled: []byte("region-1")}, {Destination: "region-2", Compiled: []byte("{}")}}})
	require.NoError(t, err)

	v, err := c.ResolveView(t.Context(), "shop", "prod", "region-1")
	require.NoError(t, err)
	require.Equal(t, pkgregister.IDOf([]byte("region-1")), v.Object)
	require.NotEmpty(t, v.Manifest)

	as, err := c.Assigned(t.Context(), "region-2")
	require.NoError(t, err)
	require.Len(t, as, 1)
	require.Equal(t, "shop", as[0].Project)
	require.Equal(t, pkgregister.IDOf([]byte("{}")), as[0].View.Object)

	_, err = c.ResolveView(t.Context(), "shop", "prod", "region-3")
	require.ErrorIs(t, err, pkgregister.ErrNoView)
	_, err = c.ResolveView(t.Context(), "shop", "staging", "region-1")
	require.ErrorIs(t, err, pkgregister.ErrNotPublished)
	_, err = c.ResolveView(t.Context(), "shop", "prod", "")
	require.ErrorIs(t, err, pkgregister.ErrNoDestination)
	_, err = c.Assigned(t.Context(), "")
	require.ErrorIs(t, err, pkgregister.ErrNoDestination)

	// cut for nobody is not uncut: no destination holds it
	_, err = c.Publish(t.Context(), pkgregister.Publication{Project: "blog", Environment: "prod", Revision: 1, Compiled: []byte("b"), Views: []pkgregister.View{}})
	require.NoError(t, err)
	_, err = c.ResolveView(t.Context(), "blog", "prod", "region-1")
	require.ErrorIs(t, err, pkgregister.ErrNoView)
	// and uncut is whole everywhere
	_, err = c.Publish(t.Context(), pkgregister.Publication{Project: "blog", Environment: "dev", Revision: 1, Compiled: []byte("b")})
	require.NoError(t, err)
	v, err = c.ResolveView(t.Context(), "blog", "dev", "region-1")
	require.NoError(t, err)
	require.Equal(t, pkgregister.IDOf([]byte("b")), v.Object)
}

func TestTheManifestCrossesTheWire(t *testing.T) {
	h := newHarness(t, pkgregister.NewMemStore())
	c := newClient(t, h)

	_, _, err := c.Manifest(t.Context(), "shop", "prod")
	require.ErrorIs(t, err, pkgregister.ErrNotPublished)

	_, err = c.Publish(t.Context(), pkgregister.Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("whole"),
		Views: []pkgregister.View{{Destination: "region-1", Compiled: []byte("region-1")}}})
	require.NoError(t, err)
	m, id, err := c.Manifest(t.Context(), "shop", "prod")
	require.NoError(t, err)
	local, localID, err := h.Service.reg.Manifest(t.Context(), "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, local, m)
	require.Equal(t, localID, id)
}
