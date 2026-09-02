package section

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// THE API IS THE WALL, so the API is what is pinned.
//
// Every defeat this package exists to end was a caller reaching for something
// that was there: a "which section is running" accessor that returned a
// spendable number, a mutex beside it, a stamp it could assign. So the whole
// exported surface is enumerated here, and a function added to it — however
// reasonable — fails until somebody has decided what it hands out.
func TestTheOnlyWayToGetASectionIsToEnterOne(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)
	require.Contains(t, pkgs, "section")

	var exported, handsOutASection, handsOutAReading []string
	locksTheMutex := map[string]bool{}

	for _, file := range pkgs["section"].Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() {
				continue
			}
			name := fn.Name.Name
			exported = append(exported, name)

			if fn.Type.Results != nil {
				for _, result := range fn.Type.Results.List {
					switch typeName(result.Type) {
					case "Section":
						handsOutASection = append(handsOutASection, name)
					case "Reading":
						handsOutAReading = append(handsOutAReading, name)
					}
				}
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "Lock" {
					locksTheMutex[name] = true
				}
				return true
			})
		}
	}

	require.ElementsMatch(t,
		[]string{"Enter", "Leave", "Held", "Live", "Read", "Value", "Found", "Live"},
		exported,
		"the exported surface of this package is the whole of what a caller can spend")

	require.Equal(t, []string{"Enter"}, handsOutASection,
		"a second source of a Section is a token a caller can hold without holding the lock — "+
			"which is exactly what a `current()` accessor was")
	require.True(t, locksTheMutex["Enter"],
		"and Enter must take the mutex to make one, or the token is not exclusion")

	require.Equal(t, []string{"Read"}, handsOutAReading,
		"a second maker of a Reading is a stamp put on by something other than a read")
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

// A SECTION IS ALIVE ONLY WHILE IT IS HELD, and the next one is never the same
// one again.
func TestASectionIsLiveOnlyWhileItIsHeld(t *testing.T) {
	var l Lock
	require.False(t, l.Held())

	first := l.Enter()
	require.True(t, l.Held())
	require.NoError(t, first.Live())
	first.Leave()

	require.False(t, l.Held())
	require.ErrorIs(t, first.Live(), ErrStale, "it is over the moment it is left")

	second := l.Enter()
	defer second.Leave()
	require.NoError(t, second.Live())
	require.ErrorIs(t, first.Live(), ErrStale,
		"and a section retaken is a different section, so the old one does not come back to life")
	require.NotEqual(t, first, second)
}

// THE ZERO SECTION IS NO SECTION. It is the only one a caller can construct,
// and it answers nothing.
func TestTheZeroSectionIsLiveInNothing(t *testing.T) {
	var none Section
	require.ErrorIs(t, none.Live(), ErrStale)
	require.ErrorContains(t, none.Live(), "under no section")

	// And leaving it releases nothing: a Lock somebody else is holding stays
	// held.
	var l Lock
	held := l.Enter()
	none.Leave()
	require.True(t, l.Held(), "the zero section must not release a lock it never took")
	held.Leave()
	require.False(t, l.Held())
}

// LEAVING IS IDEMPOTENT, AND A STALE SECTION RELEASES NOTHING. Two Leaves of
// one holding would unlock a mutex twice — and a Leave of a holding that has
// already ended would release the exclusion of whoever holds it NOW, which is
// the "release the mutex and leave the number alive" shape one door over.
func TestAStaleSectionCannotReleaseTheOneThatIsRunning(t *testing.T) {
	var l Lock

	first := l.Enter()
	first.Leave()
	first.Leave() // a second Leave of the same holding does nothing

	second := l.Enter()
	first.Leave() // and a stale holding does not release the live one
	require.True(t, l.Held())
	require.NoError(t, second.Live())

	second.Leave()
	require.False(t, l.Held())
}

// THE EXCLUSION IS REAL, and Held is not a substitute for it: entering blocks
// until the holding running now has been left.
func TestEnteringWaitsForTheSectionRunningNow(t *testing.T) {
	var l Lock
	var order []uint64
	var mu sync.Mutex

	first := l.Enter()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		second := l.Enter()
		defer second.Leave()
		mu.Lock()
		order = append(order, second.id)
		mu.Unlock()
	}()

	mu.Lock()
	order = append(order, first.id)
	mu.Unlock()
	first.Leave()
	wg.Wait()

	require.True(t, slices.IsSorted(order), "the second entry cannot have run before the first left")
	require.False(t, l.Held())
}

// A READING CARRIES THE SECTION THE READ WAS TAKEN IN, and what it read.
func TestAReadingCarriesTheSectionItWasTakenIn(t *testing.T) {
	var l Lock

	in := l.Enter()
	r, err := Read(in, func() (string, bool, error) { return "the record", true, nil })
	require.NoError(t, err)
	require.Equal(t, "the record", r.Value())
	require.True(t, r.Found())
	require.NoError(t, r.Live())

	in.Leave()
	require.ErrorIs(t, r.Live(), ErrStale, "and it is dead the moment that section is")

	next := l.Enter()
	defer next.Leave()
	require.ErrorIs(t, r.Live(), ErrStale,
		"including while a DIFFERENT section is running, which is the whole difference "+
			"between owning the section and there being one")
	require.ErrorContains(t, r.Live(), "was made under section")
}

// A read that found nothing is still a reading, and a read that FAILED is not
// one at all: there is nothing to decide a write on.
func TestAReadThatFailedIsNotAReading(t *testing.T) {
	var l Lock
	in := l.Enter()
	defer in.Leave()

	empty, err := Read(in, func() (string, bool, error) { return "", false, nil })
	require.NoError(t, err)
	require.False(t, empty.Found())
	require.NoError(t, empty.Live(), "a read that found nothing still decides what to write")

	boom := errors.New("the store is gone")
	failed, err := Read(in, func() (string, bool, error) { return "", false, boom })
	require.ErrorIs(t, err, boom)
	require.ErrorIs(t, failed.Live(), ErrStale, "and a failed read is stamped with nothing")
}

// A read taken OUTSIDE any section is stamped with the zero section on purpose:
// it is a fact the code states rather than a comment, and every write that
// tried to spend it is refused.
func TestAReadTakenOutsideASectionDecidesNoWrite(t *testing.T) {
	var l Lock
	held := l.Enter()
	defer held.Leave()

	// Another goroutine's read, with no section of its own, while a section it
	// does not hold is running.
	r, err := Read(Section{}, func() (string, bool, error) { return "the record", true, nil })
	require.NoError(t, err)
	require.Equal(t, "the record", r.Value())
	require.ErrorIs(t, r.Live(), ErrStale)
}
