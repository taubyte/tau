package node

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
	authService "github.com/taubyte/tau/services/auth"
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
	n, err := New(Config{Services: []string{Auth}},
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

func TestConsoleOriginsAreTheCallbacksOrigins(t *testing.T) {
	require.Equal(t, []string{"https://console.example", "http://127.0.0.1:3000"},
		consoleOrigins([]string{"https://console.example/cb", "https://console.example/other", "http://127.0.0.1:3000/x", "::"}))
}

func TestAuthConfigTakesEachForgesApp(t *testing.T) {
	n := &Node{cfg: Config{Cloud: "example.com", Forges: []Forge{{Host: "github.com", OAuthClientID: "c1"}, {Host: "gitlab.com"}}}}
	c := n.authConfig()
	require.Equal(t, "tau:example.com", c.Issuer)
	require.Equal(t, []authService.ForgeApp{{Host: "github.com", ClientID: "c1"}}, c.Forges)
	n.cfg.Auth.Issuer = "tau:set"
	require.Equal(t, "tau:set", n.authConfig().Issuer)
}
