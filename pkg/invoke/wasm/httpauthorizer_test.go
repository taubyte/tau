package wasm_test

import (
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// httpauthorizer_test drives `taubyte:authorizer/http-authorizer` against a
// REAL component, for the reason every other world's test gives: a double
// would agree with whatever this package assumed.
//
// IT IS ALSO THE PROOF THE RUNTIME NEEDED NOTHING NEW. This world's request is
// the record requestValue already lowers for the handler, the router and the
// transformer, and its decision is the variant decisionFrom already lifts for
// the authorizer — so if wazy carries those two shapes it carries this world,
// and a green run here is what says so rather than an argument that it should.

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

// TestAnHttpAuthorizerAnswersWithTheOpsAndNotAYesOrNo — the grant arm, and the
// reason the answer is a list: "may listen, may not send" is unsayable as a
// bool, and it is the decision an attachment actually needs.
func TestAnHttpAuthorizerGrantsTheOpsItChose(t *testing.T) {
	r := tier(t, doorman(t))

	admission, err := r.AuthorizeRequest(t.Context(), doormanSpec(), aRequest(nil))
	require.NoError(t, err)
	require.Empty(t, admission.Denied)
	require.Equal(t, []string{"subscribe"}, admission.Granted,
		"the guard granted one op and withheld the other, which a yes could not say")
}

// TestADenialCarriesAReasonForTheOperator — the other arm. The reason is not
// for the caller: composing what a caller sees is a transformer's job.
func TestAnHttpAuthorizerDeniesWithAReason(t *testing.T) {
	r := tier(t, doorman(t))

	admission, err := r.AuthorizeRequest(t.Context(), doormanSpec(),
		aRequest(map[string][]string{"X-Deny": {"not on the list"}}))
	require.NoError(t, err)
	require.Nil(t, admission.Granted)
	require.Equal(t, "not on the list", admission.Denied)
}

// TestTheWorldGateHoldsHere too: a handler instantiates perfectly well and
// would read a request as a request, answering with a response the caller
// would read as a decision.
func TestAuthorizeRequestRefusesAHandler(t *testing.T) {
	r := tier(t, handler(t))

	_, err := r.AuthorizeRequest(t.Context(), invoke.Spec{Name: "checkout", Artifact: "bafy"}, aRequest(nil))
	require.ErrorIs(t, err, wasm.ErrUnsupportedWorld)
	require.Contains(t, err.Error(), "http-authorizer", "and which world was wanted")
}
