package section

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTheOnlyThingThatHandsOutAHoldingIsDo(t *testing.T) {
	var exported, handsOutAHolding []string
	locksTheMutex := map[string]bool{}

	for _, fn := range declarations(t) {
		if !fn.Name.IsExported() {
			continue
		}
		name := fn.Name.Name
		exported = append(exported, name)

		if fn.Type.Results != nil {
			for _, result := range fn.Type.Results.List {
				if typeName(result.Type) == "Holding" {
					handsOutAHolding = append(handsOutAHolding, name)
				}
			}
		}
		// A Holding handed to a callback is handed out just as much as one
		// returned, which is exactly how Do hands one over.
		for _, param := range fn.Type.Params.List {
			ast.Inspect(param.Type, func(n ast.Node) bool {
				if fn, ok := n.(*ast.FuncType); ok {
					for _, arg := range fn.Params.List {
						if typeName(arg.Type) == "Holding" {
							handsOutAHolding = append(handsOutAHolding, name)
						}
					}
				}
				return true
			})
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "Lock" {
				locksTheMutex[name] = true
			}
			return true
		})
	}

	require.ElementsMatch(t,
		[]string{"Do", "Held", "Subject", "Value", "Found", "Write", "Reread"},
		exported,
		"the exported surface of this package is the whole of what a caller can spend")

	require.Equal(t, []string{"Do"}, handsOutAHolding,
		"a second source of a Holding is a handle somebody can hold without holding the lock — "+
			"which is what an Enter that returned one was, and what a Read that stamped one was")
	require.True(t, locksTheMutex["Do"],
		"and Do must take the mutex to make one, or the handle is not exclusion")
}

func TestNothingExportedTakesAValueFromItsCaller(t *testing.T) {
	require.Empty(t, producers(t, declarations(t)),
		"a parameter that returns T is a caller saying what this holding carries — which is "+
			"the whole of what a section refuses, one indirection in")

	// THE CONTROL. The same scan over a declaration that DOES take one finds
	// it, so the emptiness above is a fact about this package rather than about
	// a scan that looks for nothing.
	require.Equal(t, []string{"Reread"}, producers(t, parse(t, `package section
		func (h *Holding[T]) Reread(read func() (T, bool, error)) error { return nil }`)))
}

// producers is every declaration that takes a function returning T — the shape
// "here is the value, install it".
func producers(t *testing.T, decls []*ast.FuncDecl) []string {
	t.Helper()

	var out []string
	for _, fn := range decls {
		if !fn.Name.IsExported() {
			continue
		}
		for _, param := range fn.Type.Params.List {
			fnType, ok := param.Type.(*ast.FuncType)
			if !ok || fnType.Results == nil {
				continue
			}
			for _, result := range fnType.Results.List {
				if typeName(result.Type) == "T" {
					out = append(out, fn.Name.Name)
				}
			}
		}
	}
	return out
}

// declarations is this package's own source, pinned to the expected file list so
// an absence measured over it is a fact about the package.
func declarations(t *testing.T) []*ast.FuncDecl {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)
	require.Contains(t, pkgs, "section")

	var read []string
	var out []*ast.FuncDecl
	for name, file := range pkgs["section"].Files {
		read = append(read, filepath.Base(name))
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				out = append(out, fn)
			}
		}
	}

	require.Equal(t, []string{"section.go"}, read,
		"the whole of this package is one file, and the scan above reads it — a file it does "+
			"not read is a declaration the absence it asserts was never measured over")
	require.ElementsMatch(t, read, sourceFiles(t),
		"and the files on disk are that same one: a file added here and left out of the line "+
			"above is one the parser could be told to skip with nothing disagreeing")

	return out
}

// sourceFiles is the directory's own answer: every .go file here that is not a
// test.
func sourceFiles(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		out = append(out, e.Name())
	}
	return out
}

// parse is the control's source: a declaration written here rather than in the
// package, so that a scan asserting an absence is shown finding a presence.
func parse(t *testing.T, src string) []*ast.FuncDecl {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "control.go", src, 0)
	require.NoError(t, err)

	var out []*ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			out = append(out, fn)
		}
	}
	return out
}

func typeName(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.IndexExpr:
		return typeName(v.X)
	case *ast.StarExpr:
		return typeName(v.X)
	}
	return ""
}

func TestAHoldingIsLiveOnlyWhileItsBodyRuns(t *testing.T) {
	var l Lock[string]
	require.False(t, l.Held())

	var escaped Holding[string]
	require.NoError(t, l.Do(read("a job", "the record"), func(h Holding[string]) error {
		require.True(t, l.Held())
		require.Equal(t, "a job", h.Subject())
		require.Equal(t, "the record", h.Value())
		require.True(t, h.Found())
		require.NoError(t, h.Write(func() error { return nil }))
		escaped = h
		return nil
	}))

	require.False(t, l.Held(), "the exclusion is released when the body returns")
	require.ErrorIs(t, escaped.Write(func() error { return nil }), ErrStale,
		"and the handle is dead at the same instant")

	require.NoError(t, l.Do(read("another job", "a later record"), func(Holding[string]) error {
		require.ErrorIs(t, escaped.Write(func() error { return nil }), ErrStale,
			"including while a DIFFERENT holding is running, which is the whole difference "+
				"between owning the section and there being one")
		return nil
	}))
}

func TestARefusedWriteDoesNotRunTheWrite(t *testing.T) {
	var l Lock[string]
	var escaped Holding[string]
	require.NoError(t, l.Do(read("a job", "x"), func(h Holding[string]) error {
		escaped = h
		return nil
	}))

	written := false
	require.ErrorIs(t, escaped.Write(func() error { written = true; return nil }), ErrStale)
	require.False(t, written, "the write is the argument, so a refusal is a write that never ran")
}

func TestTheZeroHoldingIsLiveInNothing(t *testing.T) {
	var none Holding[string]
	require.ErrorIs(t, none.Write(func() error { return nil }), ErrStale)
	require.ErrorContains(t, none.Write(func() error { return nil }), "no section ever handed out")
	require.ErrorIs(t, none.Reread(), ErrStale)
	require.Empty(t, none.Subject(), "and it is about nothing, so it composes no key either")
	require.Empty(t, none.Value())
	require.False(t, none.Found())

	// And it releases nothing: a Lock somebody else is holding stays held.
	var l Lock[string]
	require.NoError(t, l.Do(read("a job", "x"), func(Holding[string]) error {
		require.True(t, l.Held())
		require.ErrorIs(t, none.Write(func() error { return nil }), ErrStale)
		return nil
	}))
}

func TestTheSubjectIsSnapshottedAtTheRead(t *testing.T) {
	var l Lock[string]
	moving := &mutable{subject: "the job the read was taken on", value: "x"}

	var carried Holding[string]
	require.NoError(t, l.Do(moving, func(h Holding[string]) error {
		moving.subject = "some other job"
		require.Equal(t, "the job the read was taken on", h.Subject(),
			"a source that renames itself mid-body must not rename the section")
		carried = h
		return nil
	}))
	require.Equal(t, "the job the read was taken on", carried.Subject(),
		"and a copy is about what the original was about — the subject travels with the "+
			"authority, so there is no assignment that separates the two")
}

func TestDoWaitsForTheBodyRunningNow(t *testing.T) {
	var l Lock[int]
	var mu sync.Mutex
	var order []int

	first := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)

	require.NoError(t, l.Do(read("one", 1), func(Holding[int]) error {
		go func() {
			defer wg.Done()
			<-first
			l.Do(read("two", 2), func(Holding[int]) error {
				mu.Lock()
				order = append(order, 2)
				mu.Unlock()
				return nil
			})
		}()
		close(first)

		mu.Lock()
		order = append(order, 1)
		mu.Unlock()
		return nil
	}))
	wg.Wait()

	require.Equal(t, []int{1, 2}, order, "the second body cannot have run before the first returned")
	require.False(t, l.Held())
}

func TestAFailedReadRunsNoBody(t *testing.T) {
	var l Lock[string]
	boom := errors.New("the store is gone")

	ran := false
	err := l.Do(failing[string]{err: boom}, func(Holding[string]) error {
		ran = true
		return nil
	})
	require.ErrorIs(t, err, boom)
	require.False(t, ran)
	require.False(t, l.Held(), "and the lock is released however the read went")
}

func TestAReadThatFoundNothingStillDecides(t *testing.T) {
	var l Lock[string]
	require.NoError(t, l.Do(&mutable{subject: "a job"}, func(h Holding[string]) error {
		require.False(t, h.Found())
		return h.Write(func() error { return nil })
	}))
}

func TestRereadReplacesWhatTheHoldingCarries(t *testing.T) {
	var l Lock[string]
	src := &mutable{subject: "a job", value: "triage", found: true}

	require.NoError(t, l.Do(src, func(h Holding[string]) error {
		require.Equal(t, "triage", h.Value())

		src.value = "decided"
		require.NoError(t, h.Reread())
		require.Equal(t, "decided", h.Value())
		require.True(t, h.Found())
		require.Equal(t, "a job", h.Subject(), "and a second read is a second read of the SAME thing")

		// A failed re-read leaves what was there: there is no third state to
		// install and nothing has happened.
		src.err = errors.New("the store is gone")
		require.ErrorIs(t, h.Reread(), src.err)
		require.Equal(t, "decided", h.Value())
		return nil
	}))
	require.Equal(t, 2, src.agains, "Reread reads, every time, rather than taking what it is given")
}

func TestAnEscapedHandleCannotRereadItselfAlive(t *testing.T) {
	var l Lock[string]
	src := &mutable{subject: "a job", value: "x", found: true}

	var escaped Holding[string]
	require.NoError(t, l.Do(src, func(h Holding[string]) error {
		escaped = h
		return nil
	}))

	src.value = "fresh"
	require.ErrorIs(t, escaped.Reread(), ErrStale)
	require.Equal(t, "x", escaped.Value())
	require.Equal(t, 0, src.agains, "and the read it would have decided on is never even taken")
	require.ErrorIs(t, escaped.Write(func() error { return nil }), ErrStale)
}

func TestACopyOfAHandleDiesWithTheOneItWasCopiedFrom(t *testing.T) {
	var l Lock[string]
	var copied Holding[string]
	require.NoError(t, l.Do(read("a job", "x"), func(h Holding[string]) error {
		copied = h
		require.NoError(t, copied.Write(func() error { return nil }), "a copy writes while the body runs")
		require.Equal(t, h.Subject(), copied.Subject())
		return nil
	}))
	require.ErrorIs(t, copied.Write(func() error { return nil }), ErrStale)
}

func TestDoReportsTheBodyAndSurvivesAPanic(t *testing.T) {
	var l Lock[string]
	boom := errors.New("the body refused")
	require.ErrorIs(t, l.Do(read("a job", "x"), func(Holding[string]) error { return boom }), boom)

	var escaped Holding[string]
	require.Panics(t, func() {
		l.Do(read("a job", "x"), func(h Holding[string]) error {
			escaped = h
			panic("the body blew up")
		})
	})
	require.False(t, l.Held(), "a panicking body must not hold the table forever")
	require.ErrorIs(t, escaped.Write(func() error { return nil }), ErrStale)
}

func TestTheIdentityDiesBeforeTheExclusionIsReleased(t *testing.T) {
	var l Lock[string]
	var cleared atomic.Bool
	refused := make(chan error, 64)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				err := l.Do(watchful{lock: &l, cleared: &cleared}, func(h Holding[string]) error {
					return h.Write(func() error { return nil })
				})
				if err != nil {
					select {
					case refused <- err:
					default:
					}
				}
			}
		}()
	}
	wg.Wait()
	close(refused)

	require.NoError(t, <-refused, "a section that entered correctly must not have its writes refused")
	require.False(t, cleared.Load(),
		"a holding was cleared while another section's read was running under it")
}

// watchful checks the identity while reading: First runs with the mutex held and
// this holding minted, so an unheld Lock there means another section cleared it.
type watchful struct {
	lock    *Lock[string]
	cleared *atomic.Bool
}

func (w watchful) Subject() string { return "the section coming in" }

func (w watchful) First() (string, bool, error) {
	for range 16 {
		if !w.lock.Held() {
			w.cleared.Store(true)
			break
		}
		runtime.Gosched()
	}
	return "the record", true, nil
}

func (w watchful) Again() (string, bool, error) { return "the record", true, nil }

// mutable is a source whose answers a test can move, so that "the subject is
// snapshotted" and "Reread reads again" are shown rather than asserted.
type mutable struct {
	subject string
	value   string
	found   bool
	err     error
	agains  int
}

func (m *mutable) Subject() string { return m.subject }

func (m *mutable) First() (string, bool, error) { return m.value, m.found, m.err }

func (m *mutable) Again() (string, bool, error) {
	m.agains++
	return m.value, m.found, m.err
}

// failing is a source whose first read fails.
type failing[T any] struct{ err error }

func (f failing[T]) Subject() string         { return "a job" }
func (f failing[T]) First() (T, bool, error) { var zero T; return zero, false, f.err }
func (f failing[T]) Again() (T, bool, error) { var zero T; return zero, false, f.err }

// fixed is the ordinary case: one subject, one value, both reads the same.
type fixed[T any] struct {
	subject string
	value   T
}

func (f fixed[T]) Subject() string         { return f.subject }
func (f fixed[T]) First() (T, bool, error) { return f.value, true, nil }
func (f fixed[T]) Again() (T, bool, error) { return f.value, true, nil }

func read[T any](subject string, value T) Source[T] {
	return fixed[T]{subject: subject, value: value}
}
