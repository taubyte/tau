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
// THE FIRST VERSION OF THIS TABLE WAS ITSELF TOO COARSE, and that is the second
// defect it now carries. It had one row per FUNCTION and one column — "does the
// read hold the lock" — so it could say that begin's read was behind jobLock
// and could not say that the SECOND write that read decided, the ack of a
// delivery it drops, was made after the section closed. One read, two branches,
// two writes, one of them loose. The table below therefore names the KINDS of
// write a read decides, and checks each of them separately against the code.
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
	// at is where each call first appears, which is what lets a test say
	// "before the read" rather than only "somewhere in the body".
	at     map[string]token.Pos
	locks  bool
	lockAt token.Pos
}

// pairedQueueWrites are the queue verbs that PAIR WITH A RECORD WRITE: a job
// gets an item, loses one, or has one acknowledged, and the record says the
// same thing in the same breath. Claim and Sweep are not among them — see
// TestEveryQueueWriteIsInsideTheSectionThatDecidedIt.
var pairedQueueWrites = map[string]bool{"Push": true, "Remove": true, "Ack": true, "Nack": true}

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
			f := funcs{name: qualify(fn), calls: map[string]bool{}, at: map[string]token.Pos{}}
			note := func(name string, pos token.Pos) {
				f.calls[name] = true
				if at, seen := f.at[name]; !seen || pos < at {
					f.at[name] = pos
				}
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.SelectorExpr:
					// s.record, s.store.Get, jobLock.Lock — the selector's last
					// segment is the name, whatever it hangs off.
					note(v.Sel.Name, v.Sel.NamePos)
					if id, ok := v.X.(*ast.Ident); ok && id.Name == "jobLock" && v.Sel.Name == "Lock" {
						f.locks = true
						f.lockAt = v.Sel.NamePos
					}
				case *ast.CallExpr:
					// Bare calls, for the key builders that have no receiver.
					if id, ok := v.Fun.(*ast.Ident); ok {
						note(id.Name, id.NamePos)
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

// decls maps a call name back to the declarations it can mean: "finish" is
// "Service.finish", "seg" is "seg", and a name this package does not declare —
// "Push", "Batch" — maps to nothing.
func decls(all map[string]funcs, call string) []string {
	var out []string
	for n := range all {
		if n == call || strings.HasSuffix(n, "."+call) {
			out = append(out, n)
		}
	}
	return out
}

// reach is every call name a function makes, transitively through this
// package's own functions. skip stops the walk at a name — record() is skipped
// wherever the question is "does this WRITE a record", because record() reads
// jobRecordKey and every site calls it.
func reach(all map[string]funcs, start string, skip map[string]bool) map[string]bool {
	out := map[string]bool{}
	var walk func(string)
	walk = func(name string) {
		for _, d := range decls(all, name) {
			for c := range all[d].calls {
				if out[c] || skip[c] {
					continue
				}
				out[c] = true
				walk(c)
			}
		}
	}
	walk(start)
	return out
}

func bare(qualified string) string {
	if i := strings.LastIndex(qualified, "."); i >= 0 {
		return qualified[i+1:]
	}
	return qualified
}

// writesRecord reports whether a site's read decides an entry that writes the
// job record, wherever in the call tree that entry is built.
func writesRecord(all map[string]funcs, qualified string) bool {
	return reach(all, bare(qualified), map[string]bool{"record": true})["jobRecordKey"]
}

// writesQueue reports the same for a queue write that pairs with one.
func writesQueue(all map[string]funcs, qualified string) bool {
	for name := range reach(all, bare(qualified), map[string]bool{"record": true}) {
		if pairedQueueWrites[name] {
			return true
		}
	}
	return false
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

// site is one read of a job record and THE WRITES THAT READ DECIDES — one row
// per read, with a column per KIND of write, because a read can decide a record
// entry in one branch and a queue write in another and a single boolean cannot
// say which of the two escaped its section.
type site struct {
	// record: the read decides an entry that writes the job record.
	record bool
	// queue: the read decides a push, a withdrawal or an ack.
	queue bool
}

func (s site) section() bool { return s.record || s.queue }

// EVERY READ OF A JOB RECORD IS NAMED HERE, with every write it decides. Adding
// a site means adding a line, which
// is the point: a site nobody thought about fails here rather than shipping,
// which is exactly what the sixth site did — and a site whose SECOND write
// leaves the section fails here too, which is what the ninth did.
func TestEveryReadModifyWriteOfAJobRecordIsOneSection(t *testing.T) {
	population := map[string]site{
		// jobs.go
		"Service.job":    {}, // a read shape: it decides no write at all
		"Service.cancel": {record: true, queue: true},
		"Service.retry":  {record: true, queue: true},
		// queue.go
		"Service.resume": {queue: true},
		"Service.create": {record: true, queue: true},
		// runner.go
		"Service.begin":           {record: true, queue: true},
		"Service.settle":          {record: true, queue: true},
		"Service.drainDeadLetter": {record: true, queue: true},
		// reconcile.go
		"Service.mend": {queue: true},
	}

	all := pkgFuncs(t)
	found := callers(all, "record")
	delete(found, "Service.record")
	require.ElementsMatch(t, names(population), names(found),
		"a read of a job record that is not in the table above, or a name in it that is gone")

	for name, want := range population {
		f := found[name]

		require.Equal(t, want.section(), f.locks,
			"%s: the read and the entries it decides must be one section", name)
		require.Equal(t, want.record, writesRecord(all, name),
			"%s: the table and the code disagree about whether this read decides a record write", name)
		require.Equal(t, want.queue, writesQueue(all, name),
			"%s: the table and the code disagree about whether this read decides a queue write", name)
	}
}

// EVERY QUEUE WRITE IS INSIDE THE SECTION THAT DECIDED IT — the rule jobLock's
// own comment states, checked from the write side rather than the read side.
//
// THIS IS THE ONE THE PREVIOUS ROUND WROTE DOWN AND DID NOT IMPLEMENT. The
// comment said "a transition holds this across the queue write that pairs with
// its record write — a push, a withdrawal, an ack", and run() acked its
// deliveries with no lock held at all, at both of its two exits. A table keyed
// by the READING function could not see it, because run reads nothing.
func TestEveryQueueWriteIsInsideTheSectionThatDecidedIt(t *testing.T) {
	// verb -> the functions of this package that call it -> why it is outside a
	// section, or "" when it is inside one.
	sites := map[string]map[string]string{
		"Push":   {"Service.requeue": "", "Service.retry": "", "Service.mend": ""},
		"Remove": {"Service.withdraw": ""},
		"Ack":    {"Service.ack": "", "Service.drainDeadLetter": ""},
		"Nack":   {},
		"Claim": {
			"Service.step": "the queue's own transition, taken under the queue's own group lock " +
				"and decided on no job record. The record write it pairs with is begin's, and " +
				"THAT is a section: a claim whose record turns out to be finished is dropped " +
				"there and acked there.",
			"Service.drainDeadLetter": "the same, one queue over. The dead letter's group lock is " +
				"what makes its claim exclusive; the record write it pairs with is inside the " +
				"section below it.",
		},
		"Sweep": {
			"Service.step": "moves expired claims on and hands nothing out. It reads no job " +
				"record and decides nothing on one.",
		},
	}

	all := pkgFuncs(t)
	for verb, want := range sites {
		found := callers(all, verb)
		require.ElementsMatch(t, names(want), names(found),
			"%s is called somewhere the table above does not name", verb)

		for name, why := range want {
			held, chain := inSection(all, name, verb)
			if why != "" {
				require.False(t, held,
					"%s: %s is inside a section now, so the exemption above is stale", verb, name)
				continue
			}
			require.True(t, held,
				"%s: %s writes the queue outside the section that decided it — %s", verb, name, chain)
		}
	}
}

// inSection reports whether fn's call to `call` is made under jobLock — which
// is a question about the CALL SITE and not about the function: drainDeadLetter
// takes the lock and claims the dead letter before it, so "drainDeadLetter
// locks" is true and says nothing about which of its two queue writes is
// covered.
//
// A function that takes no lock of its own is covered only if every function
// that calls IT is covered, which is what makes ack(), requeue() and withdraw()
// helpers rather than holes. It returns the chain to the frame that broke,
// because "some caller somewhere" is not a useful failure.
func inSection(all map[string]funcs, fn, call string) (bool, string) {
	return inSectionFrom(all, fn, call, map[string]bool{})
}

func inSectionFrom(all map[string]funcs, fn, call string, seen map[string]bool) (bool, string) {
	f, known := all[fn]
	if !known {
		return false, fn + " is not a function of this package"
	}
	if f.locks {
		if at, ok := f.at[call]; ok && at > f.lockAt {
			return true, ""
		}
		return false, fn + " calls " + call + " before it takes jobLock"
	}
	if seen[fn] {
		return true, ""
	}
	seen[fn] = true

	up := callers(all, bare(fn))
	if len(up) == 0 {
		return false, fn + " holds no lock and nothing in this package calls it"
	}
	for n := range up {
		if ok, chain := inSectionFrom(all, n, bare(fn), seen); !ok {
			return false, fn + " -> " + chain
		}
	}
	return true, ""
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
