package node

import (
	"context"
	"errors"
	"fmt"
	"sync"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

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

	// mu guards the stream to a remote vault, opened on first use and
	// re-opened if the node it names changes.
	mu     sync.Mutex
	client *secretsService.Client
	at     libp2ppeer.ID
}

// open is the one rule: run it here, use it directly; otherwise ask stats who
// does and dial that one.
//
// A LOCAL VAULT IS NOT A CACHE, IT IS THE ABSENCE OF A HOP — and here it is
// also the absence of a secret on the wire.
func (v *vault) open() (holder, error) {
	if v.node.secrets != nil {
		return v.node.secrets.Local(), nil
	}
	return v.remote()
}

// Secret is the read on the request path.
func (v *vault) Secret(ctx context.Context, project, environment, secretID string) (string, error) {
	h, err := v.open()
	if err != nil {
		return "", err
	}
	return h.Secret(ctx, project, environment, secretID)
}

// Set, Delete and List are the developer's half, reached from the build
// frontend. They are the one write in v2 that is not a git push, and the
// frontend authorizes them exactly as it authorizes a deploy.
func (v *vault) Set(ctx context.Context, project, environment, name string, value []byte) error {
	h, err := v.open()
	if err != nil {
		return err
	}
	return h.Set(ctx, project, environment, name, value)
}

func (v *vault) Delete(ctx context.Context, project, environment, name string) error {
	h, err := v.open()
	if err != nil {
		return err
	}
	return h.Delete(ctx, project, environment, name)
}

func (v *vault) List(ctx context.Context, project, environment string) ([]string, error) {
	h, err := v.open()
	if err != nil {
		return nil, err
	}
	return h.List(ctx, project, environment)
}

// remote finds a vault in the fleet and keeps the stream. Looked up per call
// and cached by peer, not resolved once at construction: services start
// concurrently, a vault can go, and which node answers is a routing question.
func (v *vault) remote() (*secretsService.Client, error) {
	n := v.node
	if n.stats == nil {
		return nil, fmt.Errorf("%w: this node runs neither a secrets service nor stats to find one", ErrNoSecrets)
	}

	nodes := n.stats.Route(Secrets, n.cfg.Stats.Freshness())
	if len(nodes) == 0 {
		return nil, ErrNoSecrets
	}

	id, err := libp2ppeer.Decode(nodes[0].Peer)
	if err != nil {
		return nil, fmt.Errorf("%w: %q is not a peer", ErrNoSecrets, nodes[0].Peer)
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	if v.client != nil && v.at == id {
		return v.client, nil
	}
	if v.client != nil {
		v.client.Close()
	}

	c, err := secretsService.NewClient(n.opts.host, id)
	if err != nil {
		return nil, err
	}
	v.client, v.at = c, id
	return c, nil
}

// Close releases the stream to a remote vault, if one was opened.
func (v *vault) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.client != nil {
		v.client.Close()
		v.client = nil
	}
	return nil
}
