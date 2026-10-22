package node

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	"github.com/taubyte/tau/pkg/specs"
	secretsService "github.com/taubyte/tau/services/secrets"
)

// secrets.go wires the one store in v2 that git does not write.
//
// It is locate.go's rule again — run it here, use it directly; otherwise ask
// discovery who does — and the only thing that makes it its own file is that the
// consumer is an interface services/worker declares rather than one this
// package does.

// ErrNoSecrets is a node that needs a vault and can find none.
//
// IT IS A REFUSAL AND NOT AN EMPTY VALUE, which is the same rule the vault
// itself keeps: a workload started without its database password fails
// somewhere else, later, as an application that looks broken.
var ErrNoSecrets = errors.New("node: no secrets service is reachable")

// holder is the vault as every caller on this node sees it.
type holder = Vault

// vault resolves a secret for whichever of this node's services needs one.
//
// It satisfies services/worker's Secrets interface — the read on the request
// path — and services/build/frontend's Vault, which is the write. The write
// half is deliberately NOT the read half's mirror: the frontend's interface has
// no Get, because nothing on a public ingress reads a value back.
//
// IT IS THE SECRETS SERVICE'S OWN CLIENT, on every node alike: a write is
// encrypted here before it leaves, and a read is opened here from a threshold
// of members' partial decryptions, so no member — this node's own included,
// when it runs one — is handed a plaintext.
type vault struct {
	client *secretsService.Client
}

// newVault is the only way to make one. Who runs the service is asked at
// CALL time, so a vault made during composition finds services that start
// after it.
func newVault(n *Node) *vault {
	client, err := secretsService.NewClient(n.opts.host,
		func() []libp2ppeer.ID { return (&fromDiscovery{node: n, name: Secrets}).Known() },
		func() *secretsService.Service { return n.secrets })
	if err != nil {
		// NewClient refuses only a nil host, and New refused that first.
		panic(err)
	}
	return &vault{client: client}
}

// absent turns a vault with nobody to ask into this node's own refusal.
func absent(err error) error {
	if errors.Is(err, secretsService.ErrNoMembers) {
		return fmt.Errorf("%w: %w", ErrNoSecrets, err)
	}
	return err
}

// Secret is the read on the request path.
func (v *vault) Secret(ctx context.Context, project, environment, secretID string) (string, error) {
	value, err := v.client.Secret(ctx, project, environment, secretID)
	return value, absent(err)
}

// Set, Delete and List are the developer's half, reached from the build
// frontend. They are the one write in v2 that is not a git push, and the
// frontend authorizes them exactly as it authorizes a deploy.
func (v *vault) Set(ctx context.Context, project, environment, name string, value []byte) error {
	return absent(v.client.Set(ctx, projectRef(project, environment, name), value))
}

func (v *vault) Delete(ctx context.Context, project, environment, name string) error {
	return absent(v.client.Delete(ctx, projectRef(project, environment, name)))
}

func (v *vault) List(ctx context.Context, project, environment string) ([]string, error) {
	names, err := v.client.List(ctx, secretsService.Project(project, environment))
	return names, absent(err)
}

func projectRef(project, environment, name string) secretsService.Ref {
	return secretsService.Ref{Scope: secretsService.Project(project, environment), Name: name}
}

// cloud is one of the cloud's own services' scope of the vault: auth's keys,
// cm's. No project can name it, so no developer's write lands there.
func (v *vault) cloud(service string) cloudVault {
	return cloudVault{client: v.client, scope: secretsService.Cloud(service)}
}

type cloudVault struct {
	client *secretsService.Client
	scope  secretsService.Scope
}

func (c cloudVault) Get(ctx context.Context, name string) ([]byte, error) {
	value, err := c.client.Get(ctx, secretsService.Ref{Scope: c.scope, Name: name})
	return value, absent(err)
}

func (c cloudVault) Set(ctx context.Context, name string, value []byte) error {
	return absent(c.client.Set(ctx, secretsService.Ref{Scope: c.scope, Name: name}, value))
}

func (c cloudVault) List(ctx context.Context) ([]string, error) {
	names, err := c.client.List(ctx, c.scope)
	return names, absent(err)
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

// ErrNoSecretsHere is an init on a node whose config runs no secrets service.
var ErrNoSecretsHere = errors.New("node: this node runs no secrets service to initialize")

// secretsOptions are what every secrets service on this node is opened with,
// serving or not, so init writes the store the service later reads.
func secretsOptions(cfg Config, root string) []secretsService.Option {
	return []secretsService.Option{
		secretsService.WithRoot(filepath.Join(root, Secrets)),
		secretsService.WithConfig(cfg.Secrets),
	}
}

// InitSecrets writes a sealed bundle into the vault of a node that is not
// running: its own database and share store, through a service that never
// serves. No services named is all of them, secrets among them. See
// services/secrets' Init for what it writes.
func InitSecrets(ctx context.Context, cfg Config, root string, h host.Host, databases kvdb.Factory, bundle *secretsv2.InitBundle) error {
	if len(cfg.Services) > 0 && !cfg.runs(Secrets) {
		return ErrNoSecretsHere
	}
	if root == "" {
		return secretsService.ErrNoRoot
	}

	db, err := databases.New(logger, Secrets, rebroadcastSeconds)
	if err != nil {
		return err
	}
	defer db.Close()

	svc, err := secretsService.New(h, db, secretsOptions(cfg, root)...)
	if err != nil {
		return err
	}
	return errors.Join(svc.Init(ctx, bundle), svc.Close())
}

// Close releases the streams the vault opened.
func (v *vault) Close() error { return v.client.Close() }
