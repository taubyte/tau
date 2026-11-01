package wasm_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// Every test runs a real authorizer built from wit/deps/taubyte-authorizer, so the
// host's lowering is shown to agree with generated bindings.

// guard is the fixture. It grants the op it was asked about plus one synthetic
// entry encoding the whole question, refuses `forbidden` with an empty grant,
// and declines to decide about `undecidable`.
func guard(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile("fixtures/guard.wasm")
	require.NoError(t, err, "run `cargo component build --release` in fixtures/guard")
	return code
}

func guardSpec() invoke.Spec {
	return invoke.Spec{
		Name:     "keeper",
		Artifact: "bafyguard",
		Settings: map[string]string{wasm.SettingImplements: wasm.WorldAuthorizer},
	}
}

func aQuestion() wasm.Question {
	return wasm.Question{
		Who: wasm.Caller{
			Project: "shop", Environment: "prod", Kind: "function", Name: "checkout",
			Scope: []string{"shop", "local"},
		},
		What: wasm.Target{Kind: "cache", Label: "orders", Instance: "room-42"},
		Op:   "get",
	}
}

func TestTheWholeQuestionReachesTheGuest(t *testing.T) {
	r := tier(t, guard(t))

	decision, err := r.Authorize(t.Context(), guardSpec(), aQuestion())
	require.NoError(t, err)
	require.Empty(t, decision.Undecided)

	var saw string
	for _, op := range decision.Granted {
		if strings.HasPrefix(op, "saw:") {
			saw = op
		}
	}
	require.Equal(t, "saw:shop/prod/function/checkout/[shop,local]:cache/orders/room-42", saw,
		"the caller, its scope, the target and the instance all crossed intact")
	require.Contains(t, decision.Granted, "get", "and the op it was asked about")
}

func TestARefusalIsAnEmptyGrantAndNotAnError(t *testing.T) {
	r := tier(t, guard(t))

	q := aQuestion()
	q.What.Label = "forbidden"

	decision, err := r.Authorize(t.Context(), guardSpec(), q)
	require.NoError(t, err, "a refusal is an answer")
	require.Empty(t, decision.Undecided)
	require.NotNil(t, decision.Granted, "decided, and it decided nothing")
	require.Empty(t, decision.Granted)
}

func TestAGuardThatDeclinesToDecideSaysSo(t *testing.T) {
	r := tier(t, guard(t))

	q := aQuestion()
	q.What.Label = "undecidable"

	decision, err := r.Authorize(t.Context(), guardSpec(), q)
	require.NoError(t, err)
	require.Nil(t, decision.Granted)
	require.Contains(t, decision.Undecided, "no opinion")
}

func TestAuthorizeRefusesAHandler(t *testing.T) {
	r := tier(t, handler(t))

	_, err := r.Authorize(t.Context(), invoke.Spec{Name: "checkout", Artifact: "bafy"}, aQuestion())
	require.ErrorIs(t, err, wasm.ErrUnsupportedWorld)
	require.Contains(t, err.Error(), "handler")
	require.Contains(t, err.Error(), "authorizer", "and which world was wanted")
}

func TestStartStillRefusesAnAuthorizer(t *testing.T) {
	r := tier(t, guard(t))

	_, err := r.Start(t.Context(), guardSpec())
	require.ErrorIs(t, err, wasm.ErrUnsupportedWorld)
}

// asked records what a guard was asked and answers with what it was told to.
type asked struct {
	ops  []string
	err  error
	saw  []string
	seen int
}

func (a *asked) Ops(_ context.Context, spec invoke.Spec, grant invoke.Grant, op string) ([]string, error) {
	a.seen++
	a.saw = append(a.saw, spec.Label+"/"+grant.Label+"/"+op)
	return a.ops, a.err
}

// guarded is a cache grant whose target names a guard.
func guarded(label, handle string, ops ...string) []invoke.Grant {
	return []invoke.Grant{{
		Kind: "cache", Label: label, Handle: handle, Ops: ops,
		Settings: map[string]string{
			"authorizer/name":      "keeper",
			"authorizer/namespace": "/shop",
		},
	}}
}

func TestAGuardNarrowsWhatTheAccessBlockAsked(t *testing.T) {
	ask := &asked{ops: []string{"get"}}
	instance := start(t, tier(t, cacher(t), wasm.WithCaches(newCaches("h")), wasm.WithAuthorizers(ask)),
		invoke.Spec{Name: "fn", Label: "checkout", Kind: "function", Reaches: guarded("sessions", "h", "get", "put")})

	res := call(t, instance, "/put/sessions/a", []byte("1"))
	require.Contains(t, string(res.Body), "denied", "the guard did not grant put")

	require.Equal(t, 404, call(t, instance, "/get/sessions/a", nil).Status,
		"and get went through to the store, which has nothing at that key")
	require.Contains(t, ask.saw, "checkout/sessions/put", "the guard was asked about the op attempted")
}

func TestAGuardCannotGrantWhatTheConfigNeverAsked(t *testing.T) {
	ask := &asked{ops: []string{"get", "put", "delete"}}
	instance := start(t, tier(t, cacher(t), wasm.WithCaches(newCaches("h")), wasm.WithAuthorizers(ask)),
		invoke.Spec{Name: "fn", Label: "checkout", Kind: "function", Reaches: guarded("sessions", "h", "get")})

	res := call(t, instance, "/delete/sessions/a", nil)
	require.Contains(t, string(res.Body), "denied")
	require.NotContains(t, ask.saw, "checkout/sessions/delete",
		"the guard is never asked to widen, so it is not asked at all")
}

func TestAGuardThatCannotBeRunDenies(t *testing.T) {
	ask := &asked{err: errors.New("the guard is on fire")}
	instance := start(t, tier(t, cacher(t), wasm.WithCaches(newCaches("h")), wasm.WithAuthorizers(ask)),
		invoke.Spec{Name: "fn", Label: "checkout", Kind: "function", Reaches: guarded("sessions", "h", "get")})

	res := call(t, instance, "/get/sessions/a", nil)
	require.Contains(t, string(res.Body), "denied")
	require.Contains(t, string(res.Body), "on fire", "and says what went wrong")
}

func TestAGuardThatDidNotDecideDenies(t *testing.T) {
	ask := &asked{err: fmt.Errorf("%w: keeper had no opinion", wasm.ErrUndecided)}
	instance := start(t, tier(t, cacher(t), wasm.WithCaches(newCaches("h")), wasm.WithAuthorizers(ask)),
		invoke.Spec{Name: "fn", Label: "checkout", Kind: "function", Reaches: guarded("sessions", "h", "get")})

	res := call(t, instance, "/get/sessions/a", nil)
	require.Contains(t, string(res.Body), "denied")
	require.Contains(t, string(res.Body), "no opinion")
}

func TestAGuardedSpecNeedsSomethingToAsk(t *testing.T) {
	_, err := tier(t, cacher(t), wasm.WithCaches(newCaches("h"))).Start(t.Context(), invoke.Spec{
		Name: "fn", Artifact: "bafy", Reaches: guarded("sessions", "h", "get"),
	})
	require.ErrorIs(t, err, wasm.ErrNoAuthorizers)
	require.Contains(t, err.Error(), "sessions", "and names which grant")
}

func TestARefusedGuardedGrantDoesNotStopTheWorkload(t *testing.T) {
	dead := guarded("sessions", "h", "get")
	dead[0].Refused = "this cloud cannot serve it"

	_, err := tier(t, cacher(t), wasm.WithCaches(newCaches("h"))).Start(t.Context(), invoke.Spec{
		Name: "fn", Artifact: "bafy", Reaches: dead,
	})
	require.NoError(t, err, "nothing will ask about a grant nothing can use")
}

func TestAnUnguardedGrantAsksNobody(t *testing.T) {
	ask := &asked{ops: []string{"get"}}
	instance := start(t, tier(t, cacher(t), wasm.WithCaches(newCaches("h")), wasm.WithAuthorizers(ask)),
		invoke.Spec{Name: "fn", Reaches: grants("sessions", "h", "get")})

	require.Equal(t, 404, call(t, instance, "/get/sessions/a", nil).Status)
	require.Zero(t, ask.seen)
}
