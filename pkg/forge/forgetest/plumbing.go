package forgetest

import (
	"errors"

	"github.com/taubyte/tau/pkg/forge"
)

// PLUMBING IS NOT AN ASSERTION, AND THIS FILE IS WHERE THE SUITE SAYS SO.
//
// Every assertion in this suite is a claim about the SUBJECT — the driver, and
// the probes its author wired up — and the ledger beside it requires that each
// one have a FAULT BEHIND IT: a subject that is wrong in a named way and makes
// exactly that assertion fire. An assertion nothing can make fire is
// decoration, and four rounds of this package's history are four different ways
// of adding one.
//
// But a suite also has to GET to its questions. It mints a secret to register a
// hook with. It installs a deploy key so that "is it read-only" and "does
// removal remove it" have something to be about. It binds a session. None of
// those is a claim: if one fails, the suite did not learn that the subject is
// wrong, it learned that it never got to ask. Demanding a fault behind "the
// harness started" produces faults nobody wants and assertions nobody believes.
//
// So a step is spelled differently from a claim.
//
//	mustReach / mustHold — a step. Says, in its own words, WHAT QUESTION it was
//	on its way to. Exempt from needing a fault.
//
//	assert / require     — a claim about the subject. Needs a fault.
//
// THE SPELLING IS THE MARKER, AND IT CANNOT BE APPLIED BY HABIT. Nobody types
// mustReach where they meant require.NoError: it is a different name, it takes
// a mandatory sentence, and it shows up in a diff as a rewritten line rather
// than as an unchanged one. That is the review half.
//
// THE LEDGER IS THE OTHER HALF, AND IT IS THE ONE THAT CANNOT BE ARGUED WITH. A
// step must not be reachable by a fault: if any proof in the suite's own table
// makes one of these fire, it was never plumbing — it was a claim somebody
// demoted, and the build fails saying which.
//
// THE ONE EXCUSE, AND WHY IT IS THE ONLY ONE. A proof is allowed to list a step
// it STOPS, because an honest cascade exists: a Subject that declares a
// capability its driver does not have turns the next call into an
// *UnsupportedError, and the suite really did not get there. That excuse was
// the hole. "Moved to stops" was a free edit, so a live claim could be respelled
// as a step, its saying moved one list over, and the tree stayed green.
//
// It is not free any more. A STOPPED STEP MUST HAVE FAILED BECAUSE THE SESSION
// REFUSES A CAPABILITY, and mustReach is the only thing that can say so — it
// reads the error and spells a capability refusal with refusedCapability, which
// nothing else in this package emits. A step that fired for any other reason
// cannot be listed in stops at all: a driver that answered wrongly, a probe that
// lied, a state that did not hold. Those are complaints about the subject, and a
// complaint about the subject has exactly one honest spelling, which is a claim
// with a fault behind it.
//
// mustHold has no error to read, so no mustHold can ever be stopped by anything.
//
// And a step must be ON THE WAY somewhere: the ledger requires a real assertion
// after it in the same function, or that the function returns the value a
// caller will ask its question with. A step that is the last thing a subtest
// does was never a step.

// refusedCapability is how mustReach spells "the session refuses this
// capability" and how the ledger tells that failure from every other one. It is
// deliberately a sentence no assertion message contains.
const refusedCapability = ": this session refuses "

// step is the question a marker was on its way to, and it is a TYPE rather
// than a string so that the ledger can find the suite's steps by what they
// TAKE rather than by what they are CALLED.
//
// The ledger used to know the three markers by name, which meant a fourth one
// spelled anything else was invisible to it — the same blindness that let an
// assertion hide behind an import alias. A marker is now the thing that asks
// for one of these, so renaming mustReach, adding mustSettle, or writing the
// next marker in another file all leave the ledger seeing a step; and a claim
// cannot become a step without taking a step, which is a rewritten signature
// and not a rewritten call.
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
