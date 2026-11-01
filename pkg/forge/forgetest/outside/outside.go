// Package outside is an assertion helper declared outside the suite's package,
// for the ledger's fault of a test-failing method value handed to another
// package: inside forgetest the body would be walked and the evasion would
// vanish.
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
