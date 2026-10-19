package node

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/discovery"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
	authService "github.com/taubyte/tau/services/auth"
	"github.com/taubyte/tau/services/build/clients"
)

// linked admits subject "fake-user-subject", the fake forge's only user.
type linked struct{ closed bool }

func (l *linked) Admit(_ context.Context, c authService.Caller) (authService.Admission, error) {
	return authService.Admission{Admitted: c.Subject == "fake-user-subject", Reason: "not linked"}, nil
}

func (l *linked) Close() error { l.closed = true; return nil }

func TestAComposedIdentityIsWhatAuthAnswersWith(t *testing.T) {
	id := &linked{}
	var handed Host
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)
	n, err := New(Config{Services: []string{Auth}, Auth: authService.Config{DomainValidation: keyPair(t)}},
		WithHost(h), WithDatabases(&memFactory{}),
		WithIdentity(func(host Host) (authService.Identity, error) {
			handed = host
			return id, nil
		}))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })
	require.NoError(t, n.Start(t.Context()))
	require.Equal(t, Auth, handed.Name())

	client, err := rpc.NewClient(newHost(t, mn), h.ID(), authService.Protocol)
	require.NoError(t, err)
	defer client.Close()
	identities := authv2connect.NewIdentityServiceClient(client.HTTP(), client.URL())

	got, err := identities.Admit(t.Context(), connect.NewRequest(&authv2.Caller{Forge: "github.com", Subject: "someone-else"}))
	require.NoError(t, err)
	require.False(t, got.Msg.GetAdmitted())
	require.Equal(t, "not linked", got.Msg.GetReason())

	require.NoError(t, n.Close())
	require.True(t, id.closed, "an identity that can be closed is closed with the node")
}

func TestWhatWithIdentityRefuses(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	_, err := New(Config{Services: []string{Discovery}}, WithHost(h), WithIdentity(nil))
	require.Error(t, err)

	for name, build := range map[string]func(Host) (authService.Identity, error){
		"fails":   func(Host) (authService.Identity, error) { return nil, errors.New("no accounts") },
		"nothing": func(Host) (authService.Identity, error) { return nil, nil },
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New(Config{Services: []string{Auth}}, WithHost(newHost(t, mn)), WithDatabases(&memFactory{}), WithIdentity(build))
			require.Error(t, err)
		})
	}
}

func TestAdmissionsAsksTheForgeWhoThenAuthWhether(t *testing.T) {
	f := fake.New(fake.GitHubLike)
	registry, err := f.Registry(forge.Policy{})
	require.NoError(t, err)

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	server, caller := newHost(t, mn), newHost(t, mn)

	auth, err := authService.New(server, &memKV{rows: map[string][]byte{}, factory: &memFactory{}}, authService.Config{DomainValidation: keyPair(t)}, authService.WithIdentity(&linked{}))
	require.NoError(t, err)
	require.NoError(t, auth.Start(t.Context()))
	t.Cleanup(func() { auth.Close() })

	pool, err := clients.NewPool(caller, discovery.Static(server.ID()), clients.AuthProtocol, 2)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	identities, err := clients.Identities(pool)
	require.NoError(t, err)

	a := &admissions{forges: registry, auth: identities}
	admitted, _, err := a.Admit(t.Context(), f.TokenString(), f.Keys().Writable)
	require.NoError(t, err)
	require.True(t, admitted)

	// A credential the forge will not say who holds is not decided.
	_, _, err = a.Admit(t.Context(), f.BadTokenString(), f.Keys().Writable)
	require.Error(t, err)

	// Nor is a host no forge is bound to.
	other := fake.New(fake.GitLabLike)
	_, _, err = a.Admit(t.Context(), f.TokenString(), other.Keys().Writable)
	require.Error(t, err)

	// Nor an auth that cannot be reached.
	require.NoError(t, auth.Close())
	_, _, err = a.Admit(t.Context(), f.TokenString(), f.Keys().Writable)
	require.Error(t, err)
}

func keyPair(t *testing.T) authService.KeyPair {
	t.Helper()
	pair, err := authService.GenerateKeyPair()
	require.NoError(t, err)
	return pair
}
