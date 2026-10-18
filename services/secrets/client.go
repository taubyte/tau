package secrets

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/p2p/rpc"

	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2/secretsv2connect"
)

// client.go is the other side of rpc.go, and it exists so a consumer holds ONE
// shape whether the vault is on this node or another. The errors come back as
// the same sentinels, so `errors.Is(err, secrets.ErrNotSet)` is true either
// way — without that, a worker would carry two error paths for one question
// and would get the remote one wrong, because a status code is easy to ignore.

// Client calls a secrets service on another peer.
type Client struct {
	conn *rpc.Client
	svc  secretsv2connect.SecretsServiceClient
}

// NewClient prepares calls to the vault on p. Nothing is dialled until the
// first call, and that call's context governs the dial.
func NewClient(h host.Host, p libp2ppeer.ID) (*Client, error) {
	conn, err := rpc.NewClient(h, p, Protocol)
	if err != nil {
		return nil, fmt.Errorf("secrets: dialling %s: %w", p, err)
	}
	return &Client{conn: conn, svc: secretsv2connect.NewSecretsServiceClient(conn.HTTP(), conn.URL())}, nil
}

// Close releases the stream. Calls after it fail.
func (c *Client) Close() { c.conn.Close() }

func (c *Client) Get(ctx context.Context, project, environment, name string) ([]byte, error) {
	res, err := c.svc.Get(ctx, connect.NewRequest(&secretsv2.SecretRequest{
		Scope: &secretsv2.Scope{Project: project, Environment: environment}, Name: name,
	}))
	if err != nil {
		return nil, unwire(err)
	}
	return res.Msg.GetValue(), nil
}

func (c *Client) Set(ctx context.Context, project, environment, name string, value []byte) error {
	_, err := c.svc.Set(ctx, connect.NewRequest(&secretsv2.SetRequest{
		Scope: &secretsv2.Scope{Project: project, Environment: environment}, Name: name, Value: value,
	}))
	if err != nil {
		return unwire(err)
	}
	return nil
}

func (c *Client) Delete(ctx context.Context, project, environment, name string) error {
	_, err := c.svc.Delete(ctx, connect.NewRequest(&secretsv2.SecretRequest{
		Scope: &secretsv2.Scope{Project: project, Environment: environment}, Name: name,
	}))
	if err != nil {
		return unwire(err)
	}
	return nil
}

func (c *Client) List(ctx context.Context, project, environment string) ([]string, error) {
	res, err := c.svc.List(ctx, connect.NewRequest(&secretsv2.Scope{Project: project, Environment: environment}))
	if err != nil {
		return nil, unwire(err)
	}
	return res.Msg.GetNames(), nil
}

// Secret is services/worker's Secrets interface, so a worker composed against
// a remote vault and one composed against a local *Vault differ in no code
// path but the constructor.
func (c *Client) Secret(ctx context.Context, project, environment, secretID string) (string, error) {
	value, err := c.Get(ctx, project, environment, secretID)
	if err != nil {
		return "", err
	}
	return string(value), nil
}

// unwire turns a status back into the sentinel a local vault would have
// returned.
func unwire(err error) error {
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		return err
	}

	switch cerr.Code() {
	case connect.CodeNotFound:
		return fmt.Errorf("%w: %s", ErrNotSet, cerr.Message())
	case connect.CodeInvalidArgument:
		// Four sentinels share this code and they are different answers: a
		// caller that omitted the environment and one that put a "/" in the
		// name are told apart by which message came back, because rpc.go
		// wraps rather than replaces.
		for _, sentinel := range []error{ErrNoProject, ErrNoEnvironment, ErrNoName, ErrSeparator} {
			if strings.HasPrefix(cerr.Message(), sentinel.Error()) {
				return fmt.Errorf("%w: %s", sentinel, cerr.Message())
			}
		}
	}
	return err
}
