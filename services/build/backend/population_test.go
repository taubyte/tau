package backend

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// population_test.go pins the POPULATION rather than any one member of it.
//
// The defect these tests exist for was not that one transition wrote a stale
// record; it was that a fix closed ONE read-modify-write site over the job
// record and left the others open, and nothing said how many there were. A
// seventh site added later would be that again, silently, and no interleaving
// test can fail for a site nobody wrote one for.
//
// So the enumeration is mechanical and it is checked. record() is the only
// reader of jobRecordKey — TestOnlyOneFunctionReadsAJobRecord is that claim —
// so the calls to it ARE every read of a job record there is, and every one of
// them is named below with what it is allowed to be.
//
// It reads this package's own source, the way services/build/boundary_test.go
// reads the two roles' import lists, and for the same reason: this is the kind
// of property that decays without anyone noticing.

// funcs is one non-test function of this package, and what its body does. It
// is keyed by receiver type and name — this package has two Allow methods, and
// a table keyed by name alone would silently hold one of them.
type funcs struct {
	name  string
	calls map[string]bool
	locks bool
}

// qualify names a declaration the way the tables below do: "Service.cancel",
// "attemptSink.write", or a bare "jobRecordKey" for a function with no
// receiver.
func qualify(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	id, ok := expr.(*ast.Ident)
	if !ok {
		return fn.Name.Name
	}
	return id.Name + "." + fn.Name.Name
}

func pkgFuncs(t *testing.T) map[string]funcs {
	t.Helper()

	fset := token.NewFileSet()
	parsed, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)
	require.Contains(t, parsed, "backend")

	out := map[string]funcs{}
	for _, file := range parsed["backend"].Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			f := funcs{name: qualify(fn), calls: map[string]bool{}}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.SelectorExpr:
					// s.record, s.store.Get, jobLock.Lock — the selector's last
					// segment is the name, whatever it hangs off.
					f.calls[v.Sel.Name] = true
					if id, ok := v.X.(*ast.Ident); ok && id.Name == "jobLock" && v.Sel.Name == "Lock" {
						f.locks = true
					}
				case *ast.CallExpr:
					// Bare calls, for the key builders that have no receiver.
					if id, ok := v.Fun.(*ast.Ident); ok {
						f.calls[id.Name] = true
					}
				}
				return true
			})
			require.NotContains(t, out, f.name, "two declarations named %s", f.name)
			out[f.name] = f
		}
	}
	return out
}

// callers returns every function of this package whose body calls name.
func callers(all map[string]funcs, name string) map[string]funcs {
	out := map[string]funcs{}
	for n, f := range all {
		if n != name && f.calls[name] {
			out[n] = f
		}
	}
	return out
}

func names[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	return out
}

// record() is the only reader of jobRecordKey, so counting its callers counts
// every read of a job record in this package. If that stops being true the
// enumeration below stops being an enumeration.
func TestOnlyOneFunctionReadsAJobRecord(t *testing.T) {
	all := pkgFuncs(t)

	var readers []string
	for n, f := range all {
		if n != "jobRecordKey" && f.calls["jobRecordKey"] && f.calls["Get"] {
			readers = append(readers, n)
		}
	}
	require.ElementsMatch(t, []string{"Service.record"}, readers,
		"every read of a job record must go through record(), or the population below is incomplete")
}

// EVERY READ OF A JOB RECORD IS NAMED HERE, and every one that decides a write
// is one section under jobLock. Adding a site means adding a line to this
// table, which is the point: a site nobody thought about fails here rather than
// shipping, which is exactly what the sixth site did.
func TestEveryReadModifyWriteOfAJobRecordIsOneSection(t *testing.T) {
	// name -> whether the read decides a write, and so must hold jobLock.
	population := map[string]bool{
		// jobs.go
		"Service.job":    false, // a read shape: it decides no write at all
		"Service.cancel": true,
		"Service.retry":  true,
		// queue.go
		"Service.resume": true,
		"Service.create": true,
		// runner.go
		"Service.begin":           true,
		"Service.settle":          true,
		"Service.drainDeadLetter": true,
		// reconcile.go
		"Service.mend": true,
	}

	all := pkgFuncs(t)
	found := callers(all, "record")
	delete(found, "Service.record")
	require.ElementsMatch(t, names(population), names(found),
		"a read of a job record that is not in the table above, or a name in it that is gone")

	for name, mustLock := range population {
		require.Equal(t, mustLock, found[name].locks,
			"%s: the read and the entry it decides must be one section", name)
	}
}

// The write side, from the other direction: every entry that SETS a job record
// is written by a function that holds jobLock, or by finish, whose own callers
// all do.
func TestEveryWriteOfAJobRecordIsInsideASection(t *testing.T) {
	all := pkgFuncs(t)

	var writers []string
	for n, f := range all {
		if n != "jobRecordKey" && n != "Service.record" && f.calls["jobRecordKey"] {
			writers = append(writers, n)
		}
	}
	require.ElementsMatch(t,
		[]string{"Service.create", "Service.begin", "Service.retry", "Service.finish"}, writers)

	for _, n := range []string{"Service.create", "Service.begin", "Service.retry"} {
		require.True(t, all[n].locks, "%s writes a record and must hold jobLock", n)
	}

	// finish is the shared terminal write, so it is its CALLERS that must hold
	// the lock — it takes none itself, because two of them would deadlock.
	require.False(t, all["Service.finish"].locks)
	for name, f := range callers(all, "finish") {
		require.True(t, f.locks, "%s writes a terminal record and must hold jobLock", name)
	}
}

// THE FOUR LOOP-SIDE TRANSITIONS CANNOT INTERLEAVE WITH EACH OTHER, and this is
// why rather than an assumption: begin, settle, drainDeadLetter and mend are
// reached only from step() and reconcile(), which are called only from loop(),
// which is started by exactly one `go` statement in this package — and Start
// refuses a second one (TestStartTwiceIsAnErrorAndSoIsStartAfterClose). One
// goroutine runs all four, in order.
//
// They still take jobLock, because the goroutine they share is not the one
// Cancel, Retry and Enqueue answer on, and because a rule that holds by
// arithmetic over the package's `go` statements is a rule that a second loop
// would break without touching any of them.
func TestTheClaimLoopIsTheOnlyGoroutineThisPackageStarts(t *testing.T) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)

	var started []string
	for _, file := range parsed["backend"].Files {
		ast.Inspect(file, func(n ast.Node) bool {
			g, ok := n.(*ast.GoStmt)
			if !ok {
				return true
			}
			sel, ok := g.Call.Fun.(*ast.SelectorExpr)
			require.True(t, ok, "a goroutine started on something unnamed")
			started = append(started, sel.Sel.Name)
			return true
		})
	}
	require.Equal(t, []string{"loop"}, started,
		"a second goroutine in this package is a second writer, and the four loop-side transitions stop excluding each other by construction")
}
