package secrets

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/invoke"
	invokesecret "github.com/taubyte/tau/pkg/invoke/secret"
	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
)

// vaultNode is the instance one secrets node runs on: its peer, its directory, the
// service once started.
type vaultNode struct {
	peer.Node
	*service.Served
	root   string
	config func(any) error

	mu      sync.Mutex
	running []service.Running
}

func (v *vaultNode) Peer() peer.Node { return v.Node }
func (v *vaultNode) Running() []service.Running {
	v.mu.Lock()
	defer v.mu.Unlock()
	return slices.Clone(v.running)
}

func (v *vaultNode) runs(name string, s any) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.running = append(v.running, service.Running{Name: name, Service: s})
}
func (v *vaultNode) Root() string  { return v.root }
func (v *vaultNode) Cloud() string { return "test" }
func (*vaultNode) Region() string  { return "" }
func (*vaultNode) Now() time.Time  { return time.Now() }
func (v *vaultNode) Config(into any) error {
	if v.config != nil {
		return v.config(into)
	}
	return nil
}

var _ service.Host = (*vaultNode)(nil)

func TestAVaultIsWrittenBeforeItServesAndReadThroughItsHost(t *testing.T) {
	ctx := t.Context()
	p := peer.Mock(ctx)
	t.Cleanup(p.Close)
	h := &vaultNode{Node: p, Served: service.NewServed(p.ID(), nil), root: t.TempDir()}

	ref := Ref{Scope: Cloud("auth"), Name: "pepper"}
	sealed, err := Sealed(ref, []byte("salt"), time.Now())
	require.NoError(t, err)
	bundle := &secretsv2.InitBundle{Secrets: []*secretsv2.InitSecret{sealed}}
	require.ErrorIs(t, Init(ctx, p, "", Config{}, bundle), ErrNoRoot)
	require.NoError(t, Init(ctx, p, h.root, Config{}, bundle))

	svc := New()
	t.Cleanup(func() { svc.Close() })
	require.NoError(t, svc.Start(ctx, h))
	h.runs(Descriptor.Name, svc)

	vault := VaultOn(h)
	t.Cleanup(func() { vault.Close() })
	cloud := vault.Cloud("auth")
	got, err := cloud.Get(ctx, "pepper")
	require.NoError(t, err)
	require.Equal(t, "salt", string(got))
	require.NoError(t, cloud.Set(ctx, "signing", []byte("key")))
	names, err := cloud.List(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"pepper", "signing"}, names)

	// A PROJECT'S HALF: the developer's writes, a run's read, a guest's grant.
	require.NoError(t, vault.Set(ctx, "shop", "prod", "dbpass", []byte("one")))
	require.NoError(t, vault.Set(ctx, "shop", "prod", "dbpass", []byte("two")))
	value, err := vault.Secret(ctx, "shop", "prod", "dbpass")
	require.NoError(t, err)
	require.Equal(t, "two", value)
	previous, _, err := vault.Previous(ctx, "shop", "prod", "dbpass")
	require.NoError(t, err)
	require.Equal(t, "one", previous)
	listed, err := vault.List(ctx, "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, []string{"dbpass"}, listed)

	granted, err := vault.SecretFor(ctx, invoke.Grant{Handle: "shop/prod/dbpass"})
	require.NoError(t, err)
	require.Equal(t, "two", string(granted))
	_, err = vault.SecretFor(ctx, invoke.Grant{Handle: "shop/prod/nothing"})
	require.ErrorIs(t, err, invokesecret.ErrNotSet, "the module's arm")
	_, err = vault.SecretFor(ctx, invoke.Grant{Handle: "shop"})
	require.Error(t, err, "not a secret's address")

	require.NoError(t, vault.Delete(ctx, "shop", "prod", "dbpass"))
	_, err = vault.Secret(ctx, "shop", "prod", "dbpass")
	require.ErrorIs(t, err, ErrNotSet)
}

func TestAVaultWithNobodyToAskSaysSo(t *testing.T) {
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	vault := VaultOn(&vaultNode{Node: p})
	t.Cleanup(func() { vault.Close() })
	_, err := vault.Secret(t.Context(), "shop", "prod", "dbpass")
	require.ErrorIs(t, err, ErrNoSecrets)
}

func TestSecretsRefusesWhatItCannotStartWith(t *testing.T) {
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	require.ErrorIs(t, New().Start(t.Context(), &vaultNode{Node: p}), ErrNoRoot, "no directory for its shares")
	require.ErrorContains(t, New().Start(t.Context(), &vaultNode{Node: p, root: t.TempDir(), config: func(any) error {
		return errors.New("no such key")
	}}), "no such key")
	require.NoError(t, New().Close(), "never built, nothing to stop")
}
