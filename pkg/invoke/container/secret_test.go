package container

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/vault"
)

// held reads what a grant names and records which grant asked.
type held struct {
	values map[string]string
	asked  []string
}

func (h *held) SecretFor(_ context.Context, grant invoke.Grant) ([]byte, error) {
	h.asked = append(h.asked, grant.Handle)
	value, ok := h.values[grant.Label]
	if !ok {
		return nil, errors.New("not set")
	}
	return []byte(value), nil
}

func secretGrant(label string, ops ...string) invoke.Grant {
	return invoke.Grant{Kind: secretKind, Label: label, Handle: "shop/prod/" + label, Ops: ops}
}

// vaultMount is the vault a container was created with, or fails.
func vaultMount(t *testing.T, cfg *core.ContainerConfig) core.VolumeMount {
	t.Helper()
	for _, m := range cfg.Volumes {
		if m.Destination == vault.Dir {
			return m
		}
	}
	t.Fatalf("no vault at %s in %+v", vault.Dir, cfg.Volumes)
	return core.VolumeMount{}
}

// read is what the helper does inside the container.
func read(dir, label string) (int, string, error) {
	client := http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", filepath.Join(dir, vault.Socket))
		},
	}}
	res, err := client.Get("http://vault/v1/secret/" + label)
	if err != nil {
		return 0, "", err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	return res.StatusCode, string(body), err
}

func TestAWorkloadReadsWhatItIsGrantedAndNothingElse(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	secrets := &held{values: map[string]string{"stripe": "sk_live_1", "slack": "xoxb"}}
	e := newRuntime(t, rt, Image{Ref: "acme/shop:1"}, func() []Option { return []Option{WithSecrets(secrets)} })

	s := spec()
	s.Reaches = []invoke.Grant{
		secretGrant("stripe", "get"),
		secretGrant("slack"),
		{Kind: secretKind, Label: "aws", Ops: []string{"get"}, Refused: "this cloud does not serve that store"},
	}
	inst, err := e.Start(context.Background(), s)
	require.NoError(t, err)

	created, _, _ := rt.snapshot()
	m := vaultMount(t, created[0])
	require.True(t, m.ReadOnly)

	status, body, err := read(m.Source, "stripe")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "sk_live_1", body)
	require.Equal(t, []string{"shop/prod/stripe"}, secrets.asked, "read through the grant, by its handle")

	for label, says := range map[string]string{
		"slack":    "slack cannot get",
		"aws":      "this cloud does not serve that store",
		"database": `names no secret "database"`,
	} {
		status, body, err := read(m.Source, label)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status, label)
		require.Contains(t, body, says, label)
	}
	require.Len(t, secrets.asked, 1, "a refusal never reaches the node's vault")

	require.NoError(t, inst.Close())
	_, err = os.Stat(m.Source)
	require.ErrorIs(t, err, os.ErrNotExist, "the vault goes with the instance")
}

func TestAWorkloadGrantedNoSecretGetsNoVault(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	e := newRuntime(t, rt, Image{Ref: "acme/shop:1"}, func() []Option { return []Option{WithSecrets(&held{})} })

	inst, err := e.Start(context.Background(), spec())
	require.NoError(t, err)
	defer inst.Close()

	created, _, _ := rt.snapshot()
	require.Empty(t, created[0].Volumes)
}

func TestANodeWithNothingToReadWithSaysSo(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	var warnings []string
	e := newRuntime(t, rt, Image{Ref: "acme/shop:1"}, func() []Option {
		return []Option{WithWarn(func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) })}
	})

	s := spec()
	s.Reaches = []invoke.Grant{secretGrant("stripe", "get")}
	inst, err := e.Start(context.Background(), s)
	require.NoError(t, err)
	defer inst.Close()

	created, _, _ := rt.snapshot()
	status, body, err := read(vaultMount(t, created[0]).Source, "stripe")
	require.NoError(t, err)
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Contains(t, body, "this node has nothing to read a secret with")
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "(secret)")
}

func TestASecretAJobPrintsIsMaskedInItsFailure(t *testing.T) {
	rt := newFakeBackend()
	rt.run = func(cfg *core.ContainerConfig, _ []byte) (string, string, int) {
		for _, m := range cfg.Volumes {
			if m.Destination == vault.Dir {
				_, value, err := read(m.Source, "stripe")
				if err != nil {
					return "", err.Error(), 2
				}
				return "", "boom: the key " + value + " was rejected", 1
			}
		}
		return "", "no vault", 2
	}
	secrets := &held{values: map[string]string{"stripe": "sk_live_1"}}
	e := newRuntime(t, rt, Image{Ref: "acme/exporter:2"}, func() []Option { return []Option{WithSecrets(secrets)} })

	s := jobSpec()
	s.Reaches = []invoke.Grant{secretGrant("stripe", "get")}
	inst, err := e.Start(context.Background(), s)
	require.NoError(t, err)

	_, err = inst.Serve(context.Background(), &invoke.Request{})
	require.ErrorIs(t, err, ErrJobFailed)
	require.ErrorContains(t, err, "boom: the key *** was rejected")
	require.NotContains(t, err.Error(), "sk_live_1")

	created, _, _ := rt.snapshot()
	dir := vaultMount(t, created[0]).Source
	require.NoError(t, inst.Close())
	_, err = os.Stat(dir)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestAStartThatFailsTakesItsVaultWithIt(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	rt.startErr = errors.New("no room")
	e := newRuntime(t, rt, Image{Ref: "acme/shop:1"}, func() []Option { return []Option{WithSecrets(&held{})} })

	s := spec()
	s.Reaches = []invoke.Grant{secretGrant("stripe", "get")}
	_, err := e.Start(context.Background(), s)
	require.ErrorContains(t, err, "no room")

	rt.mu.Lock()
	cfg := rt.created[0]
	rt.mu.Unlock()
	_, err = os.Stat(vaultMount(t, cfg).Source)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestANilSecretsReaderIsRefused(t *testing.T) {
	_, err := New(ImageFromSpec, WithBackend(newFakeBackend()), WithSecrets(nil))
	require.ErrorContains(t, err, "a nil secrets reader")
}
