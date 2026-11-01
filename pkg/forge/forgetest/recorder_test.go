package forgetest

import (
	"fmt"
	"runtime"
	"strings"
)

// recorder is a tb that remembers instead of failing, so this package can assert
// the suite catches a broken driver. FailNow is runtime.Goexit, as testing's
// is, so each subtest runs in its own goroutine.
type recorder struct {
	failed bool
	msgs   []string
}

func (r *recorder) Helper()             {}
func (r *recorder) Logf(string, ...any) {}
func (r *recorder) FailNow()            { runtime.Goexit() }
func (r *recorder) String() string      { return strings.Join(r.msgs, "\n") }
func (r *recorder) Errorf(f string, a ...any) {
	r.failed = true
	r.msgs = append(r.msgs, fmt.Sprintf(f, a...))
}

func (r *recorder) Fatalf(f string, a ...any) {
	r.Errorf(f, a...)
	r.FailNow()
}

func (r *recorder) Run(name string, f func(tb)) bool {
	child := &recorder{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f(child)
	}()
	<-done
	if child.failed {
		r.failed = true
		for _, m := range child.msgs {
			r.msgs = append(r.msgs, name+": "+m)
		}
	}
	return !child.failed
}

func record(s Subject) *recorder {
	r := &recorder{}
	r.Run("suite", func(t tb) { run(t, s) })
	return r
}
