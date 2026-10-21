//go:build dreaming

package dream_test

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
	"github.com/taubyte/tau/services/auth"
)

// TestSignInAcrossAFleet: the built-in admin signs in on one auth node, mints
// an invite there, and a person a forge vouches for redeems it on the OTHER
// node; each node's token verifies on the other. That is the whole of what a
// fleet adds — keys and pepper in the real vault, users in the replicated
// store, two signing keys with nothing written by both.
func TestSignInAcrossAFleet(t *testing.T) {
	f := fake.New(fake.GitHubLike)
	reg, err := f.Registry(forge.Policy{})
	require.NoError(t, err)
	hash, err := auth.HashAdminPassword("correct horse battery")
	require.NoError(t, err)

	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Copies:   2,
		Services: []string{node.Discovery, node.Secrets, node.Auth},
		Options:  []node.Option{node.WithForges(reg)},
		Configure: func(_ int, c *node.Config) {
			c.Auth.Admin = auth.Admin{Name: "root", PasswordHash: hash}
			c.Auth.SignUp = auth.SignUpInvite
		},
	})
	require.NoError(t, err)
	defer u.Close()
	pids := u.Pids()
	require.Len(t, pids, 2)

	dial := func(i int) (authv2connect.SessionServiceClient, authv2connect.UserServiceClient, authv2connect.IdentityServiceClient) {
		target, err := libp2ppeer.Decode(pids[i])
		require.NoError(t, err)
		c, err := rpc.NewClient(u.Peers()[1-i].Peer(), target, auth.Protocol)
		require.NoError(t, err)
		t.Cleanup(c.Close)
		return authv2connect.NewSessionServiceClient(c.HTTP(), c.URL()),
			authv2connect.NewUserServiceClient(c.HTTP(), c.URL()),
			authv2connect.NewIdentityServiceClient(c.HTTP(), c.URL())
	}
	s0, u0, id0 := dial(0)
	s1, _, id1 := dial(1)
	ctx := t.Context()

	// The vault needs its members before the first key can be made.
	var admin *authv2.SignedIn
	require.Eventually(t, func() bool {
		resp, err := s0.Login(ctx, connect.NewRequest(&authv2.LoginRequest{Name: "root", Password: "correct horse battery"}))
		if err != nil {
			return false
		}
		admin = resp.Msg
		return true
	}, 60*time.Second, time.Second, "the admin signs in once the vault answers")

	req := connect.NewRequest(&authv2.CreateInviteRequest{Note: "fleet"})
	req.Header().Set("Authorization", "Bearer "+admin.GetSession().GetAccessToken())
	inv, err := u0.CreateInvite(ctx, req)
	require.NoError(t, err)

	// Redeemed on the other node, once the invite has replicated there.
	var person *authv2.SignedIn
	require.Eventually(t, func() bool {
		resp, err := s1.Pasted(ctx, connect.NewRequest(&authv2.PastedRequest{Provider: f.Host(), Token: f.TokenString(), Invite: inv.Msg.GetCode()}))
		if err != nil {
			return false
		}
		person = resp.Msg
		return true
	}, 60*time.Second, time.Second, "the invite replicates to the other node")
	require.Equal(t, "fake-user", person.GetSession().GetUser().GetName())

	// Each node verifies the other's token.
	require.Eventually(t, func() bool {
		a, err := id1.Verify(ctx, connect.NewRequest(&authv2.VerifyRequest{Token: admin.GetSession().GetAccessToken()}))
		if err != nil || a.Msg.GetUser().GetId() != auth.AdminID {
			return false
		}
		p, err := id0.Verify(ctx, connect.NewRequest(&authv2.VerifyRequest{Token: person.GetSession().GetAccessToken()}))
		return err == nil && p.Msg.GetUser().GetName() == "fake-user"
	}, 60*time.Second, time.Second, "each node's signing key and user reach the other")

	// And the forge identity is now admitted without a tau token, on either.
	a, err := id0.Admit(ctx, connect.NewRequest(&authv2.Caller{Forge: f.Host(), Subject: "fake-user-subject"}))
	require.NoError(t, err)
	require.True(t, a.Msg.GetAdmitted())
	a, err = id0.Admit(ctx, connect.NewRequest(&authv2.Caller{Forge: f.Host(), Subject: "stranger"}))
	require.NoError(t, err)
	require.False(t, a.Msg.GetAdmitted())

	// The forge credential comes back to the client beside the tau token,
	// never inside it.
	require.Equal(t, f.TokenString(), person.GetForge().GetAccessToken())
	require.Equal(t, f.Host(), person.GetConnection().GetHost())
	require.Equal(t, authv2.SignInLink_SIGN_IN_LINK_THIS_USER, person.GetConnection().GetSignIn())
	require.NotContains(t, person.GetSession().GetAccessToken(), f.TokenString())
	require.NotContains(t, person.GetSession().GetRefreshToken(), f.TokenString())
}
