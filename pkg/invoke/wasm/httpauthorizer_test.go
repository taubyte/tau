package wasm_test

import (
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// Against a real component. The request record and decision variant are the ones
// already used by other worlds, so a green run shows wazy needs nothing new.

// doorman is the fixture: it denies when asked to by header and otherwise
// grants a PARTIAL set — `subscribe` and never `publish`.
func doorman(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile("fixtures/doorman.wasm")
	require.NoError(t, err, "run `cargo component build --release` in fixtures/doorman")
	return code
}

func doormanSpec() invoke.Spec {
	return invoke.Spec{
		Name:     "doorman",
		Artifact: "bafydoorman",
		Settings: map[string]string{wasm.SettingImplements: wasm.WorldHTTPAuthorizer},
	}
}

func aRequest(headers map[string][]string) *invoke.Request {
	return &invoke.Request{
		Method:  http.MethodGet,
		Path:    "/",
		Headers: headers,
		Body:    []byte("body"),
	}
}

func TestAnHttpAuthorizerGrantsTheOpsItChose(t *testing.T) {
	r := tier(t, doorman(t))

	admission, err := r.AuthorizeRequest(t.Context(), doormanSpec(), aRequest(nil))
	require.NoError(t, err)
	require.Empty(t, admission.Denied)
	require.Equal(t, []string{"subscribe"}, admission.Granted,
		"the guard granted one op and withheld the other, which a yes could not say")
}

func TestAnHttpAuthorizerDeniesWithAReason(t *testing.T) {
	r := tier(t, doorman(t))

	admission, err := r.AuthorizeRequest(t.Context(), doormanSpec(),
		aRequest(map[string][]string{"X-Deny": {"not on the list"}}))
	require.NoError(t, err)
	require.Nil(t, admission.Granted)
	require.Equal(t, "not on the list", admission.Denied)
}

func TestAuthorizeRequestRefusesAHandler(t *testing.T) {
	r := tier(t, handler(t))

	_, err := r.AuthorizeRequest(t.Context(), invoke.Spec{Name: "checkout", Artifact: "bafy"}, aRequest(nil))
	require.ErrorIs(t, err, wasm.ErrUnsupportedWorld)
	require.Contains(t, err.Error(), "http-authorizer", "and which world was wanted")
}
