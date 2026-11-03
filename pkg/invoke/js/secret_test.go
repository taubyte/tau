package js_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	js "github.com/taubyte/tau/pkg/invoke/js"
	"github.com/taubyte/tau/pkg/invoke/secret"
)

// reader answers a value per handle, "down" as an outage.
type reader map[string]string

func (v reader) SecretFor(_ context.Context, grant invoke.Grant) ([]byte, error) {
	switch value, ok := v[grant.Handle]; {
	case grant.Handle == "down":
		return nil, errors.New("no member answered")
	case !ok:
		return nil, fmt.Errorf("%w: %s", secret.ErrNotSet, grant.Handle)
	default:
		return []byte(value), nil
	}
}

// vaultWorker reads the secret its path names, as jco's bindings would: the value
// on success, a ComponentError carrying the WIT's error otherwise.
const vaultWorker = `import { get } from "taubyte:secret/vault";
import * as versionless from "taubyte:secret/vault";

export default {
  fetch(request) {
    const label = request.url.slice(request.url.lastIndexOf("/") + 1);
    if (versionless.get !== get) {
      return new Response("two modules", { status: 500 });
    }
    try {
      const value = get(label);
      return new Response((value instanceof Uint8Array) + ":" + new TextDecoder().decode(value));
    } catch (e) {
      return new Response(e.constructor.name + ":" + e.payload.tag + ":" + (e.payload.val ?? ""), { status: 403 });
    }
  },
};`

func TestAFunctionReadsItsSecretThroughTheVaultModule(t *testing.T) {
	f := newFixtureFiles(t, map[string]string{"worker.js": vaultWorker}, js.WithSecrets(reader{"h1": "sk_live_1"}))
	f.spec.Reaches = []invoke.Grant{
		{Kind: "secret", Label: "stripe", Handle: "h1", Ops: []string{"get"}},
		{Kind: "secret", Label: "unset", Handle: "h2", Ops: []string{"get"}},
		{Kind: "secret", Label: "down", Handle: "down", Ops: []string{"get"}},
		{Kind: "secret", Label: "aws", Handle: "h1", Ops: []string{"get"}, Refused: "this cloud does not serve that store"},
	}
	inst := f.start()

	type testCase struct {
		label  string
		status int
		body   string
	}
	for _, tc := range []testCase{
		{"stripe", 200, "true:sk_live_1"},
		{"unset", 403, "ComponentError:not-set:"},
		{"down", 403, "ComponentError:unavailable:no member answered"},
		{"aws", 403, "ComponentError:denied:this cloud does not serve that store"},
		{"nothing", 403, "ComponentError:no-such-secret:nothing"},
	} {
		resp := serve(t, inst, get("/"+tc.label))
		require.Equal(t, tc.status, resp.Status, tc.label)
		require.Equal(t, tc.body, string(resp.Body), tc.label)
	}
}

func TestEachRealmReadsOnlyItsOwnGrants(t *testing.T) {
	f := newFixtureFiles(t, map[string]string{"worker.js": vaultWorker}, js.WithSecrets(reader{"h1": "one", "h2": "two"}))
	first, second := f.spec, f.spec
	first.Reaches = []invoke.Grant{{Kind: "secret", Label: "key", Handle: "h1", Ops: []string{"get"}}}
	second.Name, second.Reaches = "shop/other", []invoke.Grant{{Kind: "secret", Label: "key", Handle: "h2", Ops: []string{"get"}}}

	a, err := f.tier.Start(t.Context(), first)
	require.NoError(t, err)
	t.Cleanup(func() { a.Close() })
	b, err := f.tier.Start(t.Context(), second)
	require.NoError(t, err)
	t.Cleanup(func() { b.Close() })

	require.Equal(t, "true:one", string(serve(t, a, get("/key")).Body))
	require.Equal(t, "true:two", string(serve(t, b, get("/key")).Body))
}

func TestAGrantedSecretWithNoVaultIsRefusedAtStart(t *testing.T) {
	f := newFixtureFiles(t, map[string]string{"worker.js": vaultWorker})
	f.spec.Reaches = []invoke.Grant{{Kind: "secret", Label: "stripe", Handle: "h1", Ops: []string{"get"}}}
	_, err := f.tier.Start(t.Context(), f.spec)
	require.ErrorIs(t, err, js.ErrNoSecrets)
}

func TestAFunctionGrantedNoSecretStillImportsTheVault(t *testing.T) {
	inst := newFixtureFiles(t, map[string]string{"worker.js": vaultWorker}).start()
	resp := serve(t, inst, get("/stripe"))
	require.Equal(t, 403, resp.Status)
	require.Equal(t, "ComponentError:no-such-secret:stripe", string(resp.Body))
}
