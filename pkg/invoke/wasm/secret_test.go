package wasm_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// held is a vault, as this tier sees one.
type held struct {
	mu     sync.Mutex
	values map[string][]byte
	err    error
	reads  int
}

func newHeld() *held { return &held{values: map[string][]byte{}} }

func (h *held) SecretFor(_ context.Context, grant invoke.Grant) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reads++
	if h.err != nil {
		return nil, h.err
	}
	value, ok := h.values[grant.Handle]
	if !ok {
		return nil, fmt.Errorf("%w: %s", wasm.ErrNotSet, grant.Handle)
	}
	return value, nil
}

func (h *held) set(handle string, value []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.values[handle] = value
}

func (h *held) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reads
}

func secretGrant(label, handle string, ops ...string) []invoke.Grant {
	return []invoke.Grant{{Kind: "secret", Label: label, Handle: handle, Ops: ops}}
}

func TestAComponentReadsASecret(t *testing.T) {
	vault := newHeld()
	vault.set("shop/prod/QmStripe", []byte("sk_live"))

	instance := start(t, tier(t, cacher(t), wasm.WithSecrets(vault)), invoke.Spec{
		Name: "fn", Reaches: secretGrant("stripe", "shop/prod/QmStripe", "get"),
	})

	res := call(t, instance, "/secret/stripe/", nil)
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t, "sk_live", string(res.Body))
}

func TestARotatedSecretIsSeenWithoutARestart(t *testing.T) {
	vault := newHeld()
	vault.set("h", []byte("old"))

	instance := start(t, tier(t, cacher(t), wasm.WithSecrets(vault)), invoke.Spec{
		Name: "fn", Reaches: secretGrant("stripe", "h", "get"),
	})

	require.Equal(t, "old", string(call(t, instance, "/secret/stripe/", nil).Body))

	vault.set("h", []byte("new"))

	require.Equal(t, "new", string(call(t, instance, "/secret/stripe/", nil).Body),
		"the same instance, the new value")
	require.Equal(t, 2, vault.count(), "it is not cached in the tier")
}

func TestASecretThatIsNotSetIsNotAnEmptyString(t *testing.T) {
	vault := newHeld()
	instance := start(t, tier(t, cacher(t), wasm.WithSecrets(vault)), invoke.Spec{
		Name: "fn", Reaches: secretGrant("stripe", "h", "get"),
	})

	res := call(t, instance, "/secret/stripe/", nil)
	require.Equal(t, 403, res.Status)
	require.Equal(t, "not-set", string(res.Body))
}

func TestAnUndeclaredSecretIsRefused(t *testing.T) {
	vault := newHeld()
	vault.set("h", []byte("sk_live"))

	instance := start(t, tier(t, cacher(t), wasm.WithSecrets(vault)), invoke.Spec{
		Name: "fn", Reaches: secretGrant("stripe", "h", "get"),
	})

	res := call(t, instance, "/secret/somebody-elses/", nil)
	require.Equal(t, 403, res.Status)
	require.Equal(t, "no-such-secret:somebody-elses", string(res.Body))
	require.Zero(t, vault.count(), "nothing was read")
}

func TestNoFailureCarriesTheValue(t *testing.T) {
	vault := newHeld()
	vault.err = errors.New("reading shop/prod/QmStripe: the swarm is unreachable")

	instance := start(t, tier(t, cacher(t), wasm.WithSecrets(vault)), invoke.Spec{
		Name: "fn", Reaches: secretGrant("stripe", "h", "get"),
	})

	res := call(t, instance, "/secret/stripe/", nil)
	require.Equal(t, 403, res.Status)
	require.Contains(t, string(res.Body), "unavailable:")
	require.NotContains(t, string(res.Body), "sk_")
}

func TestATierWithNoSecretsRefusesAWorkloadThatReadsOne(t *testing.T) {
	_, err := tier(t, cacher(t)).Start(t.Context(), invoke.Spec{
		Name: "fn", Artifact: "bafy", Reaches: secretGrant("stripe", "h", "get"),
	})
	require.ErrorIs(t, err, wasm.ErrNoSecrets)
}

func TestTheInterfaceIsOneRead(t *testing.T) {
	vault := newHeld()
	vault.set("h", []byte("v"))

	// A grant carrying a verb the kind does not have buys nothing.
	instance := start(t, tier(t, cacher(t), wasm.WithSecrets(vault)), invoke.Spec{
		Name: "fn", Reaches: secretGrant("stripe", "h", "put"),
	})

	res := call(t, instance, "/secret/stripe/", nil)
	require.Equal(t, 403, res.Status)
	require.Equal(t, "denied:stripe cannot get", string(res.Body))
}
