package node

import (
	"context"
	"errors"
	"fmt"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/specs"
	secretsService "github.com/taubyte/tau/services/secrets"
)

// secrets.go wires the one store in v2 that git does not write.
//
// It is locate.go's rule again — run it here, use it directly; otherwise ask
// stats who does — and the only thing that makes it its own file is that the
// consumer is an interface services/worker declares rather than one this
// package does.

// ErrNoSecrets is a node that needs a vault and can find none.
//
// IT IS A REFUSAL AND NOT AN EMPTY VALUE, which is the same rule the vault
// itself keeps: a workload started without its database password fails
// somewhere else, later, as an application that looks broken.
var ErrNoSecrets = errors.New("node: no secrets service is reachable")

// holder is what both halves offer: this node's own vault and a client onto
// another node's answer with the same four methods, so nothing above knows
// which it got.
type holder interface {
	Secret(ctx context.Context, project, environment, secretID string) (string, error)
	Set(ctx context.Context, project, environment, name string, value []byte) error
	Delete(ctx context.Context, project, environment, name string) error
	List(ctx context.Context, project, environment string) ([]string, error)
}

// vault resolves a secret for whichever of this node's services needs one.
//
// It satisfies services/worker's Secrets interface — the read on the request
// path — and services/build/frontend's Vault, which is the write. The write
// half is deliberately NOT the read half's mirror: the frontend's interface has
// no Get, because nothing on a public ingress reads a value back.
type vault struct {
	node *Node

	// reach is the one rule, as [rpc.Reach]: run it here, use it directly;
	// otherwise ask stats who does and dial that one.
	//
	// A LOCAL VAULT IS NOT A CACHE, IT IS THE ABSENCE OF A HOP — and here it
	// is also the absence of a secret on the wire, which is the one place in
	// this composition where the short-circuit is a security property and not
	// only a latency one.
	reach *rpc.Reacher[holder]
}

// newVault is the only way to make one: the reach is what a vault IS, and a
// zero value would be a vault that opens nothing.
func newVault(n *Node) *vault {
	v := &vault{node: n}
	v.reach = rpc.Reach(n.opts.host, Secrets,
		n.routeTo(Secrets, ErrNoSecrets, "this node runs neither a secrets service nor stats to find one"),
		func() (holder, bool) {
			if n.secrets == nil {
				return nil, false
			}
			return n.secrets.Local(), true
		},
		func(id libp2ppeer.ID) (holder, error) {
			c, err := secretsService.NewClient(n.opts.host, id)
			if err != nil {
				return nil, err
			}
			return c, nil
		})
	return v
}

// Secret is the read on the request path.
func (v *vault) Secret(ctx context.Context, project, environment, secretID string) (string, error) {
	h, err := v.reach.Now(ctx)
	if err != nil {
		return "", err
	}
	return h.Secret(ctx, project, environment, secretID)
}

// Set, Delete and List are the developer's half, reached from the build
// frontend. They are the one write in v2 that is not a git push, and the
// frontend authorizes them exactly as it authorizes a deploy.
func (v *vault) Set(ctx context.Context, project, environment, name string, value []byte) error {
	h, err := v.reach.Now(ctx)
	if err != nil {
		return err
	}
	return h.Set(ctx, project, environment, name, value)
}

func (v *vault) Delete(ctx context.Context, project, environment, name string) error {
	h, err := v.reach.Now(ctx)
	if err != nil {
		return err
	}
	return h.Delete(ctx, project, environment, name)
}

func (v *vault) List(ctx context.Context, project, environment string) ([]string, error) {
	h, err := v.reach.Now(ctx)
	if err != nil {
		return nil, err
	}
	return h.List(ctx, project, environment)
}

// Secret is pkg/invoke/wasm's Secrets: the value a grant names, read at the
// moment the guest asks rather than at start.
//
// IT IS THE SAME VAULT the worker resolves a run-time variable from, which is
// what makes the two paths agree — a rotated secret reaches a variable at the
// next deploy and reaches this at the next call, and both read the same row.
func (v *vault) SecretFor(ctx context.Context, grant invoke.Grant) ([]byte, error) {
	project, environment, id := specs.SplitHandle(grant.Handle)
	if project == "" || environment == "" || id == "" {
		return nil, fmt.Errorf("node: %q does not address a secret", grant.Handle)
	}

	value, err := v.Secret(ctx, project, environment, id)
	if err != nil {
		if errors.Is(err, secretsService.ErrNotSet) {
			// THE TIER'S ARM, so a guest is told "nothing is set" rather than
			// "the node is unhappy" — the same translation node/cache.go makes
			// and the same bug it was written to stop.
			return nil, fmt.Errorf("%w: %w", wasmtier.ErrNotSet, err)
		}
		return nil, err
	}
	return []byte(value), nil
}

// Close releases the stream to a remote vault, if one was opened.
func (v *vault) Close() error { return v.reach.Close() }
