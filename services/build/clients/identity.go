package clients

import (
	"context"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/p2p/rpc"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

// IdentityClient asks auth whether a caller a forge vouched for may use the
// cloud, against tau.auth.v2's IdentityService.
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

// Admit is auth's answer about one caller.
func (c *IdentityClient) Admit(ctx context.Context, caller *authv2.Caller) (*authv2.Admission, error) {
	var admission *authv2.Admission
	err := c.pool.Do(ctx, func(client *rpc.Client) error {
		resp, err := authv2connect.NewIdentityServiceClient(client.HTTP(), client.URL()).Admit(ctx, connect.NewRequest(caller))
		if err != nil {
			return err
		}
		admission = resp.Msg
		return nil
	})
	return admission, err
}
