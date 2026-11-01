package forgetest

import (
	"errors"

	"github.com/taubyte/tau/pkg/forge"
)

// A step is how the suite gets to a question, not a claim about the subject:
// mustReach, mustAsk and mustHold say what question they were on the way to and
// need no fault, while assert and require need one. The ledger refuses a step a
// proof's fault reaches, except a stop caused by the session refusing a
// capability, which only mustReach can report. A step must lead to a real
// assertion in the same function, or to a returned value a caller asks about.

// refusedCapability is how mustReach spells "the session refuses this
// capability" and how the ledger tells that failure from every other one. It is
// deliberately a sentence no assertion message contains.
const refusedCapability = ": this session refuses "

// step is a type so the ledger finds markers by what they take, not by what they
// are called.
type step string

// mustReach is a step that had to succeed for the question after it to mean
// anything. question is what the suite was on its way to ask, in words, and it
// is mandatory: a step that cannot say what it was for is an assertion wearing
// the wrong name.
//
// A capability refusal is spelled differently from every other failure, and the
// difference is what the ledger reads. See refusedCapability.
func mustReach(t tb, err error, question step, args ...any) {
	t.Helper()
	if err == nil {
		return
	}
	var un *forge.UnsupportedError
	if errors.As(err, &un) {
		t.Fatalf("forgetest: the suite never got as far as "+string(question)+refusedCapability+"%s",
			append(append([]any{}, args...), un.Capability)...)
	}
	t.Fatalf("forgetest: the suite never got as far as "+string(question)+": %v", append(append([]any{}, args...), err)...)
}

// mustAsk is the step for a call the suite makes for its SIDE EFFECT rather
// than for its answer — the address the driver composed, the request the
// subject recorded. Such a call is expected to fail: whereItWouldGo breaks the
// wire on purpose, and the side effect is left behind either way. The one
// failure that takes the side effect away too is the session refusing the
// capability, because then nothing was composed at all, and that is the only
// failure this step reads.
func mustAsk(t tb, err error, question step, args ...any) {
	t.Helper()
	var un *forge.UnsupportedError
	if !errors.As(err, &un) {
		return
	}
	t.Fatalf("forgetest: the suite never got as far as "+string(question)+refusedCapability+"%s",
		append(append([]any{}, args...), un.Capability)...)
}

// mustHold is the same thing about a state rather than an error — a probe that
// reported nothing, a fixture that is not what the next question needs. It has
// no error to read, so it can never carry refusedCapability, and therefore no
// proof can ever stop one.
func mustHold(t tb, ok bool, question step, args ...any) {
	t.Helper()
	if ok {
		return
	}
	t.Fatalf("forgetest: the suite never got as far as "+string(question), args...)
}
