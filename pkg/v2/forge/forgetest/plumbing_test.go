package forgetest

import (
	"errors"
	"strings"
	"testing"

	"github.com/taubyte/tau/pkg/v2/forge"
)

// THE WORDS A STEP SAYS ARE LOAD-BEARING NOW, so they are tested here rather
// than assumed. The ledger tells a step the suite could not take from a claim
// somebody demoted by reading refusedCapability out of what the step actually
// said: a stop that does not carry it is a complaint about the subject wearing
// a step's name. If mustReach stopped spelling a capability refusal differently
// from every other failure, that rule would quietly pass everything, and no
// proof in the suite would notice — a proof only ever runs the suite, and the
// suite's steps are the one thing no proof may make fire.
func TestAStepSaysWhichKindOfFailureStoppedIt(t *testing.T) {
	const question = "asking the question this step was on its way to"
	refusal := &forge.UnsupportedError{Host: "forge.invalid", Capability: forge.CapPushHooks}

	for name, tc := range map[string]struct {
		take    func(t tb)
		fires   bool
		says    string
		refusal bool
	}{
		"mustReach on nothing": {
			take: func(t tb) { mustReach(t, nil, question) },
		},
		"mustReach on a subject that answered wrongly": {
			take:  func(t tb) { mustReach(t, errors.New("the forge said no"), question) },
			fires: true, says: "the forge said no",
		},
		"mustReach on a capability the session refuses": {
			take:  func(t tb) { mustReach(t, refusal, question) },
			fires: true, says: string(forge.CapPushHooks), refusal: true,
		},
		"mustAsk ignores a failure that left the side effect behind": {
			take: func(t tb) { mustAsk(t, errors.New("the wire is broken"), question) },
		},
		"mustAsk on a capability the session refuses": {
			take:  func(t tb) { mustAsk(t, refusal, question) },
			fires: true, says: string(forge.CapPushHooks), refusal: true,
		},
		"mustHold on a state that holds": {
			take: func(t tb) { mustHold(t, true, question) },
		},
		"mustHold on a state that does not": {
			take:  func(t tb) { mustHold(t, false, question) },
			fires: true, says: question,
		},
	} {
		t.Run(name, func(t *testing.T) {
			rec := &recorder{}
			rec.Run("step", func(t tb) { tc.take(t) })
			if rec.failed != tc.fires {
				t.Fatalf("failed = %v, want %v: %s", rec.failed, tc.fires, rec)
			}
			if !tc.fires {
				return
			}
			if !strings.Contains(rec.String(), question) {
				t.Errorf("a step that fired did not say what it was on its way to: %s", rec)
			}
			if !strings.Contains(rec.String(), tc.says) {
				t.Errorf("a step that fired did not say %q: %s", tc.says, rec)
			}
			if got := strings.Contains(rec.String(), refusedCapability); got != tc.refusal {
				t.Errorf("carries refusedCapability = %v, want %v; the ledger reads exactly this to tell a stop from a demoted claim: %s",
					got, tc.refusal, rec)
			}
		})
	}
}
