package forgetest

import "testing"

// tb is the part of *testing.T this suite uses, so the suite can run against a
// recorder and its negative cases be asserted.
type tb interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
	FailNow()
	Logf(format string, args ...any)
	Run(name string, f func(tb)) bool
}

type realTB struct{ *testing.T }

func (r realTB) Run(name string, f func(t tb)) bool {
	return r.T.Run(name, func(t *testing.T) { f(realTB{t}) })
}
