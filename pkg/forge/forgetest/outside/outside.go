// Package outside is an assertion helper declared OUTSIDE the suite's own
// package, which is the one thing the suite's own source cannot be.
//
// Every rule the ledger imposes is proven against real source type-checked
// through the same loader, and one of the shapes an assertion was measured
// hiding in is a call into another package handed the METHOD that fails a test
// rather than the reporter that method belongs to: nothing about
// func(string, ...any) says it can fail anything, and everything about what it
// is handed at the call site does.
//
// That violation cannot be written inside forgetest. A body in that package is
// walked, so an assertion in it is counted where it is written and the evasion
// evaporates before the rule can be shown it. So the helper lives out here, and
// nothing but the ledger's own fault uses it.
package outside

// Check fails a test through a method value it was handed. It is shaped like
// testify's assertions — the message is the head of the variadic — so the
// ledger reads what it says exactly as it reads what testify says, and the
// only difference between the two is the one being proven.
func Check(fail func(string, ...any), ok bool, msgAndArgs ...any) {
	if ok || len(msgAndArgs) == 0 {
		return
	}
	msg, _ := msgAndArgs[0].(string)
	fail("%s", msg)
}
