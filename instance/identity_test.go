package instance

import (
	"context"
	"errors"
	"testing"

	"github.com/taubyte/tau/pkg/forge/fake"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
	authService "github.com/taubyte/tau/services/auth"
)

// linked admits the fake forge's only user.
type linked struct{ closed bool }

func (l *linked) Admit(_ context.Context, c authService.Caller) (authService.Admission, error) {
	return authService.Admission{Admitted: c.Subject == fake.Subject, Reason: "not linked"}, nil
}

func (l *linked) Close() error { l.closed = true; return nil }

func TestAComposedIdentityIsWhatAuthAnswersWith(t *testing.T) {
	id := &linked{}
	var handed Host
	p := mockPeer(t)
	n, err := New(p, rooted(t, Config{Services: []string{Auth}}), withKVDB(&memFactory{}), WithIdentity(func(host Host) (authService.Identity, error) {
		handed = host
		return id, nil
	}))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })
	require.NoError(t, n.Start(t.Context()))
	require.Equal(t, p.Host(), handed.Peer().Host(), "handed the instance it runs on")

	client, err := rpc.NewClient(callerOf(t, p), p.ID(), authService.Protocol)
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
	_, err := New(mockPeer(t), rooted(t, Config{Services: []string{Stats}}), WithIdentity(nil))
	require.Error(t, err)

	for name, build := range map[string]func(Host) (authService.Identity, error){
		"fails":   func(Host) (authService.Identity, error) { return nil, errors.New("no accounts") },
		"nothing": func(Host) (authService.Identity, error) { return nil, nil },
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New(mockPeer(t), rooted(t, Config{Services: []string{Auth}}), withKVDB(&memFactory{}), WithIdentity(build))
			require.Error(t, err)
		})
	}
}

func TestAuthConfigDefaultsTheIssuer(t *testing.T) {
	n := &Instance{Node: mockPeer(t), cfg: Config{Cloud: "example.com"}}
	require.Equal(t, "tau:example.com", n.authConfig().Issuer)
	n.cfg.Auth.Issuer = "tau:set"
	require.Equal(t, "tau:set", n.authConfig().Issuer)
}
