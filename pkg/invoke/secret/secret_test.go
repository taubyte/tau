package secret_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/samyfodil/warpjs/hostmod"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
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

func vaultOf(t *testing.T, spec invoke.Spec, r secret.Reader) hostmod.Func {
	t.Helper()
	reg := hostmod.New()
	require.NoError(t, secret.Register(reg, spec, r))
	in, err := reg.Interface(secret.Interface)
	require.NoError(t, err)
	require.Len(t, in.Funcs, 1)
	require.Equal(t, "get", in.Funcs[0].Name)
	return in.Funcs[0].Impl
}

func get(t *testing.T, spec invoke.Spec, r secret.Reader, label string) hostmod.Value {
	t.Helper()
	out, err := vaultOf(t, spec, r)(t.Context(), []hostmod.Value{label})
	require.NoError(t, err)
	require.Len(t, out, 1)
	return out[0]
}

// The cases of the WIT's error, in its order.
const (
	noSuchSecret uint32 = iota
	denied
	notSet
	unavailable
)

func fail(disc uint32, detail hostmod.Value) hostmod.Value {
	return hostmod.ResultValue{IsErr: true, Payload: hostmod.VariantValue{Disc: disc, Payload: detail}}
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
		want  hostmod.Value
	}
	for _, tc := range []testCase{
		{"stripe", hostmod.ResultValue{Payload: []byte("sk_live_1")}},
		{"unset", fail(notSet, nil)},
		{"down", fail(unavailable, errDown.Error())},
		{"put", fail(denied, "put cannot get")},
		{"aws", fail(denied, "this cloud does not serve that store")},
		{"sessions", fail(noSuchSecret, "sessions")},
	} {
		require.Equal(t, tc.want, get(t, spec, v, tc.label), tc.label)
	}

	require.Equal(t, fail(unavailable, "this node has nothing to read a secret with"), get(t, spec, nil, "stripe"))
}

func TestAnArgumentThatIsNotALabelTraps(t *testing.T) {
	_, err := vaultOf(t, invoke.Spec{}, vault{})(t.Context(), []hostmod.Value{uint32(1)})
	require.ErrorContains(t, err, "want string")
}

func TestARegistryARuntimeHoldsIsRefused(t *testing.T) {
	reg := hostmod.New()
	require.NoError(t, reg.Freeze())
	require.ErrorIs(t, secret.Register(reg, invoke.Spec{}, vault{}), hostmod.ErrFrozen)
}

func TestOnlySecretGrantsAreTheVaults(t *testing.T) {
	require.Nil(t, secret.GrantsOf(invoke.Spec{Reaches: []invoke.Grant{{Kind: "cache", Label: "c"}}}))
	require.Len(t, secret.GrantsOf(invoke.Spec{Reaches: []invoke.Grant{grant("a", "h"), grant("b", "h")}}), 2)
}
