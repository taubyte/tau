package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/taubyte/tau/services/auth/token"
)

// keys.go is the key material auth holds in the secrets service: each node's
// token-signing key and password pepper, and the operator's OAuth client
// secrets.
//
// NO KEY HERE IS ONE TWO NODES WRITE. A shared signing key or pepper would be
// a contended key — two nodes booting together would each generate one, and
// last-write-wins would quietly drop the loser's, invalidating every token and
// hash made with it. So each node generates its OWN, under a name carrying its
// peer id and the time it was made, and says which one it used: a token names
// its key by kid, a hash its pepper by name. Any node verifies with any key,
// fetching one it has not seen yet, once, and keeping it.
//
// A node's public key is published in auth's store as well, so a verifier
// finds it without opening a secret.

// Vault is the part of the secrets client auth uses.
type Vault interface {
	Get(ctx context.Context, project, environment, name string) ([]byte, error)
	Set(ctx context.Context, project, environment, name string, value []byte) error
	List(ctx context.Context, project, environment string) ([]string, error)
}

// The scope every auth secret lives in. A project id is a CID and never
// starts with a dot, and the frontend's secret routes reach only projects the
// register holds, so no developer's write can land here.
const (
	VaultProject     = ".tau"
	VaultEnvironment = "auth"
)

// Secret names. A host or provider name never holds a "/", which the vault
// refuses.
const (
	secretSigning = "signing-"
	secretPepper  = "pepper-"
	secretOAuth   = "oauth-"
	secretOIDC    = "oidc-"
)

// OAuthSecretName is the vault name an operator stores a forge OAuth app's
// client secret under, in project ".tau", environment "auth".
func OAuthSecretName(host string) string { return secretOAuth + host }

// OIDCSecretName is the same for an OIDC provider.
func OIDCSecretName(provider string) string { return secretOIDC + provider }

// keyring caches what it read.
type keyring struct {
	vault Vault
	store store
	node  string
	now   func() int64

	mu      sync.Mutex
	signer  *token.Signer
	pepper  string // this node's pepper's name
	peppers map[string][]byte
	secrets map[string][]byte
}

func newKeyring(v Vault, st store, node string, now func() int64) *keyring {
	return &keyring{vault: v, store: st, node: node, now: now, peppers: map[string][]byte{}, secrets: map[string][]byte{}}
}

// own returns this node's newest secret named prefix+node+"-"+n, creating one
// with mint when there is none.
func (k *keyring) own(ctx context.Context, prefix string, mint func() ([]byte, error)) (string, []byte, error) {
	names, err := k.vault.List(ctx, VaultProject, VaultEnvironment)
	if err != nil {
		return "", nil, fmt.Errorf("auth: listing auth's secrets: %w", err)
	}
	mine := prefix + k.node + "-"
	var newest string
	var newestAt int64 = -1
	for _, n := range names {
		at, err := strconv.ParseInt(strings.TrimPrefix(n, mine), 10, 64)
		if !strings.HasPrefix(n, mine) || err != nil {
			continue
		}
		if at > newestAt {
			newest, newestAt = n, at
		}
	}
	if newest != "" {
		v, err := k.vault.Get(ctx, VaultProject, VaultEnvironment, newest)
		if err != nil {
			return "", nil, fmt.Errorf("auth: reading %s: %w", newest, err)
		}
		return newest, v, nil
	}
	v, err := mint()
	if err != nil {
		return "", nil, err
	}
	name := mine + strconv.FormatInt(k.now(), 10)
	if err := k.vault.Set(ctx, VaultProject, VaultEnvironment, name, v); err != nil {
		return "", nil, fmt.Errorf("auth: storing %s: %w", name, err)
	}
	return name, v, nil
}

// Signer is this node's token signer, made and published on first use.
func (k *keyring) Signer(ctx context.Context, issuer string) (*token.Signer, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.signer != nil {
		return k.signer, nil
	}
	_, seed, err := k.own(ctx, secretSigning, func() ([]byte, error) {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		return priv.Seed(), nil
	})
	if err != nil {
		return nil, err
	}
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New("auth: this node's signing key is not an ed25519 seed")
	}
	priv := ed25519.NewKeyFromSeed(seed)
	s, err := token.NewSigner(issuer, priv)
	if err != nil {
		return nil, err
	}
	// PUBLISHED BEFORE ANYTHING IS SIGNED, so no token names a kid no node
	// can find. The row is immutable once written: its key is the kid.
	if err := k.store.put(ctx, prefixKeys+s.KeyID(), keyRow{Public: priv.Public().(ed25519.PublicKey), Node: k.node, Created: k.now()}); err != nil {
		return nil, err
	}
	k.signer = s
	return s, nil
}

// PublicKey resolves a kid from the store.
func (k *keyring) PublicKey(ctx context.Context, kid string) (ed25519.PublicKey, error) {
	var row keyRow
	if err := k.store.get(ctx, prefixKeys+kid, &row); err != nil {
		return nil, err
	}
	return row.Public, nil
}

// Pepper is this node's pepper and its name, for a new hash.
func (k *keyring) Pepper(ctx context.Context) (string, []byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.pepper != "" {
		return k.pepper, k.peppers[k.pepper], nil
	}
	name, v, err := k.own(ctx, secretPepper, func() ([]byte, error) {
		b := make([]byte, 32)
		_, err := rand.Read(b)
		return b, err
	})
	if err != nil {
		return "", nil, err
	}
	k.pepper, k.peppers[name] = name, v
	return name, v, nil
}

// PepperNamed is the pepper a stored hash names, from any node.
func (k *keyring) PepperNamed(ctx context.Context, name string) ([]byte, error) {
	if !strings.HasPrefix(name, secretPepper) {
		return nil, fmt.Errorf("auth: %q is not a pepper", name)
	}
	return k.cached(ctx, k.peppers, name)
}

// Secret is an operator's secret by name, read once.
func (k *keyring) Secret(ctx context.Context, name string) ([]byte, error) {
	return k.cached(ctx, k.secrets, name)
}

func (k *keyring) cached(ctx context.Context, m map[string][]byte, name string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if v, ok := m[name]; ok {
		return slices.Clone(v), nil
	}
	v, err := k.vault.Get(ctx, VaultProject, VaultEnvironment, name)
	if err != nil {
		return nil, fmt.Errorf("auth: reading %s from the secrets service: %w", name, err)
	}
	m[name] = v
	return slices.Clone(v), nil
}
