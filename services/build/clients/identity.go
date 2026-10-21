package clients

import (
	"context"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/p2p/rpc"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

// IdentityClient asks auth who a caller is, against tau.auth.v2's
// IdentityService.
type IdentityClient struct {
	pool *Pool
}

// Identities adapts a pool dialling auth.
func Identities(p *Pool) (*IdentityClient, error) {
	if err := requireProtocol(p, AuthProtocol, "identities"); err != nil {
		return nil, err
	}
	return &IdentityClient{pool: p}, nil
}

// Caller is the id of the user a tau token was issued to. A token auth
// refuses is its Unauthenticated error.
func (c *IdentityClient) Caller(ctx context.Context, token string) (string, error) {
	var user string
	err := c.pool.Do(ctx, func(client *rpc.Client) error {
		resp, err := authv2connect.NewIdentityServiceClient(client.HTTP(), client.URL()).Verify(ctx, connect.NewRequest(&authv2.VerifyRequest{Token: token}))
		if err != nil {
			return err
		}
		user = resp.Msg.GetUser().GetId()
		return nil
	})
	return user, err
}
