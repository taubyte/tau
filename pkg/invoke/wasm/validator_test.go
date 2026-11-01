package wasm_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// A real instance validator.

func admitter(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile("fixtures/admitter.wasm")
	require.NoError(t, err, "run `cargo component build --release` in fixtures/admitter")
	return code
}

func admitterSpec() invoke.Spec {
	return invoke.Spec{
		Name:     "may_open",
		Artifact: "bafyadmitter",
		Settings: map[string]string{wasm.SettingImplements: wasm.WorldValidator},
	}
}

func anOpening(instance string) wasm.Opening {
	return wasm.Opening{
		Who: wasm.Caller{
			Project: "shop", Environment: "prod", Kind: "function", Name: "chat",
			Scope: []string{"shop", "local"},
		},
		What:     wasm.Store{Kind: "cache", Label: "sessions"},
		Instance: instance,
	}
}

func TestAValidatorAdmits(t *testing.T) {
	r := tier(t, admitter(t))

	verdict, err := r.Admit(t.Context(), admitterSpec(), anOpening("room-42"))
	require.NoError(t, err)
	require.True(t, verdict.Admitted)
	require.Empty(t, verdict.Refused)
	require.Empty(t, verdict.Undecided)
}

func TestAValidatorRefusesWithAReason(t *testing.T) {
	r := tier(t, admitter(t))

	verdict, err := r.Admit(t.Context(), admitterSpec(), anOpening("denied"))
	require.NoError(t, err, "a refusal is an answer")
	require.False(t, verdict.Admitted)
	require.Contains(t, verdict.Refused, "over its plan")
	require.Empty(t, verdict.Undecided)
}

func TestAValidatorThatDeclinesToDecideIsNotARefusal(t *testing.T) {
	r := tier(t, admitter(t))

	verdict, err := r.Admit(t.Context(), admitterSpec(), anOpening("unsure"))
	require.NoError(t, err)
	require.False(t, verdict.Admitted)
	require.Empty(t, verdict.Refused, "nobody was turned down")
	require.Contains(t, verdict.Undecided, "did not answer")
}

func TestTheWholeOpeningReachesTheGuest(t *testing.T) {
	r := tier(t, admitter(t))

	verdict, err := r.Admit(t.Context(), admitterSpec(), anOpening("echo"))
	require.NoError(t, err)
	require.Equal(t, "saw:shop/prod/function/chat/[shop,local]:cache/sessions", verdict.Refused)
}

func TestACallerThatIsNotAWorkloadLowersCleanly(t *testing.T) {
	r := tier(t, admitter(t))

	verdict, err := r.Admit(t.Context(), admitterSpec(), wasm.Opening{
		Who:      wasm.Caller{Project: "shop", Environment: "prod"},
		What:     wasm.Store{Kind: "bus", Label: "chat"},
		Instance: "echo",
	})
	require.NoError(t, err)
	require.Equal(t, "saw:shop/prod///[]:bus/chat", verdict.Refused,
		"the empty kind is what tells a validator this came from the internet")
}

func TestAdmitRefusesAHandler(t *testing.T) {
	r := tier(t, handler(t))

	_, err := r.Admit(t.Context(), invoke.Spec{Name: "checkout", Artifact: "bafy"}, anOpening("x"))
	require.ErrorIs(t, err, wasm.ErrUnsupportedWorld)
	require.Contains(t, err.Error(), "instance-validator", "and names the world that was wanted")
}

func TestAdmitRefusesAnAuthorizer(t *testing.T) {
	r := tier(t, guard(t))

	_, err := r.Admit(t.Context(), guardSpec(), anOpening("x"))
	require.ErrorIs(t, err, wasm.ErrUnsupportedWorld)
}

func TestAuthorizeRefusesAValidator(t *testing.T) {
	r := tier(t, admitter(t))

	_, err := r.Authorize(t.Context(), admitterSpec(), aQuestion())
	require.ErrorIs(t, err, wasm.ErrUnsupportedWorld)
}

func TestStartRefusesAValidator(t *testing.T) {
	r := tier(t, admitter(t))

	_, err := r.Start(t.Context(), admitterSpec())
	require.ErrorIs(t, err, wasm.ErrUnsupportedWorld)
}
