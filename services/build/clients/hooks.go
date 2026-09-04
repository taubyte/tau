package clients

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/rpc"
	authv1 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v1"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v1/authv1connect"
)

// HookClient answers one question against tau.auth.v1's HookService: what does
// this cloud know about the webhook a stranger just POSTed to.
//
// It is the frontend's single most security-sensitive call, because its answer
// decides an authentication: the secret verifies the body's signature, and the
// repository key says which repository that signature is good FOR. v1 fetched
// only the secret (services/patrick/github.go's githubCheckHookAndExtractSecret
// sets one context variable, "GithubSecret") and then let the BODY name the
// repository — so a signature made under any registered hook authorized a job
// for any repository. auth.proto already carries repository_key on GithubHook;
// this returns the message so the pair cannot be separated again.
type HookClient struct {
	pool *Pool
}

// Hooks adapts a pool dialling auth.
func Hooks(p *Pool) (*HookClient, error) {
	if err := requireProtocol(p, AuthProtocol, "hooks"); err != nil {
		return nil, err
	}
	return &HookClient{pool: p}, nil
}

// Get returns the GithubHook registered under id.
//
// A hook of another provider is a FailedPrecondition rather than a nil
// dereference: auth.proto's Hook is a oneof and its BitbucketHook case is
// reachable in the schema and unreachable in the running service, so the case
// exists and has no secret to verify with. Answering it as "not a github hook"
// is what keeps the frontend from treating an absent secret as an empty one.
func (c *HookClient) Get(ctx context.Context, hookID string) (*authv1.GithubHook, error) {
	if hookID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("build: hook id is required"))
	}

	var hook *authv1.GithubHook
	err := c.pool.Do(ctx, func(client *rpc.Client) error {
		hooks := authv1connect.NewHookServiceClient(client.HTTP(), client.URL())
		resp, err := hooks.Get(ctx, connect.NewRequest(&authv1.HookRef{Id: hookID}))
		if err != nil {
			return err
		}
		hook = resp.Msg.GetGithub()
		return nil
	})
	if err != nil {
		return nil, err
	}
	if hook == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("build: hook %q is not a github hook", hookID))
	}
	return hook, nil
}

// requireProtocol refuses a pool built for another service. Nothing else would
// notice: the call would reach a real peer, find no such method, and come back
// as an Unimplemented from a service that was never meant to be asked.
func requireProtocol(p *Pool, want protocol.ID, what string) error {
	if p == nil {
		return errors.New("build: no pool")
	}
	if p.Protocol() != want {
		return fmt.Errorf("build: %s needs a pool on %s, got %s", what, want, p.Protocol())
	}
	return nil
}
