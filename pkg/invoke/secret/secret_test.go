package secret_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/hostmodule"
	"github.com/taubyte/tau/pkg/invoke/secret"
)

type vault map[string]string

var errDown = errors.New("no member answered")

func (v vault) SecretFor(_ context.Context, grant invoke.Grant) ([]byte, error) {
	switch value, ok := v[grant.Handle]; {
	case grant.Handle == "down":
		return nil, errDown
	case !ok:
		return nil, fmt.Errorf("%w: %s", secret.ErrNotSet, grant.Handle)
	default:
		return []byte(value), nil
	}
}

func get(t *testing.T, spec invoke.Spec, r secret.Reader, label string) hostmodule.Value {
	t.Helper()
	m := secret.Module(spec, r)
	out, err := m.Funcs["get"](t.Context(), []hostmodule.Value{label})
	require.NoError(t, err)
	return out
}

func grant(label, handle string, ops ...string) invoke.Grant {
	return invoke.Grant{Kind: "secret", Label: label, Handle: handle, Ops: ops}
}

func TestEachArmOfARead(t *testing.T) {
	spec := invoke.Spec{Reaches: []invoke.Grant{
		grant("stripe", "h1", "get"),
		grant("unset", "h2", "get"),
		grant("down", "down", "get"),
		grant("put", "h1", "put"),
		{Kind: "secret", Label: "aws", Handle: "h1", Ops: []string{"get"}, Refused: "this cloud does not serve that store"},
		{Kind: "cache", Label: "sessions", Ops: []string{"get"}},
	}}
	v := vault{"h1": "sk_live_1"}

	type testCase struct {
		label string
		want  hostmodule.Value
	}
	for _, tc := range []testCase{
		{"stripe", hostmodule.Ok([]byte("sk_live_1"))},
		{"unset", hostmodule.Fail(hostmodule.Variant{Case: "not-set"})},
		{"down", hostmodule.Fail(hostmodule.Variant{Case: "unavailable", Payload: errDown.Error()})},
		{"put", hostmodule.Fail(hostmodule.Variant{Case: "denied", Payload: "put cannot get"})},
		{"aws", hostmodule.Fail(hostmodule.Variant{Case: "denied", Payload: "this cloud does not serve that store"})},
		{"sessions", hostmodule.Fail(hostmodule.Variant{Case: "no-such-secret", Payload: "sessions"})},
	} {
		require.Equal(t, tc.want, get(t, spec, v, tc.label), tc.label)
	}

	require.Equal(t,
		hostmodule.Fail(hostmodule.Variant{Case: "unavailable", Payload: "this node has nothing to read a secret with"}),
		get(t, spec, nil, "stripe"))
}

func TestAnArgumentThatIsNotALabelTraps(t *testing.T) {
	m := secret.Module(invoke.Spec{}, vault{})
	_, err := m.Funcs["get"](t.Context(), []hostmodule.Value{uint32(1)})
	require.ErrorContains(t, err, "want string")
}

func TestOnlySecretGrantsAreTheVaults(t *testing.T) {
	require.Nil(t, secret.GrantsOf(invoke.Spec{Reaches: []invoke.Grant{{Kind: "cache", Label: "c"}}}))
	require.Len(t, secret.GrantsOf(invoke.Spec{Reaches: []invoke.Grant{grant("a", "h"), grant("b", "h")}}), 2)
}
