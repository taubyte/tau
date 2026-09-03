package section

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// THE API IS THE WALL, so the API is what is pinned.
//
// Every defeat this package exists to end was a caller reaching for something
// that was there: a "which section is running" accessor that returned a
// spendable number, a mutex beside it, a stamp it could assign, a minting
// function it could put on a value it had read anywhere at all — and then, when
// all of those were gone, a Reread that took a closure returning T, which is
// the minting function again with a method receiver in place of the token.
//
// So the whole exported surface is enumerated here, and a function added to it
// — however reasonable — fails until somebody has decided what it hands out.
//
// THE TWO CLAIMS UNDER IT are what the enumeration is for: nothing but Do
// returns a Holding, and Do takes the mutex to make one. A second source of a
// live Holding is a token again, and this time there would be no read attached
// to it either.
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

// AND NO VALUE COMES IN FROM A CALLER AT ALL, which is stronger than the
// sentence this test used to check.
//
// It used to allow a value in "through a read function this package calls",
// which was true of Do and true of Reread — and Reread is where that allowance
// went wrong. A body inside a live holding could hand it a closure returning
// anything, from any read, taken at any time, and the holding installed it: the
// exported generic minting function of the round before, needing no token
// because the receiver already carried the authority.
//
// THE READS ARE A Source NOW, chosen at the door by whoever took the lock, and
// nothing exported takes a function that produces a T. What this scan therefore
// requires is an EMPTY answer, and the control below it is that the same scan
// finds the shape when it is there.
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

// declarations is this package's own source, AND IT PINS WHICH SOURCE THAT IS.
//
// The scan above asserts an ABSENCE — nothing exported takes a value from its
// caller — and an absence is only as good as the file set it was measured over.
// A filter that skips a file by name, or a second file added beside this one,
// would make the emptiness a fact about the scan rather than about the package.
// So what it expects to read is written down, and what is on disk has to agree
// with it: a second file here is an edit to this list.
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

// A HOLDING IS LIVE ONLY WHILE ITS BODY IS RUNNING, and an escaped one never
// comes back to life — not when the lock is free, and not while somebody else
// holds it.
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

// AND A REFUSED WRITE IS NOT MADE. The closure is the write; a gate that
// answered a question would leave it to the caller to act on the answer.
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

// THE ZERO HOLDING IS NO HOLDING. It is the only one a caller can construct,
// and it answers nothing — which is what makes a handle assembled at a write
// site worthless rather than merely detectable.
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

// THE SUBJECT IS THE HOLDING'S, NOT THE CALLER'S. It is read from the source
// ONCE, at the instant of the read, so a source that changes its mind
// afterwards changes nothing about what the holding is about — which is what a
// caller composes the keys of its writes from.
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

// THE EXCLUSION IS REAL, and Held is not a substitute for it: Do blocks until
// the body running now has returned.
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

// A READ THAT FAILED RUNS NO BODY: there is nothing to decide a write on, and
// the error is the caller's own.
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

// A READ THAT FOUND NOTHING IS STILL A DECISION — "there is no job here" is
// what create's write is decided on — so it runs the body like any other.
func TestAReadThatFoundNothingStillDecides(t *testing.T) {
	var l Lock[string]
	require.NoError(t, l.Do(&mutable{subject: "a job"}, func(h Holding[string]) error {
		require.False(t, h.Found())
		return h.Write(func() error { return nil })
	}))
}

// REREADING IS A SECOND READ OF THE SAME SUBJECT UNDER THE SAME HOLDING, and
// the value it installs is the one the body decides on from then on.
//
// IT TAKES NOTHING. What it installs comes from Again, on the source named at
// the door — so a body that wanted to decide on a record it had read somewhere
// else has no argument to hand it and no field to assign it to.
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

// AND AN ESCAPED HANDLE CANNOT REREAD ITSELF BACK TO LIFE. Reread is a read
// under a live holding, so the one thing it cannot be is the way a dead handle
// gets a fresh value to write on.
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

// A COPY OF A HANDLE IS THE SAME HANDLE. The body is handed a value, so copying
// one is free and inevitable — and a copy that outlives the body is dead with
// it, which is what makes "hand your decision to a helper" safe and "stash it
// for later" useless.
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

// THE BODY'S ERROR IS DO'S ERROR, and a body that panics still releases the
// lock and kills the handle — a transition that crashed must not leave the
// table locked for the life of the process.
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
