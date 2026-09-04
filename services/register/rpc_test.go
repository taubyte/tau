package register

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	pkgregister "github.com/taubyte/tau/pkg/register"

	registerv1 "github.com/taubyte/tau/pkg/proto/gen/tau/register/v1"
)

func publish(t *testing.T, h *harness, project, env string, rev uint64, compiled string) string {
	t.Helper()
	res, err := h.Client.Publish(t.Context(), connect.NewRequest(&registerv1.PublishRequest{
		Project: project, Environment: env, Revision: rev, Compiled: []byte(compiled),
	}))
	require.NoError(t, err)
	return res.Msg.GetObject()
}

func codeOf(t *testing.T, err error) connect.Code {
	t.Helper()
	require.Error(t, err)
	return connect.CodeOf(err)
}

// TestTheWholeChainOverTheWire: publish a compiled project, resolve the
// pointer, fetch the object it names.
func TestTheWholeChainOverTheWire(t *testing.T) {
	h := newHarness(t, pkgregister.NewMemStore())

	id := publish(t, h, "shop", "prod", 1, "memory: 64MB")

	p, err := h.Client.Resolve(t.Context(), connect.NewRequest(&registerv1.ResolveRequest{
		Project: "shop", Environment: "prod",
	}))
	require.NoError(t, err)
	require.Equal(t, id, p.Msg.GetObject())
	require.Equal(t, uint64(1), p.Msg.GetRevision())

	obj, err := h.Client.Object(t.Context(), connect.NewRequest(&registerv1.ObjectRequest{Object: id}))
	require.NoError(t, err)
	require.Equal(t, []byte("memory: 64MB"), obj.Msg.GetCompiled())
}

// TestTheSameConfigurationIsTheSameObjectAcrossTheWire — the content address
// survives the round trip, which is what lets a caller tell a redeploy from a
// change without diffing anything.
func TestTheSameConfigurationIsTheSameObjectAcrossTheWire(t *testing.T) {
	h := newHarness(t, pkgregister.NewMemStore())

	first := publish(t, h, "shop", "prod", 1, "memory: 64MB")
	second := publish(t, h, "shop", "prod", 2, "memory: 64MB")
	changed := publish(t, h, "shop", "prod", 3, "memory: 128MB")

	require.Equal(t, first, second)
	require.NotEqual(t, first, changed)
}

// TestAStaleRevisionIsFailedPrecondition and not InvalidArgument: the request
// was well formed, the state refused it, and a retry with a higher revision
// succeeds. A caller told InvalidArgument would stop instead.
func TestAStaleRevisionIsFailedPrecondition(t *testing.T) {
	h := newHarness(t, pkgregister.NewMemStore())
	publish(t, h, "shop", "prod", 7, "current")

	_, err := h.Client.Publish(t.Context(), connect.NewRequest(&registerv1.PublishRequest{
		Project: "shop", Environment: "prod", Revision: 3, Compiled: []byte("late"),
	}))
	require.Equal(t, connect.CodeFailedPrecondition, codeOf(t, err))

	// And the retry the code invites does work.
	publish(t, h, "shop", "prod", 8, "the next one")
}

func TestAnUnpublishedEnvironmentIsNotFound(t *testing.T) {
	h := newHarness(t, pkgregister.NewMemStore())

	_, err := h.Client.Resolve(t.Context(), connect.NewRequest(&registerv1.ResolveRequest{
		Project: "shop", Environment: "prod",
	}))
	require.Equal(t, connect.CodeNotFound, codeOf(t, err))

	_, err = h.Client.Object(t.Context(), connect.NewRequest(&registerv1.ObjectRequest{
		Object: string(pkgregister.IDOf([]byte("never published"))),
	}))
	require.Equal(t, connect.CodeNotFound, codeOf(t, err))
}

// TestMalformedRequestsAreInvalidArgument. The rules are pkg/register's; what
// is checked here is only that they arrive as a status a caller can act on.
func TestMalformedRequestsAreInvalidArgument(t *testing.T) {
	h := newHarness(t, pkgregister.NewMemStore())

	for _, c := range []struct {
		name string
		req  *registerv1.PublishRequest
	}{
		{"no project", &registerv1.PublishRequest{Environment: "prod", Revision: 1, Compiled: []byte("x")}},
		{"no environment", &registerv1.PublishRequest{Project: "shop", Revision: 1, Compiled: []byte("x")}},
		{"nothing compiled", &registerv1.PublishRequest{Project: "shop", Environment: "prod", Revision: 1}},
		{"a name with a separator", &registerv1.PublishRequest{Project: "a/prod", Environment: "prod", Revision: 1, Compiled: []byte("x")}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := h.Client.Publish(t.Context(), connect.NewRequest(c.req))
			require.Equal(t, connect.CodeInvalidArgument, codeOf(t, err))
		})
	}

	_, err := h.Client.Object(t.Context(), connect.NewRequest(&registerv1.ObjectRequest{}))
	require.Equal(t, connect.CodeNotFound, codeOf(t, err), "an empty content address names no object")
}

// TestACorruptedPointerIsDataLossAndNotNotFound. A runtime told NotFound would
// report 404 for an environment that has been serving for months, and nothing
// in that answer says the store is the broken part.
func TestACorruptedPointerIsDataLoss(t *testing.T) {
	store := pkgregister.NewMemStore()
	h := newHarness(t, store)
	publish(t, h, "shop", "prod", 1, "compiled")

	// Reach past the register and corrupt the row it wrote.
	for _, k := range store.Keys() {
		if k[0] == 'p' {
			require.NoError(t, store.Put(t.Context(), k, []byte("garbage")))
		}
	}

	_, err := h.Client.Resolve(t.Context(), connect.NewRequest(&registerv1.ResolveRequest{
		Project: "shop", Environment: "prod",
	}))
	require.Equal(t, connect.CodeDataLoss, codeOf(t, err))

	// And a publish repairs it, so there is no state only a human can leave.
	publish(t, h, "shop", "prod", 2, "compiled again")
	p, err := h.Client.Resolve(t.Context(), connect.NewRequest(&registerv1.ResolveRequest{
		Project: "shop", Environment: "prod",
	}))
	require.NoError(t, err)
	require.Equal(t, uint64(2), p.Msg.GetRevision())
}

// TestAStoreFailureIsUnavailableAndNotInternal. Unavailable tells a caller to
// try another register node; Internal tells it to stop.
func TestAStoreFailureIsUnavailable(t *testing.T) {
	h := newHarness(t, brokenStore{pkgregister.NewMemStore()})

	_, err := h.Client.Resolve(t.Context(), connect.NewRequest(&registerv1.ResolveRequest{
		Project: "shop", Environment: "prod",
	}))
	require.Equal(t, connect.CodeUnavailable, codeOf(t, err))

	_, err = h.Client.Publish(t.Context(), connect.NewRequest(&registerv1.PublishRequest{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("x"),
	}))
	require.Equal(t, connect.CodeUnavailable, codeOf(t, err))

	_, err = h.Client.Object(t.Context(), connect.NewRequest(&registerv1.ObjectRequest{
		Object: string(pkgregister.IDOf([]byte("x"))),
	}))
	require.Equal(t, connect.CodeUnavailable, codeOf(t, err))
}

// TestEnvironmentsAndProjectsDoNotShareAPointer over the wire, which is the
// property a key separator escape would break.
func TestEnvironmentsAndProjectsDoNotShareAPointer(t *testing.T) {
	h := newHarness(t, pkgregister.NewMemStore())

	publish(t, h, "shop", "prod", 1, "shop prod")
	publish(t, h, "shop", "staging", 1, "shop staging")
	publish(t, h, "blog", "prod", 1, "blog prod")

	for _, c := range []struct{ project, env, want string }{
		{"shop", "prod", "shop prod"},
		{"shop", "staging", "shop staging"},
		{"blog", "prod", "blog prod"},
	} {
		p, err := h.Client.Resolve(t.Context(), connect.NewRequest(&registerv1.ResolveRequest{
			Project: c.project, Environment: c.env,
		}))
		require.NoError(t, err)

		obj, err := h.Client.Object(t.Context(), connect.NewRequest(&registerv1.ObjectRequest{Object: p.Msg.GetObject()}))
		require.NoError(t, err)
		require.Equal(t, c.want, string(obj.Msg.GetCompiled()))
	}
}

// TestAGatewayAsksWhoseNameThisIsOverTheWire — the first thing a gateway does
// with a request, and the reason the register holds a host table at all.
func TestResolveHostOverTheWire(t *testing.T) {
	h := newHarness(t, pkgregister.NewMemStore())

	_, err := h.Client.Publish(t.Context(), connect.NewRequest(&registerv1.PublishRequest{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("compiled"),
		Hosts: []string{"shop.example.com"},
	}))
	require.NoError(t, err)

	answer, err := h.Client.ResolveHost(t.Context(), connect.NewRequest(&registerv1.HostRequest{
		Fqdn: "SHOP.example.com",
	}))
	require.NoError(t, err)
	require.Equal(t, "shop", answer.Msg.GetProject())
	require.Equal(t, "prod", answer.Msg.GetEnvironment())

	_, err = h.Client.ResolveHost(t.Context(), connect.NewRequest(&registerv1.HostRequest{
		Fqdn: "nobody.example.com",
	}))
	require.Equal(t, connect.CodeNotFound, codeOf(t, err))
}

// TestAClaimedNameIsAlreadyExistsAndNotFailedPrecondition. A retry with the
// same name will never succeed, and the two codes tell a caller opposite
// things about whether to try again.
func TestAClaimedNameIsAlreadyExists(t *testing.T) {
	h := newHarness(t, pkgregister.NewMemStore())

	_, err := h.Client.Publish(t.Context(), connect.NewRequest(&registerv1.PublishRequest{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("compiled"),
		Hosts: []string{"shop.example.com"},
	}))
	require.NoError(t, err)

	_, err = h.Client.Publish(t.Context(), connect.NewRequest(&registerv1.PublishRequest{
		Project: "impostor", Environment: "prod", Revision: 1, Compiled: []byte("compiled"),
		Hosts: []string{"shop.example.com"},
	}))
	require.Equal(t, connect.CodeAlreadyExists, codeOf(t, err))
}
