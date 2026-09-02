package backend

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

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
// THE TABLE HAS BEEN TOO COARSE FIVE TIMES, AND EACH TIME IN THE SAME WAY: it
// could not see the thing it claimed to guard.
//
//   - One row per FUNCTION with one column — "does the read hold the lock" —
//     could not say that the SECOND write a read decides, the ack of a delivery
//     it drops, was made after the section closed. So the table names the KINDS
//     of write a read decides and checks each separately.
//   - "After the first jobLock.Lock()" is not a section, and neither is "inside
//     SOME Lock..Unlock span": a body that reads, unlocks, works, re-locks and
//     writes has every call inside a section, and it is not the section the
//     read was taken in.
//   - A fact checked by SPELLING is not checked. The pairing scan matched the
//     idents openIndexKey and jobRecordKey, so a second builder in store.go
//     with the same body and another name moved an open row alone in an entry
//     nothing here saw. A builder is enumerated FROM store.go now and
//     classified by the key it RETURNS, measured by calling it.
//   - A section checked by the IDENT IT HANGS OFF is not checked either:
//     jobLock.mu.Unlock() released the exclusion and left the identity alive,
//     and read as no Unlock at all.
//   - AND A SCAN THAT ONLY WALKS FUNCTION BODIES CANNOT SEE A PACKAGE-LEVEL
//     DECLARATION. A `var carry = section.Read[jobRecord]` is a GenDecl, never
//     inside a FuncDecl, so every call site through it read as a call to
//     "carry" and the enumeration of who may read or write was silently
//     complete. So pkgFuncs collects the package level too, as a frame named
//     "(package level)", and any enumeration that finds it there fails.
//
// WHAT CHANGED UNDER THESE TESTS, and what it does to them. A section is no
// longer a span between two calls: it is a CLOSURE, and the writes are methods
// on a handle that exists only inside it (section.go). So the questions worth
// asking here changed shape too — "is this call inside a Lock..Unlock span"
// became "is this write a method on the handle", which is a fact about a
// receiver rather than about two positions, and four tests whose whole subject
// was the old spelling are gone rather than rewritten:
//
//   - which functions may be PASSED a section: nothing is passed one, because
//     nothing hands one out. The check was a comparison against the string
//     "section.Section" and would have missed ...section.Section, a pointer and
//     a struct field carrying one; its subject no longer exists.
//   - that a section is entered once and left on the way out: entering is a
//     function call now, so "left on the way out" is what a call IS.
//   - that a reading is only made by the read that took it: there is no reading
//     and no stamp. What replaced it is below — a handle is only made by the
//     one thing that took the lock.
//   - that the one read taking no section is the one that decides nothing: a
//     read decides nothing on its own now, because it carries no handle. The
//     enumeration of who reads a record survives, in the first test here.
//
// WHAT IT STILL CANNOT SEE, AND WHAT NOW DOES. Everything here is a fact about
// POSITIONS IN A FILE. "The write this read decided is made while the section
// is still running" is a fact about two instants in one execution, and no scan
// can see it: a body that hands its handle to a closure that outlives the call
// has every write textually inside the section. That property is carried by
// the compiler and the runtime instead — the handle is the only route to a
// write, and it is dead the moment the section returns — and measured in
// section_test.go. These halves are complementary and neither is redundant:
// this one says which functions may write at all and which key they may write,
// at review time; that one says whether the write happened when it was still
// allowed to.

// span is one call's argument range: from its '(' to its ')'. A body handed to
// withJob is one, and so is the closure handed to under().
type span struct{ from, to token.Pos }

func covered(spans []span, at token.Pos) bool {
	for _, s := range spans {
		if at > s.from && at < s.to {
			return true
		}
	}
	return false
}

// funcs is one non-test function of this package, and what its body does. It
// is keyed by receiver type and name — this package has two Allow methods, and
// a table keyed by name alone would silently hold one of them.
type funcs struct {
	name string
	// recv is the receiver's type name: "Service", "held", "attemptSink", or
	// "" for a function with none. IT IS THE WHOLE OF WHAT "INSIDE A SECTION"
	// MEANS NOW for a write: a method on held is reachable only from a body
	// that was handed one.
	recv  string
	calls map[string]bool
	// at and last are where each call FIRST and LAST appears. The difference
	// between them is what a section that TRIAGES on a cheap read and decides
	// on a second one looks like from here.
	at   map[string]token.Pos
	last map[string]token.Pos
	// posns is every appearance of every call, because "this write is inside
	// the gate" is a question about each call site and not about the first.
	posns map[string][]token.Pos
	// writesTo is where the body ASSIGNS to a field rather than calling
	// something — s.running[id] = stop, delete(s.running, id). A section is not
	// only about calls.
	writesTo map[string][]token.Pos
	// bodies are the argument ranges of this function's calls to withJob and
	// withJobUnbarriered: the closures that ARE its sections.
	bodies []span
	// gated are the argument ranges of its calls to under(), the one place a
	// write may be made.
	gated []span
	// literals is every composite literal type this body builds.
	literals map[string][]token.Pos
	// idents is every bare identifier it names. It is what makes "the lock is
	// touched in one place" a question about the LOCK rather than about a
	// method name: sync.Once has a Do too, and matching the verb alone put
	// Close on the list of things that open a section.
	idents map[string][]token.Pos
	// returnsHandle is whether it hands a *held back to its caller, which would
	// be a second door onto one.
	returnsHandle bool
}

// packageLevel is the frame everything outside a function body is attributed
// to. It exists because the defeat of the round before this one was exactly a
// package-level declaration: a scan that walks FuncDecls only cannot see one,
// and an enumeration that cannot see a declaration is not an enumeration.
const packageLevel = "(package level)"

// pairedQueueWrites are the queue verbs that PAIR WITH A RECORD WRITE: a job
// gets an item, loses one, or has one acknowledged, and the record says the
// same thing in the same breath. Claim and Sweep are not among them — see
// TestEveryQueueWriteIsInsideTheSectionThatDecidedIt.
var pairedQueueWrites = map[string]bool{"Push": true, "Remove": true, "Ack": true, "Nack": true}

// barrierVerbs are the pkg/v2/queue calls that take a barrier of their own:
// every one of them goes through prepare(), which refuses a follower and then
// blocks until everything committed is applied.
var barrierVerbs = map[string]bool{
	"Push": true, "Remove": true, "Ack": true, "Nack": true, "Claim": true, "Sweep": true,
}

// ── WHICH KEY, MEASURED RATHER THAN SPELLED ──────────────────────────────
//
// A key builder is enumerated FROM store.go — every declaration in it with no
// receiver and exactly one string result — and then CLASSIFIED BY THE KEY IT
// RETURNS, by calling it. Two names that produce one key are one builder as far
// as every scan in this file is concerned, which is the whole point: the defect
// was six lines, a second builder with a different name and the same body, and
// it defeated both the "ends in Key" test and the ident-matching pairing scan.
//
// The enumeration is checked against the invocation table, so a builder added
// to store.go and not invoked here fails rather than going unclassified.

// sampleKeys invokes every builder store.go declares, with one canonical set of
// inputs so that two builders producing the same key produce the same string.
func sampleKeys() map[string]string {
	const (
		sampleID       = "0123456789012345678901234567890123456789abc"
		sampleProject  = "project-of-" + testRepo
		sampleResource = "resource-1"
	)
	at := time.Unix(1756000000, 0).UTC()

	return map[string]string{
		"seg":                seg(sampleID),
		"stamp":              stamp(1),
		"jobPrefix":          jobPrefix(sampleID),
		"jobRecordKey":       jobRecordKey(sampleID),
		"attemptPrefix":      attemptPrefix(sampleID),
		"attemptLogKey":      attemptLogKey(sampleID, 1, at),
		"attemptAssetKey":    attemptAssetKey(sampleID, 1, sampleResource),
		"projectIndexPrefix": projectIndexPrefix(sampleProject),
		"projectIndexKey":    projectIndexKey(sampleProject, sampleID),
		"openIndexPrefix":    openIndexPrefix(testRepo),
		"openIndexKey":       openIndexKey(testRepo, sampleID),
		"openIndexRoot":      openIndexRoot(),
		"refLastKey":         refLastKey(testRepo, testRef),
		"terminalIndexKey":   terminalIndexKey(at, sampleID),
	}
}

// storeDeclares is every builder store.go declares: no receiver, exactly one
// string result. unseg, parseAttemptKey, parseOpenIndexKey and idFromIndexKey
// return two or three values and are readers rather than builders; encode,
// status and terminal have receivers.
func storeDeclares(t *testing.T) []string {
	t.Helper()

	var file *ast.File
	for name, f := range parsePackage(t).Files {
		if strings.HasSuffix(name, "store.go") {
			file = f
		}
	}
	require.NotNil(t, file, "store.go is the one file that builds a key")

	var out []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Body == nil {
			continue
		}
		if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
			continue
		}
		result := fn.Type.Results.List[0]
		if len(result.Names) > 1 {
			continue
		}
		if id, ok := result.Type.(*ast.Ident); ok && id.Name == "string" {
			out = append(out, fn.Name.Name)
		}
	}
	return out
}

// classes maps every builder to the key it produces. It fails when store.go and
// the invocation table above disagree, because a builder nothing calls cannot
// be classified and an unclassified builder is exactly the hole.
func classes(t *testing.T) map[string]string {
	t.Helper()
	samples := sampleKeys()
	require.ElementsMatch(t, storeDeclares(t), names(samples),
		"store.go declares a key builder sampleKeys does not invoke, or invokes one it no longer declares — "+
			"a builder is classified by the key it RETURNS, so an un-invoked one is unclassified and invisible")
	return samples
}

// sameKeyAs is every builder that produces the key `like` produces. A rename is
// not a second key.
func sameKeyAs(all map[string]string, like string) map[string]bool {
	out := map[string]bool{}
	for name, key := range all {
		if key == all[like] {
			out[name] = true
		}
	}
	return out
}

// wholeKeys are the builders that produce a KEY rather than a prefix or a
// fragment: rooted at this package's root segment, and not ending at a segment
// boundary. Those are the only expressions set() and del() may be handed.
func wholeKeys(all map[string]string) map[string]bool {
	out := map[string]bool{}
	for name, key := range all {
		if strings.HasPrefix(key, rootSegment+"/") && !strings.HasSuffix(key, "/") {
			out[name] = true
		}
	}
	return out
}

// indexRowKeys are the rows a LISTING walks: the open index reconcile repairs
// and MaxOpenPerRepo counts, the project index a caller lists by, the retention
// index. A write of one is neither a record write nor a queue write, and
// without a column of its own it was invisible to the table. It is a union of
// CLASSES, so a second spelling of any of the three is in it.
func indexRowKeys(all map[string]string) map[string]bool {
	out := map[string]bool{}
	for _, like := range []string{"openIndexKey", "projectIndexKey", "terminalIndexKey"} {
		for name := range sameKeyAs(all, like) {
			out[name] = true
		}
	}
	return out
}

// qualify names a declaration the way the tables below do: "Service.cancel",
// "held.commit", or a bare "jobRecordKey" for a function with no receiver.
func qualify(fn *ast.FuncDecl) string {
	if name := receiver(fn); name != "" {
		return name + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// receiver is the type a method hangs off, with the pointer stripped.
func receiver(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	id, ok := expr.(*ast.Ident)
	if !ok {
		return ""
	}
	return id.Name
}

// parsePackage parses this package's own non-test source.
func parsePackage(t *testing.T) *ast.Package {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)
	require.Contains(t, parsed, "backend")
	return parsed["backend"]
}

// fieldWritten names the field an assignment target writes: s.running[id] and
// s.running both answer "running".
func fieldWritten(expr ast.Expr) (string, token.Pos, bool) {
	if idx, ok := expr.(*ast.IndexExpr); ok {
		expr = idx.X
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return "", 0, false
	}
	return sel.Sel.Name, sel.Sel.NamePos, true
}

// sectionEntries are the two calls that take the lock and hand over a handle.
// A body of either is a section.
var sectionEntries = map[string]bool{"withJob": true, "withJobUnbarriered": true}

func pkgFuncs(t *testing.T) map[string]funcs {
	t.Helper()

	out := map[string]funcs{}
	level := newFuncs(packageLevel, "")

	for _, file := range parsePackage(t).Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				// EVERYTHING THAT IS NOT A FUNCTION BODY IS STILL SOMEWHERE,
				// and the round before this one was defeated by exactly that: a
				// package-level var holding a function value made every call
				// through it read as a call to the var's name. Whatever is
				// declared out here is attributed to one frame, which every
				// enumeration below then rejects by name.
				scanTop(&level, decl)
				continue
			}
			f := newFuncs(qualify(fn), receiver(fn))
			if fn.Type.Results != nil {
				for _, result := range fn.Type.Results.List {
					if strings.HasSuffix(types.ExprString(result.Type), "held") {
						f.returnsHandle = true
					}
				}
			}
			scan(&f, fn.Body)

			require.NotContains(t, out, f.name, "two declarations named %s", f.name)
			out[f.name] = f
		}
	}

	require.NotContains(t, out, packageLevel)
	out[packageLevel] = level
	return out
}

func newFuncs(name, recv string) funcs {
	return funcs{
		name:     name,
		recv:     recv,
		calls:    map[string]bool{},
		at:       map[string]token.Pos{},
		last:     map[string]token.Pos{},
		posns:    map[string][]token.Pos{},
		writesTo: map[string][]token.Pos{},
		literals: map[string][]token.Pos{},
		idents:   map[string][]token.Pos{},
	}
}

// scan records what one node does: the calls it makes, the fields it assigns,
// the composite literals it builds, and the argument ranges of the two calls
// that mean something structural — a section's body and a write's gate.
func scan(f *funcs, node ast.Node) {
	note := func(name string, pos token.Pos) {
		f.calls[name] = true
		if at, seen := f.at[name]; !seen || pos < at {
			f.at[name] = pos
		}
		if at, seen := f.last[name]; !seen || pos > at {
			f.last[name] = pos
		}
		f.posns[name] = append(f.posns[name], pos)
	}

	ast.Inspect(node, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.SelectorExpr:
			// s.readRecord, s.store.Get, jobLock.Do — the selector's last
			// segment is the name, whatever it hangs off.
			note(v.Sel.Name, v.Sel.NamePos)
		case *ast.CallExpr:
			// Bare calls, for the key builders that have no receiver.
			if id, ok := v.Fun.(*ast.Ident); ok {
				note(id.Name, id.NamePos)
				// delete(s.running, id) writes a field.
				if id.Name == "delete" && len(v.Args) > 0 {
					if name, at, ok := fieldWritten(v.Args[0]); ok {
						f.writesTo[name] = append(f.writesTo[name], at)
					}
				}
			}
			switch calleeName(v.Fun) {
			case "under":
				f.gated = append(f.gated, span{from: v.Lparen, to: v.Rparen})
			default:
				if sectionEntries[calleeName(v.Fun)] {
					f.bodies = append(f.bodies, span{from: v.Lparen, to: v.Rparen})
				}
			}
		case *ast.FuncLit:
			if v.Type.Results != nil {
				for _, result := range v.Type.Results.List {
					if strings.HasSuffix(types.ExprString(result.Type), "held") {
						f.returnsHandle = true
					}
				}
			}
		case *ast.CompositeLit:
			name := types.ExprString(v.Type)
			f.literals[name] = append(f.literals[name], v.Lbrace)
		case *ast.Ident:
			f.idents[v.Name] = append(f.idents[v.Name], v.NamePos)
		case *ast.AssignStmt:
			for _, lhs := range v.Lhs {
				if name, at, ok := fieldWritten(lhs); ok {
					f.writesTo[name] = append(f.writesTo[name], at)
				}
			}
		}
		return true
	})
}

// scanTop records what a package-level declaration does, WITHOUT counting the
// names it binds. `var jobLock section.Lock[jobRecord]` names jobLock because
// it IS jobLock; what matters is who else names it.
func scanTop(f *funcs, decl ast.Decl) {
	gen, ok := decl.(*ast.GenDecl)
	if !ok {
		return
	}
	for _, spec := range gen.Specs {
		switch v := spec.(type) {
		case *ast.ValueSpec:
			for _, value := range v.Values {
				scan(f, value)
			}
			if v.Type != nil {
				scan(f, v.Type)
			}
		case *ast.TypeSpec:
			scan(f, v.Type)
		}
	}
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
// "held.finish", "seg" is "seg", and a name this package does not declare —
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
// package's own functions. skip stops the walk at a name — readRecord() is
// skipped wherever the question is "does this WRITE a record", because every
// section reads one on the way in.
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

// theRead is the one read of a job record, skipped by every question about
// writes: a section takes it on the way in, so counting it would make every
// transition a record writer.
var theRead = map[string]bool{"readRecord": true}

func bare(qualified string) string {
	if i := strings.LastIndex(qualified, "."); i >= 0 {
		return qualified[i+1:]
	}
	return qualified
}

// writesRecord reports whether a site's read decides an entry that writes the
// job record, wherever in the call tree that entry is built. It asks for the
// KEY rather than for one spelling of it.
func writesRecord(all map[string]funcs, qualified string, recordKeys map[string]bool) bool {
	for name := range reach(all, bare(qualified), theRead) {
		if recordKeys[name] {
			return true
		}
	}
	return false
}

// writesQueue reports the same for a queue write that pairs with one.
func writesQueue(all map[string]funcs, qualified string) bool {
	for name := range reach(all, bare(qualified), theRead) {
		if pairedQueueWrites[name] {
			return true
		}
	}
	return false
}

// writesIndex reports the same for a row of one of the listings.
func writesIndex(all map[string]funcs, qualified string, rows map[string]bool) bool {
	for name := range reach(all, bare(qualified), theRead) {
		if rows[name] {
			return true
		}
	}
	return false
}

// barriers reports whether a call takes a barrier, directly or through this
// package's own functions or pkg/v2/queue's prepare().
func barriers(all map[string]funcs, call string) bool {
	if call == "Barrier" || barrierVerbs[call] {
		return true
	}
	for name := range reach(all, call, nil) {
		if name == "Barrier" || barrierVerbs[name] {
			return true
		}
	}
	return false
}

// entryWrite reports whether a call ends in an entry or a paired queue write,
// wherever in the call tree it is built.
func entryWrite(all map[string]funcs, call string) bool {
	if call == "Batch" || pairedQueueWrites[call] {
		return true
	}
	for name := range reach(all, call, theRead) {
		if name == "Batch" || pairedQueueWrites[name] {
			return true
		}
	}
	return false
}

// writesIn is every position at which a body writes something.
func writesIn(all map[string]funcs, f funcs) map[string][]token.Pos {
	out := map[string][]token.Pos{}
	for name := range f.calls {
		if theRead[name] || !entryWrite(all, name) {
			continue
		}
		out[name] = f.posns[name]
	}
	return out
}

// record() reads one record and one only — of any builder that produces a job
// record key, not of the one spelled jobRecordKey — so counting its callers
// counts every read of a job record in this package.
//
// AND WHO CALLS IT IS THE OTHER HALF. A read used to be handed the section it
// was taken in, so "who may read" was also "who may stamp"; now the read is
// taken by the entry point that took the lock, before the body runs. Its three
// callers are that entry point, mend's second read under the same holding, and
// job() — the one read in this package that decides no write and therefore
// needs no section at all.
func TestOnlyOneFunctionReadsAJobRecord(t *testing.T) {
	all := pkgFuncs(t)
	recordKeys := sameKeyAs(classes(t), "jobRecordKey")

	var readers []string
	for n, f := range all {
		if recordKeys[n] || !f.calls["Get"] {
			continue
		}
		for builder := range recordKeys {
			if f.calls[builder] {
				readers = append(readers, n)
				break
			}
		}
	}
	require.ElementsMatch(t, []string{"Service.readRecord"}, readers,
		"every read of a job record must go through readRecord(), or the population below is incomplete")

	require.ElementsMatch(t,
		[]string{"Service.enter", "held.reread", "Service.job"},
		names(callers(all, "readRecord")),
		"a fourth reader is either a section that did not go through the entry point, or a read "+
			"outside every section — and a read outside every section decides nothing, because "+
			"there is no handle attached to it")
}

// site is one SECTION and THE WRITES ITS READ DECIDES — one row per transition,
// with a column per KIND of write, because a read can decide a record entry in
// one branch and a queue write in another and a single boolean cannot say which
// of the two escaped.
type site struct {
	// record: the read decides an entry that writes the job record.
	record bool
	// queue: the read decides a push, a withdrawal or an ack.
	queue bool
	// index: the read decides a write or a delete of an INDEX ROW. Without a
	// column of its own an open-row write is neither of the two above, and a
	// site that made one fell through this table as a site that decides
	// nothing at all.
	index bool
	// unbarriered says it entered through withJobUnbarriered, which reads
	// WITHOUT a barrier — so it must take one with reread() before it decides
	// anything, and must say why that placement is the safe one.
	unbarriered bool
	why         string
}

// EVERY SECTION IS NAMED HERE, with every write it decides. Adding a transition
// means adding a line, which is the point: a site nobody thought about fails
// here rather than shipping, which is exactly what the sixth site did — and a
// site whose SECOND write leaves the section fails in section_test.go, which is
// what the ninth did.
//
// THE BARRIER IS NO LONGER A COLUMN, and that is the redesign showing through.
// It used to be one per row, because each transition took its own lead() and a
// row that forgot was a row deciding on entries this node had not applied. It
// is the entry point's now — withJob takes it before the read, once, for
// everybody — so what is left to say per row is which entry point was used, and
// only one row uses the other one.
func TestEveryReadModifyWriteOfAJobRecordIsOneSection(t *testing.T) {
	population := map[string]site{
		// jobs.go
		"Service.cancel": {record: true, queue: true, index: true},
		"Service.retry":  {record: true, queue: true, index: true},
		// queue.go
		"Service.resume": {queue: true},
		"Service.create": {record: true, queue: true, index: true},
		// runner.go
		"Service.begin":           {record: true, queue: true},
		"Service.settle":          {record: true, queue: true, index: true},
		"Service.drainDeadLetter": {record: true, queue: true, index: true},
		// reconcile.go — the one section that reads without a barrier. mend
		// triages on a read that decides nothing and takes the barrier, with
		// reread(), on the branch that deletes an open-index row, so the cost is
		// one per row that looks orphaned rather than one per open job per pass.
		// barrier_test.go measures both numbers.
		"Service.mend": {queue: true, index: true, unbarriered: true,
			why: "a barrier before every row's read is one replicated entry per open job per " +
				"pass, forever; the delete branch takes one and reads again under the same holding"},
	}

	all := pkgFuncs(t)
	keys := classes(t)
	recordKeys, rows := sameKeyAs(keys, "jobRecordKey"), indexRowKeys(keys)

	found := map[string]funcs{}
	for entry := range sectionEntries {
		for name, f := range callers(all, entry) {
			if name == "Service.withJob" || name == "Service.withJobUnbarriered" {
				continue
			}
			found[name] = f
		}
	}
	require.ElementsMatch(t, names(population), names(found),
		"a section that is not in the table above, or a name in it that is gone")

	for name, want := range population {
		f := found[name]

		require.Equal(t, want.record, writesRecord(all, name, recordKeys),
			"%s: the table and the code disagree about whether this read decides a record write", name)
		require.Equal(t, want.queue, writesQueue(all, name),
			"%s: the table and the code disagree about whether this read decides a queue write", name)
		require.Equal(t, want.index, writesIndex(all, name, rows),
			"%s: the table and the code disagree about whether this read decides an index-row write", name)

		require.Equal(t, want.unbarriered, f.calls["withJobUnbarriered"],
			"%s: the table and the code disagree about which entry point took the lock", name)
		if !want.unbarriered {
			require.Empty(t, want.why, "%s: a section behind the barrier needs no exemption", name)
			continue
		}

		// AND THE TRIAGE MUST DECIDE NOTHING. Every write the section makes
		// comes after the barrier reread() takes, or the first read is not
		// triage at all.
		require.NotEmpty(t, want.why, "%s: a section that reads without a barrier must say why that is safe", name)
		reread, ok := f.at["reread"]
		require.True(t, ok, "%s: a section that reads without a barrier must read again behind one", name)
		require.True(t, barriers(all, "reread"), "%s: and that second read must barrier", name)
		for verb, positions := range writesIn(all, f) {
			for _, at := range positions {
				require.Greater(t, at, reread,
					"%s: %s is decided before the read its barrier covers", name, verb)
			}
		}
	}

	// AND THE BARRIER IS THE ENTRY POINT'S, before the read every body decides
	// on. It was nine lines in nine transitions and is one line in one place.
	enter := all["Service.enter"]
	require.True(t, barriers(all, "lead"), "lead() must barrier, or no section has one")
	require.Less(t, enter.at["lead"], enter.at["readRecord"],
		"the entry point must barrier BEFORE the read its body's writes are decided on")
}

// EVERY QUEUE WRITE IS INSIDE THE SECTION THAT DECIDED IT — the rule jobLock's
// own comment states, checked from the write side rather than the read side.
//
// THIS IS THE ONE AN EARLIER ROUND WROTE DOWN AND DID NOT IMPLEMENT. The
// comment said "a transition holds this across the queue write that pairs with
// its record write — a push, a withdrawal, an ack", and run() acked its
// deliveries with no lock held at all, at both of its two exits. A table keyed
// by the READING function could not see it, because run reads nothing.
//
// "INSIDE THE SECTION" IS A RECEIVER NOW. The three verbs are reachable only
// from methods on held, and a held exists only inside a body — so what this
// checks is that each verb's callers are those methods and that each makes its
// call inside under(), the gate that refuses a handle whose section has
// returned.
func TestEveryQueueWriteIsInsideTheSectionThatDecidedIt(t *testing.T) {
	// verb -> the functions of this package that call it -> why it is outside a
	// section, or "" when it is inside one.
	sites := map[string]map[string]string{
		"Push":   {"held.push": ""},
		"Remove": {"held.withdraw": ""},
		"Ack":    {"held.ack": ""},
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

// inSection reports whether fn's calls to `call` are made inside a section, and
// that is now two questions with two short answers: is fn a method on the
// handle, and does it make the call inside under().
//
// IT USED TO BE A WALK OVER Lock..Unlock SPANS, up through every caller, and
// the walk was the weakness: a body that reads under the lock, releases it, and
// then makes the write its read decided has every call inside SOME span. There
// are no spans now. A write is a method on a value that exists only inside a
// body, and the gate is the one expression that makes it.
func inSection(all map[string]funcs, fn, call string) (bool, string) {
	f, known := all[fn]
	if !known {
		return false, fn + " is not a function of this package"
	}
	if f.recv != "held" {
		return false, fn + " is not a method on the handle, so nothing ever handed it a section"
	}
	positions := f.posns[call]
	if len(positions) == 0 {
		return false, fn + " does not call " + call
	}
	for _, at := range positions {
		if !covered(f.gated, at) {
			return false, fn + " calls " + call + " outside under(), which is the gate every write goes through"
		}
	}
	return true, ""
}

// inBody reports whether fn's calls to `call` are made inside one of its own
// sections — for the calls a TRANSITION makes on the handle it was given. A
// method on held is inside one by construction; a function that takes no
// section of its own is inside one only if every function that calls IT is.
func inBody(all map[string]funcs, fn, call string) (bool, string) {
	return inBodyFrom(all, fn, call, map[string]bool{})
}

func inBodyFrom(all map[string]funcs, fn, call string, seen map[string]bool) (bool, string) {
	f, known := all[fn]
	if !known {
		return false, fn + " is not a function of this package"
	}
	if f.recv == "held" {
		return true, ""
	}
	if len(f.bodies) > 0 {
		positions := f.posns[call]
		if len(positions) == 0 {
			return false, fn + " does not call " + call
		}
		for _, at := range positions {
			if !covered(f.bodies, at) {
				return false, fn + " calls " + call + " outside the section it opened"
			}
		}
		return true, ""
	}
	if seen[fn] {
		return true, ""
	}
	seen[fn] = true

	up := callers(all, bare(fn))
	if len(up) == 0 {
		return false, fn + " opens no section and nothing in this package calls it"
	}
	for n := range up {
		if ok, chain := inBodyFrom(all, n, bare(fn), seen); !ok {
			return false, fn + " -> " + chain
		}
	}
	return true, ""
}

// A RECORD AND ITS OPEN ROW MOVE IN ONE ENTRY, and mend's orphan delete is the
// one entry that moves the row alone.
//
// That pairing is what makes "this row is listed as open AND its record reads
// finished" impossible to produce within ONE applied prefix: whatever prefix a
// node has, it has applied both halves or neither. mend needs it — the whole
// reason its delete is a repair and not a guess — and it needs a barrier on top
// of it, because the listing and the read are two different prefixes
// (reconcile.go). Split across two entries by some later transition, the repair
// starts dropping rows a live job still needs.
//
// AND THE EXCEPTION IS NAMED RATHER THAN INVISIBLE. mend's own entry deletes an
// open row and writes no record, on purpose: the record it read is finished or
// gone, and the row is the only thing left to remove. While that key was a loop
// variable this scan could not see the entry at all.
//
// IT IS THE KEY THAT IS COUNTED, NOT THE SPELLING. This scan matched the idents
// openIndexKey and jobRecordKey, so a second builder in store.go with the same
// body and another name moved an open row alone in an entry nothing here saw.
// Both halves of the pairing are now CLASSES: every builder that returns the
// key openIndexKey returns, and every builder that returns the key
// jobRecordKey returns.
func TestARecordAndItsOpenRowMoveInOneEntry(t *testing.T) {
	keys := classes(t)
	recordKeys, openRowKeys := sameKeyAs(keys, "jobRecordKey"), sameKeyAs(keys, "openIndexKey")

	// mentions counts a key builder's appearances, and inEntry counts the ones
	// inside a []raft.BatchOp literal — one replicated entry. A builder named
	// anywhere else is a key written or deleted on its own.
	mentions := map[string]int{}
	inEntry := map[string]int{}
	var pairing, alone []string

	count := func(n ast.Node, into map[string]int) (record, openRow bool) {
		ast.Inspect(n, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok || !(recordKeys[id.Name] || openRowKeys[id.Name]) {
				return true
			}
			into[id.Name]++
			record = record || recordKeys[id.Name]
			openRow = openRow || openRowKeys[id.Name]
			return true
		})
		return record, openRow
	}

	for _, file := range parsePackage(t).Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || qualify(fn) == "Service.readRecord" {
				continue
			}
			count(fn.Body, mentions)

			ast.Inspect(fn.Body, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok || !isBatchOps(lit.Type) {
					return true
				}
				record, openRow := count(lit, inEntry)
				if !openRow {
					return true
				}
				if record {
					pairing = append(pairing, qualify(fn))
				} else {
					alone = append(alone, qualify(fn))
				}
				return true
			})
		}
	}

	require.ElementsMatch(t, []string{"Service.create", "Service.retry", "held.finish"}, pairing,
		"the three transitions that move a record and its open row together")
	require.ElementsMatch(t, []string{"Service.mend"}, alone,
		"mend's orphan delete is the ONE entry allowed to move an open row without the record, "+
			"and a second one is a transition that leaves a job listed as open with no record saying so")
	require.Equal(t, mentions, inEntry,
		"a job record key or an open-index row key built outside a []raft.BatchOp literal is half a transition")
}

// isBatchOps reports whether a composite literal's type is []raft.BatchOp — one
// replicated entry, which is what Store.Batch takes and the only shape in which
// this package writes anything.
func isBatchOps(expr ast.Expr) bool {
	arr, ok := expr.(*ast.ArrayType)
	if !ok {
		return false
	}
	sel, ok := arr.Elt.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "BatchOp"
}

// keyBuilder reports whether an expression is a call to one of store.go's key
// builders — the only expressions that may name a key an entry writes.
//
// IT USED TO SAY THAT AND CHECK strings.HasSuffix(name, "Key"). A name is not a
// builder: any function anywhere, called anything ending in those three
// letters, satisfied it. `builders` is the measured set — declared in store.go
// and returning a whole key rather than a prefix — so this now checks what the
// sentence above always claimed.
func keyBuilder(builders map[string]bool, expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	id, ok := call.Fun.(*ast.Ident)
	return ok && builders[id.Name]
}

// calleeName is the last segment of whatever a call hangs off: "write" for
// k.write(...), "mend" for s.mend(...), "del" for del(...).
func calleeName(fun ast.Expr) string {
	switch v := fun.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	}
	return ""
}

// params maps a declaration's parameter names to their flat positions.
func params(fn *ast.FuncDecl) map[string]int {
	out := map[string]int{}
	i := 0
	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			out[name.Name] = i
			i++
		}
	}
	return out
}

// EVERY KEY AN ENTRY WRITES IS BUILT WHERE THE ENTRY IS. set() and del() are
// this package's only two BatchOp constructors, and the key each is handed must
// be a call to one of store.go's builders — or a parameter, in which case every
// caller must build one.
//
// THIS IS WHAT MAKES THE PAIRING CHECK ABOVE A CHECK RATHER THAN A SPELLING
// TEST. mend deletes an open-index row; while that key was the loop variable
// reconcile's listing handed it, the entry named nothing and no scan over the
// source could tell what it wrote. A tenth site deleting an open row alone,
// outside any section, spelled the same way, passed everything. An opaque key
// is the hole, so an opaque key is what fails here.
func TestEveryKeyAnEntryWritesIsBuiltWhereTheEntryIs(t *testing.T) {
	pkg := parsePackage(t)
	builders := wholeKeys(classes(t))

	// sinks are declarations that take a key as a parameter: the key is built
	// by their callers instead, and every caller is checked below.
	sinks := map[string]map[int]bool{}

	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			taken := params(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				if verb := calleeName(call.Fun); verb != "set" && verb != "del" {
					return true
				}
				if keyBuilder(builders, call.Args[0]) {
					return true
				}
				if id, ok := call.Args[0].(*ast.Ident); ok {
					if i, isParam := taken[id.Name]; isParam {
						if sinks[qualify(fn)] == nil {
							sinks[qualify(fn)] = map[int]bool{}
						}
						sinks[qualify(fn)][i] = true
						return true
					}
				}
				require.Fail(t, "an entry writes a key it did not build",
					"%s: %s(%s) — the key must be a store.go builder here, or a parameter every caller builds",
					qualify(fn), calleeName(call.Fun), types.ExprString(call.Args[0]))
				return true
			})
		}
	}

	for sink, positions := range sinks {
		seen := 0
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil || qualify(fn) == sink {
					continue
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok || calleeName(call.Fun) != bare(sink) {
						return true
					}
					seen++
					for i := range positions {
						require.Greater(t, len(call.Args), i, "%s calls %s with too few arguments", qualify(fn), sink)
						require.True(t, keyBuilder(builders, call.Args[i]),
							"%s passes %s the key %q, which it did not build — the entry %s writes cannot say what it writes",
							qualify(fn), sink, types.ExprString(call.Args[i]), sink)
					}
					return true
				})
			}
		}
		require.Positive(t, seen, "%s takes a key it does not build and nothing in this package calls it", sink)
	}
}

// The write side, from the other direction: every entry that SETS a job record
// is written inside the section that read it — which for three of them means
// the commit sits inside the body they opened, and for the fourth means it is a
// method on the handle a body was handed.
//
// THE ENUMERATION IS BY KEY, NOT BY IDENT. A second builder returning the same
// key let settle write a job record while this list still read
// [create, begin, retry, finish].
func TestEveryWriteOfAJobRecordIsInsideASection(t *testing.T) {
	all := pkgFuncs(t)
	recordKeys := sameKeyAs(classes(t), "jobRecordKey")

	var writers []string
	for n, f := range all {
		if recordKeys[n] || n == "Service.readRecord" {
			continue
		}
		for builder := range recordKeys {
			if f.calls[builder] {
				writers = append(writers, n)
				break
			}
		}
	}
	require.ElementsMatch(t,
		[]string{"Service.create", "Service.begin", "Service.retry", "held.finish"}, writers)

	for _, n := range []string{"Service.create", "Service.begin", "Service.retry"} {
		require.NotEmpty(t, all[n].bodies, "%s writes a record and must open a section to do it", n)
		for _, at := range all[n].posns["commit"] {
			require.True(t, covered(all[n].bodies, at),
				"%s writes a record inside the body it opened, not after it returns", n)
		}
	}

	// finish is the shared terminal write, so it is a method on the handle and
	// its callers are the ones that must open the section — which is the same
	// sentence as before with the deadlock removed: two nested sections would
	// hang rather than race.
	require.Equal(t, "held", all["held.finish"].recv)
	for name := range callers(all, "finish") {
		held, chain := inBody(all, name, "finish")
		require.True(t, held, "%s writes a terminal record outside its section — %s", name, chain)
	}
}

// THE RUN IS REGISTERED INSIDE THE SECTION THAT CLAIMED IT, and two arguments
// in this package rest their whole weight on that.
//
// runner.go's begin says a cancel serialized AFTER the claim's section must
// find the run registered, or it writes a terminal record while a build it
// could have stopped runs on to its own deadline. section.go's withdraw
// swallows queue.ErrClaimed on the strength of it: the first of the three exits
// by which a claimed item always leaves is the holder's own ack, and what makes
// that prompt is the stop inside cancel's section reaching a run this node
// registered inside begin's.
//
// IT WAS CHECKED HERE AND ONLY HERE, and that was the hole. The scan asked
// whether the assignment sat inside SOME jobLock span, which is the one thing a
// scan can ask and cannot answer: a registration handed to run() as a closure
// built inside the section and invoked after begin returns is inside the span
// textually and outside the section in every way that matters.
//
// So the write is a method on the handle, and what is left here is the
// ENUMERATION: who may touch this map at all. THE STOP IS IN IT NOW. It only
// READS the map — and then invokes what it found, which ends a build — so a
// list of the map's WRITERS walked straight past the one write in this package
// that reaches no store at all.
func TestTheClaimAndTheRegistrationAreOneSection(t *testing.T) {
	all := pkgFuncs(t)

	require.ElementsMatch(t,
		[]string{"held.register", "held.stopRun", "Service.forget"},
		names(callers(all, "running")),
		"register records the run, stop ends it, forget drops it when it is over — a fourth "+
			"toucher of this map is a fourth opinion about what this node is running, and one "+
			"that carries no handle")

	writers := map[string][]token.Pos{}
	for n, f := range all {
		if at := f.writesTo["running"]; len(at) > 0 {
			writers[n] = at
		}
	}
	require.ElementsMatch(t, []string{"held.register", "Service.forget"}, names(writers))

	// register and stop are the checked paths, so what they do to the map must
	// be inside the gate. A map write that reached the store would be caught by
	// commit; these are caught here or nowhere.
	for _, n := range []string{"held.register", "held.stopRun"} {
		f := all[n]
		require.Equal(t, "held", f.recv, "%s must be a method on the handle", n)
		for _, at := range f.posns["running"] {
			require.True(t, covered(f.gated, at),
				"%s touches the running map outside under(), which is the gate that refuses a "+
					"handle whose section has returned", n)
		}
	}

	// And begin is register's one caller, inside the section that claimed the
	// job and after the entry that wrote the record Claimed, so the two are one
	// decision.
	require.ElementsMatch(t, []string{"Service.begin"}, names(callers(all, "register")),
		"the registration belongs to the claim that decided it")

	begin := all["Service.begin"]
	claim, wrote := begin.at["commit"]
	require.True(t, wrote, "begin must write the claim itself, or there is no section to be inside of")
	for _, at := range begin.posns["register"] {
		require.True(t, covered(begin.bodies, at),
			"begin must register the run inside the body it opened, or a cancel serialized "+
				"after that section finds nothing to stop")
		require.Greater(t, at, claim,
			"and after the entry that writes the record Claimed, so the two are one decision")
	}

	// And the stop's caller is cancel, inside its own section: the record write
	// and the stop are one decision the same way.
	require.ElementsMatch(t, []string{"Service.cancel"}, names(callers(all, "stopRun")),
		"a second caller of the stop is a second opinion about which read decided to end a build")
	held, chain := inBody(all, "Service.cancel", "stopRun")
	require.True(t, held, "cancel stops a run outside the section that decided it — %s", chain)
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
	var started []string
	for _, file := range parsePackage(t).Files {
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

// EVERY WRITE IS A METHOD ON THE HANDLE, AND EVERY ONE OF THEM WRITES INSIDE
// under().
//
// commit is this package's only path to Store.Batch; push, ack and withdraw are
// the only paths to the three queue writes that pair with a record write; and
// register and stop are the only paths to the map that says what this node is
// running. Each is a method on held — so a helper that wants to write takes the
// handle, which is a parameter list rather than a scan — and each makes its
// write inside under(), which refuses a handle whose section has returned.
//
// THE RECEIVER IS THE POINT. A write used to take a reading: unforgeable, but
// MINTABLE, because the thing that stamped one was exported and generic over an
// arbitrary closure. So a body inside a live section could put a live stamp on
// any value at all, and what the check proved was that a section was running at
// the instant the stamp was made. There is nothing to mint now.
//
// THIS IS THE STATIC HALF OF section.go, and it is the half that cannot be a
// type: Go has no way to scope a struct field to a method set, so s.store.Batch
// is an expression any function in this package can write. What that expression
// CANNOT do is name a section — and this is what says it must not be written.
//
// attemptSink.write is the one exemption and it is named rather than implicit:
// a log or asset row is content keyed by (job, attempt), written DURING the
// build and outside every section on purpose, and no read of a job record
// decides it.
func TestEveryWriteIsMadeThroughTheHandleThatReadTheRecord(t *testing.T) {
	all := pkgFuncs(t)

	require.ElementsMatch(t, []string{"held.commit", "attemptSink.write"},
		names(callers(all, "Batch")),
		"an entry written outside commit() is an entry no section decided — and a handle is the "+
			"only thing that can say the section it was decided in is still running")

	// the checked path -> the write it is the only path to. register's and
	// stop's are a map rather than a call, which is exactly why they had no
	// path at all until they were methods here.
	writes := map[string]string{
		"held.commit":   "Batch",
		"held.push":     "Push",
		"held.ack":      "Ack",
		"held.withdraw": "Remove",
		"held.register": "running",
		"held.stopRun":  "running",
	}
	for fn, verb := range writes {
		f, known := all[fn]
		require.True(t, known, "%s is gone, and with it a checked path to a write", fn)
		require.Equal(t, "held", f.recv,
			"%s must be a method on the handle, or a caller with no section can reach the write", fn)
		require.NotEmpty(t, f.gated, "%s writes without the gate that refuses a returned section", fn)
		for _, at := range f.posns[verb] {
			require.True(t, covered(f.gated, at),
				"%s makes its write outside under(), which answers a question it no longer has", fn)
		}
	}

	// AND NOTHING REACHES PAST THE HANDLE INTO THE HOLDING. Write and Reread
	// are backend/section's two doors and each has exactly one caller here: the
	// gate every write is an argument to, and mend's second read. They are
	// generic over a closure, so a body that called j.in.Reread with one of its
	// own would be deciding on a value nothing read — which is the shape that
	// defeated the round before this one, one level in. It buys nothing there
	// (a body inside a live section can already put any value in an entry) and
	// it is still not a thing this package should be able to spell.
	require.ElementsMatch(t, []string{"held.under"}, onTheHolding(t, "Write"),
		"the gate is the only thing that asks the holding to write")
	require.ElementsMatch(t, []string{"held.reread"}, onTheHolding(t, "Reread"),
		"and the one second read is the only thing that replaces what a holding carries")

	// AND NOTHING ELSE IS A WRITE METHOD. A seventh method on held that reached
	// a store or the map without the gate is the hole this list would not
	// notice, so the list is closed from the other side too.
	for name, f := range all {
		if f.recv != "held" {
			continue
		}
		if _, checked := writes[name]; checked {
			continue
		}
		for verb := range map[string]bool{"Batch": true, "Push": true, "Ack": true, "Remove": true, "Nack": true, "running": true} {
			require.False(t, f.calls[verb],
				"%s writes %s and is not one of the checked paths above", name, verb)
		}
	}
}

// onTheHolding is every function that calls verb ON A HANDLE'S HOLDING —
// <anything>.in.verb(...) — including at the package level.
//
// IT MATCHES THE FIELD AND NOT THE VARIABLE, because the variable is whatever
// the body called its handle, and it does not match the verb alone because a
// verb is not a receiver: sha256's hash has a Write too, and jobID calls it.
func onTheHolding(t *testing.T, verb string) []string {
	t.Helper()

	found := map[string]bool{}
	for _, file := range parsePackage(t).Files {
		for _, decl := range file.Decls {
			owner, node := packageLevel, ast.Node(decl)
			if fn, ok := decl.(*ast.FuncDecl); ok {
				if fn.Body == nil {
					continue
				}
				owner, node = qualify(fn), fn.Body
			}
			ast.Inspect(node, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != verb {
					return true
				}
				if base, ok := sel.X.(*ast.SelectorExpr); ok && base.Sel.Name == "in" {
					found[owner] = true
				}
				return true
			})
		}
	}
	return names(found)
}

// A HANDLE IS ONLY MADE BY THE ONE THING THAT TOOK THE LOCK.
//
// The authority a write carries is the holding inside the handle, and only
// backend/section can make one live — but held is this package's own struct, so
// this package can assemble one around whatever holding it has. That is not the
// hole it looks like: a handle assembled outside a running body carries the
// zero holding and is refused (section_test.go), and one assembled inside a
// running body carries exactly the authority that body already had. What WOULD
// be a hole is a second way to obtain a live one — a factory, a stashed
// constructor, a function that hands one back — because then "the handle is the
// section" stops being true.
//
// SO THIS IS SCANNED OVER THE WHOLE FILE AND NOT OVER FUNCTION BODIES. The
// defeat of the round before this one was a package-level `var` holding the
// minting function: never inside a FuncDecl, so never scanned, and every call
// through it read as a call to the var's name.
func TestAHandleIsOnlyMadeByTheOneThatTookTheLock(t *testing.T) {
	all := pkgFuncs(t)

	var builds []string
	for name, f := range all {
		if len(f.literals["held"]) > 0 || len(f.literals["&held"]) > 0 {
			builds = append(builds, name)
		}
	}
	require.ElementsMatch(t, []string{"Service.enter"}, builds,
		"a handle assembled anywhere but the one place that took the lock is a section nobody entered")

	touches := map[string]bool{}
	for name, f := range all {
		if len(f.idents["jobLock"]) > 0 {
			touches[name] = true
		}
	}
	require.ElementsMatch(t, []string{"Service.enter"}, names(touches),
		"the lock is named in one place and that place is the one that hands out a handle — a "+
			"second mention of it is a second door onto a live holding, including one declared "+
			"at the package level, where a scan over function bodies would not have seen it. "+
			"IT IS THE LOCK THAT IS COUNTED AND NOT THE VERB: sync.Once has a Do as well, and "+
			"matching the method name put Close on this list")

	require.ElementsMatch(t, []string{"Service.withJob", "Service.withJobUnbarriered"},
		names(callers(all, "enter")),
		"the two entry points are the two barriers policies, and nothing else opens a section")

	for name, f := range all {
		require.False(t, f.returnsHandle,
			"%s hands a handle back to its caller, which is a live section outliving the call "+
				"that took it — the one thing withJob's shape exists to make impossible", name)
	}
}

// A SECTION IS NEVER OPENED INSIDE ANOTHER, and this one is about a hazard the
// redesign introduced rather than one it closed: jobLock is a plain mutex, so a
// body that reached for withJob again would deadlock against itself — silently,
// as a test that hangs rather than a test that fails.
//
// Before, the same mistake was two Enters in one body, which was a defect
// (two sections, one read) rather than a hang. It is worth an enumeration for
// the same reason the goroutine count is: it holds by arithmetic over what the
// bodies call, and nothing else would notice it going.
func TestASectionIsNeverOpenedInsideAnother(t *testing.T) {
	all := pkgFuncs(t)

	for name, f := range all {
		if len(f.bodies) == 0 {
			continue
		}
		inside := map[string]bool{}
		for call, positions := range f.posns {
			for _, at := range positions {
				if covered(f.bodies, at) {
					inside[call] = true
				}
			}
		}
		for call := range inside {
			for _, reached := range append(names(reach(all, call, nil)), call) {
				require.False(t, sectionEntries[reached] || reached == "enter",
					"%s opens a section inside one, through %s — jobLock is not reentrant, so "+
						"that is a deadlock rather than a defect somebody would see", name, call)
			}
		}
	}
}

// THERE IS ONE SECTION LOCK IN THIS PACKAGE AND IT IS jobLock. A second one is
// a second exclusion over the same job table — two writers that serialize
// against nothing.
func TestThereIsOneSectionLockAndItIsjobLock(t *testing.T) {
	var locks []string
	for _, file := range parsePackage(t).Files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok || value.Type == nil || !strings.HasPrefix(types.ExprString(value.Type), "section.Lock") {
					continue
				}
				for _, name := range value.Names {
					locks = append(locks, name.Name)
				}
			}
		}
	}
	require.Equal(t, []string{"jobLock"}, locks,
		"a second section lock is a second opinion about which writes exclude each other")

	// And it is over the job record, so every body is handed one: a lock over
	// something else would be a section that decides on a different fact.
	var over []string
	for _, file := range parsePackage(t).Files {
		ast.Inspect(file, func(n ast.Node) bool {
			value, ok := n.(*ast.ValueSpec)
			if !ok || value.Type == nil {
				return true
			}
			if name := types.ExprString(value.Type); strings.HasPrefix(name, "section.Lock") {
				over = append(over, name)
			}
			return true
		})
	}
	require.Equal(t, []string{"section.Lock[jobRecord]"}, over)
	require.True(t, slices.IsSorted(locks))
}
