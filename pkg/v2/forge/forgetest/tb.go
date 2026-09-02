package forgetest

import "testing"

// tb is the sliver of *testing.T this suite uses. It exists so the suite can be
// run against a recorder and its own negative cases asserted — a conformance
// suite that has only ever seen a correct driver has not been shown to catch
// anything, and "it would have failed" is exactly the claim a test is for.
//
// It is unexported: Run's signature is *testing.T, which is what a driver
// author writes.
type tb interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
	FailNow()
	Logf(format string, args ...any)
	Run(name string, f func(tb)) bool
}

// realTB adapts *testing.T. Everything but Run is promoted.
type realTB struct{ *testing.T }

func (r realTB) Run(name string, f func(t tb)) bool {
	return r.T.Run(name, func(t *testing.T) { f(realTB{t}) })
}
