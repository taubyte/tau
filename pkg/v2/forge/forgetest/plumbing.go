package forgetest

// PLUMBING IS NOT AN ASSERTION, AND THIS FILE IS WHERE THE SUITE SAYS SO.
//
// Every assertion in this suite is a claim about the DRIVER, and the ledger
// beside it requires that each one have a FAULT BEHIND IT — a subject that is
// wrong in a named way and makes exactly that assertion fire. An assertion
// nothing can make fire is decoration, and three rounds of this package's
// history are three different ways of adding one.
//
// But a suite also has to GET to its questions. It mints a secret to register a
// hook with. It installs a deploy key so that "is it read-only" and "does
// removal remove it" have something to be about. It binds a session. None of
// those is a claim about the driver: if one fails, the suite did not learn that
// the driver is wrong, it learned that it never got to ask. Demanding a fault
// behind "the harness started" produces faults nobody wants and assertions
// nobody believes.
//
// So a step is spelled differently from a claim.
//
//	mustReach / mustHold — a step. Says, in its own words, WHAT QUESTION it was
//	on its way to. Exempt from needing a fault.
//
//	assert / require     — a claim about the driver. Needs a fault.
//
// THE SPELLING IS THE MARKER, AND IT CANNOT BE APPLIED BY HABIT. Nobody types
// mustReach where they meant require.NoError: it is a different name, it takes
// a mandatory sentence, and it shows up in a diff as a rewritten line rather
// than as an unchanged one. That is the review half.
//
// THE LEDGER IS THE OTHER HALF, AND IT IS THE ONE THAT CANNOT BE ARGUED WITH. A
// step must not be reachable by a driver fault: if any proof in the suite's own
// table makes one of these fire, it was never plumbing — it was a claim about
// the driver that somebody demoted, and the build fails saying which. Demoting
// a load-bearing assertion therefore does not hide it; it renames the failure.
//
// And a step must be ON THE WAY somewhere: the ledger requires a real assertion
// after it in the same function, or that the function returns the value a
// caller will ask its question with. A step that is the last thing a subtest
// does was never a step.

// mustReach is a step that had to succeed for the question after it to mean
// anything. question is what the suite was on its way to ask, in words, and it
// is mandatory: a step that cannot say what it was for is an assertion wearing
// the wrong name.
func mustReach(t tb, err error, question string, args ...any) {
	t.Helper()
	if err == nil {
		return
	}
	t.Fatalf("forgetest: the suite never got as far as "+question+": %v", append(append([]any{}, args...), err)...)
}

// mustHold is the same thing about a state rather than an error — a probe that
// reported nothing, a fixture that is not what the next question needs.
func mustHold(t tb, ok bool, question string, args ...any) {
	t.Helper()
	if ok {
		return
	}
	t.Fatalf("forgetest: the suite never got as far as "+question, args...)
}
