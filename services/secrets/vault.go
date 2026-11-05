package secrets

import (
	"context"
	"errors"
	"fmt"
	"time"

	peerCore "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/pkg/invoke"
	invokesecret "github.com/taubyte/tau/pkg/invoke/secret"
	"github.com/taubyte/tau/pkg/specs"
)

// ErrNoSecrets is a caller that needs a vault and can find no secrets service: a
// refusal, not an empty value.
var ErrNoSecrets = errors.New("secrets: no secrets service is reachable")

// Vault is the secrets service as the services on any instance read and write it:
// the one on this instance when it runs one, otherwise the members the directory
// beside it names. It is the service's own client, so writes are encrypted and
// reads opened here, and no member sees plaintext.
type Vault struct {
	client *Client
}

// VaultOn is the vault h's services reach. Who runs the service is asked at call
// time, so a vault made before the service starts finds it.
func VaultOn(h service.Host) *Vault {
	client, err := NewClient(h.Peer().Host(), on{h})
	if err != nil {
		// NewClient refuses only a nil host, and a Host always has one.
		panic(err)
	}
	return &Vault{client: client}
}

func (v *Vault) Close() error { return v.client.Close() }

// on is the service as the instance h finds it: the nodes its directory names,
// and the one running beside it.
type on struct{ h service.Host }

func (o on) Members() []peerCore.ID {
	if d, ok := service.Get[service.Directory](o.h); ok {
		return d.Peers(Descriptor.Name)
	}
	return nil
}

func (o on) beside() *server {
	s, _ := service.Get[*server](o.h)
	return s
}

// absent turns a vault with nobody to ask into the caller's own refusal.
func absent(err error) error {
	if errors.Is(err, ErrNoMembers) {
		return fmt.Errorf("%w: %w", ErrNoSecrets, err)
	}
	return err
}

// Secret is the read on the request path.
func (v *Vault) Secret(ctx context.Context, project, environment, secretID string) (string, error) {
	value, err := v.client.Secret(ctx, project, environment, secretID)
	return value, absent(err)
}

// Previous is the value a secret's newest version replaced, while the vault still
// keeps it.
func (v *Vault) Previous(ctx context.Context, project, environment, secretID string) (string, time.Time, error) {
	value, at, err := v.client.Previous(ctx, projectRef(project, environment, secretID))
	return string(value), at, absent(err)
}

// Set, Delete and List are the developer's half, reached from the build frontend,
// which authorizes them exactly as it authorizes a deploy.
func (v *Vault) Set(ctx context.Context, project, environment, name string, value []byte) error {
	return absent(v.client.Set(ctx, projectRef(project, environment, name), value))
}

func (v *Vault) Delete(ctx context.Context, project, environment, name string) error {
	return absent(v.client.Delete(ctx, projectRef(project, environment, name)))
}

func (v *Vault) List(ctx context.Context, project, environment string) ([]string, error) {
	names, err := v.client.List(ctx, Project(project, environment))
	return names, absent(err)
}

func projectRef(project, environment, name string) Ref {
	return Ref{Scope: Project(project, environment), Name: name}
}

// Cloud is one of the cloud's own services' scope of the vault: auth's keys, cm's.
// No project can name it, so no developer's write lands there.
func (v *Vault) Cloud(service string) CloudVault {
	return CloudVault{client: v.client, scope: Cloud(service)}
}

// CloudVault is one cloud service's secrets.
type CloudVault struct {
	client *Client
	scope  Scope
}

func (c CloudVault) Get(ctx context.Context, name string) ([]byte, error) {
	value, err := c.client.Get(ctx, Ref{Scope: c.scope, Name: name})
	return value, absent(err)
}

func (c CloudVault) Set(ctx context.Context, name string, value []byte) error {
	return absent(c.client.Set(ctx, Ref{Scope: c.scope, Name: name}, value))
}

func (c CloudVault) List(ctx context.Context) ([]string, error) {
	names, err := c.client.List(ctx, c.scope)
	return names, absent(err)
}

// SecretFor is pkg/invoke/wasm's Secrets: the value a grant names, read at the moment
// the guest asks rather than at start.
//
// IT IS THE SAME VAULT a worker resolves a run-time variable from, which is what
// makes the two paths agree: a rotated secret reaches a variable at the next deploy
// and reaches this at the next call, and both read the same row.
func (v *Vault) SecretFor(ctx context.Context, grant invoke.Grant) ([]byte, error) {
	project, environment, id := specs.SplitHandle(grant.Handle)
	if project == "" || environment == "" || id == "" {
		return nil, fmt.Errorf("secrets: %q does not address a secret", grant.Handle)
	}
	value, err := v.Secret(ctx, project, environment, id)
	if err != nil {
		if errors.Is(err, ErrNotSet) {
			// THE MODULE'S ARM, so a guest is told "nothing is set" rather than
			// "the node is unhappy".
			return nil, fmt.Errorf("%w: %w", invokesecret.ErrNotSet, err)
		}
		return nil, err
	}
	return []byte(value), nil
}
