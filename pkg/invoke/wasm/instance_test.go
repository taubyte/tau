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

// instance_test is the vocabulary's `instances:` block finally meaning
// something: a store that holds many, an instance named in the label, and a
// function that decides whether it may exist.

// admitting records what it was asked and answers with what a test set.
type admitting struct {
	mu     sync.Mutex
	asked  []string
	refuse error
}

func (a *admitting) Admit(_ context.Context, grant invoke.Grant, instance string) error {
	a.mu.Lock()
	a.asked = append(a.asked, grant.Label+"/"+instance)
	err := a.refuse
	a.mu.Unlock()
	return err
}

func (a *admitting) seen() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.asked...)
}

// manyGrant is a cache that holds many instances, as the composer writes one.
func manyGrant(label, handle string, ops ...string) invoke.Grant {
	return invoke.Grant{
		Kind: "cache", Label: label, Handle: handle, Ops: ops,
		Settings: map[string]string{
			"instances/admission":      "validator",
			"instances/validator/name": "may_open",
			"instances/limit":          "5000",
		},
	}
}

// instanced is a tier over a cache backing keyed by the handle an instance
// produces, so a test can see WHICH store the guest reached.
func instanced(t *testing.T, admit wasm.Admitter, handles ...string) (invoke.Instance, *caches) {
	t.Helper()

	store := newCaches(handles...)
	options := []wasm.Option{wasm.WithCaches(store)}
	if admit != nil {
		options = append(options, wasm.WithAdmission(admit))
	}

	instance := start(t, tier(t, cacher(t), options...), invoke.Spec{
		Name:    "fn",
		Reaches: []invoke.Grant{manyGrant("sessions", "h", "get", "put")},
	})
	return instance, store
}

// TestAnINSTANCEIsAddressedInTheLabel, and the store it reaches is the handle
// with the instance on it — which is what makes every backing isolate one
// instance from another with the mechanism it already has for two resources.
func TestAnInstanceIsItsOwnStore(t *testing.T) {
	admit := &admitting{}
	instance, store := instanced(t, admit, "h/room-42")

	require.Equal(t, 204, call(t, instance, "/put/sessions~room-42/k", []byte("v")).Status)
	require.Equal(t, []string{"sessions/room-42"}, admit.seen(), "the validator was asked once")
	require.Equal(t, []string{"h/room-42"}, store.seen(), "and the backing got the scoped handle")
}

// TestTWOInstancesDoNotSeeEachOther, which is the whole point of the block.
func TestTwoInstancesAreTwoStores(t *testing.T) {
	instance, store := instanced(t, &admitting{}, "h/a", "h/b")

	require.Equal(t, 204, call(t, instance, "/put/sessions~a/k", []byte("mine")).Status)
	require.Equal(t, 204, call(t, instance, "/put/sessions~b/k", []byte("theirs")).Status)

	res := call(t, instance, "/get/sessions~a/k", nil)
	require.Equal(t, 200, res.Status)
	require.Equal(t, "mine", string(res.Body))
	// Three opens for three calls — the tier opens per call and caching that
	// is the backing's job — and each one is the instance the guest named.
	require.Equal(t, []string{"h/a", "h/b", "h/a"}, store.seen())
}

// TestASTOREThatHoldsManyCannotBeAddressedAsItself. Serving the resource is the
// defect the whole block exists to remove: one store where the config says
// many, admitted by nobody.
func TestAManyInstanceStoreRefusesTheBareLabel(t *testing.T) {
	admit := &admitting{}
	instance, store := instanced(t, admit, "h")

	res := call(t, instance, "/get/sessions/k", nil)
	require.Equal(t, 404, res.Status)
	require.Contains(t, string(res.Body), "denied:")
	require.Contains(t, string(res.Body), "holds many instances")
	require.Empty(t, admit.seen())
	require.Empty(t, store.seen(), "and nothing was opened to find that out")
}

// TestASTOREThatHoldsONECannotBeAddressedAsAnInstance, because inventing one
// would give a caller a private corner of a resource its author declared to be
// single.
func TestASingleStoreRefusesAnInstance(t *testing.T) {
	store := newCaches("h")
	instance := start(t, tier(t, cacher(t), wasm.WithCaches(store), wasm.WithAdmission(&admitting{})), invoke.Spec{
		Name:    "fn",
		Reaches: []invoke.Grant{{Kind: "cache", Label: "sessions", Handle: "h", Ops: []string{"get"}}},
	})

	res := call(t, instance, "/get/sessions~room/k", nil)
	require.Equal(t, 404, res.Status)
	require.Contains(t, string(res.Body), "holds one instance")
	require.Empty(t, store.seen())
}

// TestAVALIDATORThatSaysNoIsTheAnswer, and it reaches the guest as a refusal it
// can read rather than as an outage.
func TestAnInstanceTheValidatorRefused(t *testing.T) {
	admit := &admitting{refuse: errors.New("may_open refused \"room-42\" with 403")}
	instance, store := instanced(t, admit, "h/room-42")

	res := call(t, instance, "/get/sessions~room-42/k", nil)
	require.Equal(t, 404, res.Status)
	require.Contains(t, string(res.Body), "may_open refused")
	require.Empty(t, store.seen(), "the store was never opened")
}

// TestATIERWithNothingToAskRefusesEveryInstance, which is the same answer the
// composer used to give for the whole grant — and it is a composition fault
// rather than the caller's, so it says so differently.
func TestNoAdmitterRefusesEveryInstance(t *testing.T) {
	instance, _ := instanced(t, nil, "h/room-42")

	res := call(t, instance, "/get/sessions~room-42/k", nil)
	require.Equal(t, 404, res.Status)
	require.Contains(t, string(res.Body), "cannot call one")
}

// TestTHEOPCheckStillHappensFirst, so a verb the config never granted is
// refused before a validator is bothered — an admission is an invocation and
// running one for a call that was never going to be allowed is work done to
// reach a refusal.
func TestAnUngrantedVerbNeverReachesTheValidator(t *testing.T) {
	admit := &admitting{}
	store := newCaches("h/room")
	instance := start(t, tier(t, cacher(t), wasm.WithCaches(store), wasm.WithAdmission(admit)), invoke.Spec{
		Name:    "fn",
		Reaches: []invoke.Grant{manyGrant("sessions", "h", "get")},
	})

	res := call(t, instance, "/put/sessions~room/k", []byte("v"))
	require.Equal(t, 403, res.Status)
	require.Equal(t, "denied:sessions cannot put", string(res.Body))
	require.Empty(t, admit.seen())
}

// TestANINSTANCENameIsAKeyAndNotAPath. It is looked up exactly, and a separator
// in it would make one instance reachable by two names — and worse, the store
// path-cleans a key, so `..` would resolve into somebody else's.
func TestAnInstanceNameMayNotBeAPath(t *testing.T) {
	admit := &admitting{}
	instance, _ := instanced(t, admit, "h/a")

	for _, name := range []string{"sessions~a~b", "sessions~"} {
		res := call(t, instance, "/get/"+name+"/k", nil)
		require.Equal(t, 404, res.Status, name)
		require.Contains(t, string(res.Body), "may not be empty or contain /", name)
	}
	require.Empty(t, admit.seen(), "and no validator was bothered")
}

// TestALABELTheConfigNeverGrantedIsStillRefusedByNAME, instance or not — the
// split must not turn a typo into something else.
func TestAnUngrantedLabelWithAnInstanceIsRefusedByName(t *testing.T) {
	instance, _ := instanced(t, &admitting{}, "h")

	res := call(t, instance, "/get/somebody-elses~room/k", nil)
	require.Equal(t, 404, res.Status)
	require.Equal(t, "no-such-cache:somebody-elses", string(res.Body),
		"the LABEL, without the instance, because that is what the author would look for")
}

// TestANADMITTEDInstanceIsAskedAboutONCEPerCall, which is what makes the
// per-node cache in the implementation worth having — and is also why an
// Admitter must be cheap once it has decided.
func TestEveryCallAsksTheAdmitter(t *testing.T) {
	admit := &admitting{}
	instance, _ := instanced(t, admit, "h/room")

	for range 3 {
		require.Equal(t, 204, call(t, instance, "/put/sessions~room/k", []byte("v")).Status)
	}
	require.Len(t, admit.seen(), 3, "the tier asks per open; caching the answer is the Admitter's job")
}

// TestAVALIDATORThatCouldNotBeRUNIsUnavailableAndNotDenied. Telling a caller
// its instance was turned down when the cloud never asked sends somebody to
// argue with a policy that did not fire.
func TestAnUnreachableValidatorIsNotARefusal(t *testing.T) {
	admit := &admitting{refuse: errors.New("the validator could not be run: no such function")}
	instance, _ := instanced(t, admit, "h/room")

	res := call(t, instance, "/get/sessions~room/k", nil)
	require.Equal(t, 404, res.Status)
	require.Contains(t, string(res.Body), "unavailable:", "got %q", res.Body)
	require.NotContains(t, string(res.Body), "denied:")
}

// AND A VALIDATOR THAT SAID NO STILL READS AS A DECISION, which is the arm on
// the other side of that line.
func TestARefusedInstanceIsDenied(t *testing.T) {
	admit := &admitting{refuse: fmt.Errorf("%w: may_open refused it", wasm.ErrNotAdmitted)}
	instance, _ := instanced(t, admit, "h/room")

	res := call(t, instance, "/get/sessions~room/k", nil)
	require.Equal(t, 404, res.Status)
	require.Contains(t, string(res.Body), "denied:", "got %q", res.Body)
}
