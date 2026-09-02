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
// THE TABLE HAS BEEN TOO COARSE TWICE, AND EACH TIME IN THE SAME WAY: it could
// not see the thing it claimed to guard.
//
//   - One row per FUNCTION with one column — "does the read hold the lock" —
//     could not say that the SECOND write a read decides, the ack of a delivery
//     it drops, was made after the section closed. So the table names the KINDS
//     of write a read decides and checks each separately.
//   - "After the first jobLock.Lock()" is not a section. Nothing recorded an
//     Unlock at all, so a site that read under the lock, released it, and then
//     made the queue write its read decided — run's original shape, the defect
//     this all started from — was reported as covered. So a section is now a
//     Lock..Unlock span, and a call is judged by where IT is.
//   - And a fact checked by SPELLING is not checked. mend deletes an
//     open-index row, and while that key was a loop variable carried down from
//     reconcile's listing, the pairing scan below could not see the entry at
//     all — nor could it see a tenth site that deleted one outside any section.
//     So an entry's key must be built where the entry is, and an index-row
//     write is a column of the table rather than something that falls through
//     it as neither a record write nor a queue write.
//   - And a SECTION CHECKED BY THE IDENT IT HANGS OFF is not checked either.
//     jobLockCall matched a selector whose X is the ident jobLock, so
//     jobLock.mu.Unlock() — which released the mutual exclusion and left the
//     identity alive behind it — was not an Unlock as far as this file was
//     concerned, and the body read as one clean section. The mutex is in
//     another package now and cannot be reached; what is scanned here is the
//     VERB, Enter and Leave, on whatever it is called.
//   - And a KEY BUILDER checked by its NAME is not checked either. keyBuilder's
//     own comment said "a call to one of store.go's key builders" and its body
//     said strings.HasSuffix(name, "Key"); the pairing scan matched the idents
//     openIndexKey and jobRecordKey. A second builder in store.go returning a
//     BYTE-IDENTICAL key, called anything, let an open-index row be deleted
//     alone and a job record be written by a function no scan listed as a
//     writer. So a builder is now enumerated FROM store.go and classified by
//     the key it RETURNS, measured by calling it.
//
// So the enumeration is mechanical and it is checked. record() is the only
// reader of a job record key — TestOnlyOneFunctionReadsAJobRecord is that claim
// — so the calls to it ARE every read of a job record there is, and every one
// of them is named below with what it is allowed to be.
//
// It reads this package's own source, the way services/build/boundary_test.go
// reads the two roles' import lists, and for the same reason: this is the kind
// of property that decays without anyone noticing.
//
// WHAT IT CANNOT SEE, AND WHAT NOW DOES. Everything here is a fact about
// POSITIONS IN A FILE. "The write this read decided is made while the same
// section is still held" is a fact about two instants in one execution, and no
// scan can see it: a body that reads, leaves, works, re-enters and writes has
// every call inside a section, and it is not the section the read was taken in.
// That property is carried by the compiler instead — every write takes a
// reading, and backend/section is what makes one unforgeable — and measured in
// section_test.go. These halves are complementary and neither is redundant:
// this one says which functions may write and which key they may write, at
// review time; that one says whether the write happened when it was still
// allowed to.

// span is one [Lock, Unlock) interval of jobLock in a body.
type span struct{ from, to token.Pos }

// funcs is one non-test function of this package, and what its body does. It
// is keyed by receiver type and name — this package has two Allow methods, and
// a table keyed by name alone would silently hold one of them.
type funcs struct {
	name  string
	calls map[string]bool
	// at and last are where each call FIRST and LAST appears. The difference
	// between them is what a section that TRIAGES on a cheap read and decides
	// on a second one looks like from here.
	at   map[string]token.Pos
	last map[string]token.Pos
	// posns is every appearance of every call, because "this write is inside
	// the section" is a question about each call site and not about the first.
	posns map[string][]token.Pos
	// writesTo is where the body ASSIGNS to a field rather than calling
	// something — s.running[id] = stop, delete(s.running, id). A section is not
	// only about calls.
	writesTo map[string][]token.Pos
	// sections are the jobLock spans. A DEFERRED Leave runs at the end of the
	// body and so closes nothing here; a plain one closes where it is written,
	// which is the shape "after the first Enter" could not see.
	sections []span
	// enters and plainLeaves are the raw positions the spans were built from.
	// TestASectionIsEnteredOnceAndLeftOnTheWayOut reads them: two Enters in one
	// body are two sections, and a write decided in the first and made in the
	// second is exactly what "inside SOME span" reports as covered.
	enters      []token.Pos
	plainLeaves []token.Pos
	end         token.Pos
}

func (f funcs) sectioned() bool { return len(f.sections) > 0 }

// covered reports whether a position is inside one of the body's jobLock
// spans.
func (f funcs) covered(at token.Pos) bool {
	for _, s := range f.sections {
		if at > s.from && at < s.to {
			return true
		}
	}
	return false
}

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

// sectionCall reports whether a selector is an Enter or a Leave, WHATEVER IT
// HANGS OFF.
//
// It used to require the ident jobLock, which is how jobLock.mu.Unlock() —
// releasing the exclusion and leaving the identity alive — read as no Unlock at
// all. There is no reachable mutex to unlock any more, and there is no second
// lock either (TestThereIsOneSectionLockAndItIsjobLock), so the verb is the
// whole of what has to be matched: a section is entered by Enter and left by
// Leave, on the value Enter handed back.
func sectionCall(n ast.Node, verb string) (token.Pos, bool) {
	sel, ok := n.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != verb {
		return 0, false
	}
	return sel.Sel.NamePos, true
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

func pkgFuncs(t *testing.T) map[string]funcs {
	t.Helper()

	out := map[string]funcs{}
	for _, file := range parsePackage(t).Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			f := funcs{
				name:     qualify(fn),
				calls:    map[string]bool{},
				at:       map[string]token.Pos{},
				last:     map[string]token.Pos{},
				posns:    map[string][]token.Pos{},
				writesTo: map[string][]token.Pos{},
				end:      fn.Body.End(),
			}

			// A deferred Leave runs at the end of the body, so it does not
			// close the section where it is written.
			deferred := map[token.Pos]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				d, ok := n.(*ast.DeferStmt)
				if !ok {
					return true
				}
				ast.Inspect(d.Call, func(n ast.Node) bool {
					if at, ok := sectionCall(n, "Leave"); ok {
						deferred[at] = true
					}
					return true
				})
				return true
			})

			var enters, leaves []token.Pos
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
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.SelectorExpr:
					// s.record, s.store.Get, jobLock.Enter — the selector's last
					// segment is the name, whatever it hangs off.
					note(v.Sel.Name, v.Sel.NamePos)
					if at, ok := sectionCall(v, "Enter"); ok {
						enters = append(enters, at)
					}
					if at, ok := sectionCall(v, "Leave"); ok && !deferred[at] {
						leaves = append(leaves, at)
					}
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
				case *ast.AssignStmt:
					for _, lhs := range v.Lhs {
						if name, at, ok := fieldWritten(lhs); ok {
							f.writesTo[name] = append(f.writesTo[name], at)
						}
					}
				}
				return true
			})

			slices.Sort(enters)
			slices.Sort(leaves)
			f.enters, f.plainLeaves = enters, leaves
			for _, from := range enters {
				to := f.end
				for _, u := range leaves {
					if u > from {
						to = u
						break
					}
				}
				f.sections = append(f.sections, span{from: from, to: to})
			}

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
// job record, wherever in the call tree that entry is built. It asks for the
// KEY rather than for one spelling of it.
func writesRecord(all map[string]funcs, qualified string, recordKeys map[string]bool) bool {
	for name := range reach(all, bare(qualified), map[string]bool{"record": true}) {
		if recordKeys[name] {
			return true
		}
	}
	return false
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

// writesIndex reports the same for a row of one of the listings.
func writesIndex(all map[string]funcs, qualified string, rows map[string]bool) bool {
	for name := range reach(all, bare(qualified), map[string]bool{"record": true}) {
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
	for name := range reach(all, call, map[string]bool{"record": true}) {
		if name == "Batch" || pairedQueueWrites[name] {
			return true
		}
	}
	return false
}

// writesIn is every position at which a section writes something.
func writesIn(all map[string]funcs, f funcs) map[string][]token.Pos {
	out := map[string][]token.Pos{}
	for name := range f.calls {
		if name == "record" || !entryWrite(all, name) {
			continue
		}
		out[name] = f.posns[name]
	}
	return out
}

// barriersBefore is every call a section makes between taking jobLock and
// limit that takes a barrier. A section with none of them is deciding on state
// this node may not have applied yet.
func barriersBefore(all map[string]funcs, f funcs, limit token.Pos) []string {
	var out []string
	for name := range f.calls {
		at, ok := f.at[name]
		if !ok || !f.covered(at) || at >= limit {
			continue
		}
		if barriers(all, name) {
			out = append(out, name)
		}
	}
	return out
}

// record() is the only reader of a job record KEY — of any builder that
// produces one, not of the one spelled jobRecordKey — so counting its callers
// counts every read of a job record in this package. If that stops being true
// the enumeration below stops being an enumeration.
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
	// index: the read decides a write or a delete of an INDEX ROW. Without a
	// column of its own an open-row write is neither of the two above, and a
	// site that made one fell through this table as a site that decides
	// nothing at all.
	index bool
	// barrier names the call that barriers between the lock and the read the
	// section's writes are decided on. A section that takes none is deciding on
	// entries this node may not have applied, so an empty one has to say in why
	// that a stale read is harmless.
	barrier string
	// rereads says the deciding read is not the first one: the section triages
	// on a cheap read and pays for the barrier only on the branch that needs
	// it. Then the barrier is before the LAST read, and every write the section
	// makes comes after that read rather than before it.
	rereads bool
	why     string
}

func (s site) section() bool { return s.record || s.queue || s.index }

// EVERY READ OF A JOB RECORD IS NAMED HERE, with every write it decides and the
// barrier the section takes before it. Adding a site means adding a line, which
// is the point: a site nobody thought about fails here rather than shipping,
// which is exactly what the sixth site did — and a site whose SECOND write
// leaves the section fails here too, which is what the ninth did.
func TestEveryReadModifyWriteOfAJobRecordIsOneSection(t *testing.T) {
	population := map[string]site{
		// jobs.go
		"Service.job":    {}, // a read shape: it decides no write at all
		"Service.cancel": {record: true, queue: true, index: true, barrier: "lead"},
		"Service.retry":  {record: true, queue: true, index: true, barrier: "lead"},
		// queue.go
		"Service.resume": {queue: true, barrier: "lead"},
		"Service.create": {record: true, queue: true, index: true, barrier: "lead"},
		// runner.go
		"Service.begin": {record: true, queue: true, barrier: "lead"},
		// settle's barrier WAS its own ack — queue.Ack goes through prepare(),
		// which refuses a follower and blocks until everything committed is
		// applied — taken before the read so that the record was read at an
		// applied prefix. A write taken before the read cannot carry one, so
		// the ack took a bare section instead, and an ack that names a section
		// rather than a read is an ack any live section can make. It is lead()
		// now, like every other row here, and the ack is an ordinary write
		// behind the read: one more replicated entry per settled build
		// (barrier_test.go), and no exception in the rule.
		"Service.settle":          {record: true, queue: true, index: true, barrier: "lead"},
		"Service.drainDeadLetter": {record: true, queue: true, index: true, barrier: "lead"},
		// reconcile.go — the one section whose barrier is not before its FIRST
		// read. mend triages on a stale read that decides nothing, and takes
		// the barrier on the branch that deletes an open-index row, so the cost
		// is one per row that looks orphaned rather than one per open job per
		// pass. barrier_test.go measures both numbers.
		"Service.mend": {queue: true, index: true, barrier: "lead", rereads: true},
	}

	all := pkgFuncs(t)
	keys := classes(t)
	recordKeys, rows := sameKeyAs(keys, "jobRecordKey"), indexRowKeys(keys)

	found := callers(all, "record")
	delete(found, "Service.record")
	require.ElementsMatch(t, names(population), names(found),
		"a read of a job record that is not in the table above, or a name in it that is gone")

	for name, want := range population {
		f := found[name]

		require.Equal(t, want.section(), f.sectioned(),
			"%s: the read and the entries it decides must be one section", name)
		require.Equal(t, want.record, writesRecord(all, name, recordKeys),
			"%s: the table and the code disagree about whether this read decides a record write", name)
		require.Equal(t, want.queue, writesQueue(all, name),
			"%s: the table and the code disagree about whether this read decides a queue write", name)
		require.Equal(t, want.index, writesIndex(all, name, rows),
			"%s: the table and the code disagree about whether this read decides an index-row write", name)

		if !want.section() {
			continue
		}

		deciding := f.at["record"]
		if want.rereads {
			require.NotEqual(t, f.at["record"], f.last["record"],
				"%s: a section that triages must read the record again after its barrier", name)
			deciding = f.last["record"]

			// AND THE TRIAGE MUST DECIDE NOTHING. Every write the section makes
			// comes after the read the barrier covers, or the first read is not
			// triage at all.
			for verb, positions := range writesIn(all, f) {
				for _, at := range positions {
					require.Greater(t, at, deciding,
						"%s: %s is decided before the read its barrier covers", name, verb)
				}
			}
		}

		taken := barriersBefore(all, f, deciding)
		if want.barrier == "" {
			require.NotEmpty(t, want.why, "%s: a section with no barrier must say why that is safe", name)
			require.Empty(t, taken, "%s: it does take a barrier now, so the exemption above is stale", name)
			continue
		}
		require.Empty(t, want.why, "%s: a section with a barrier needs no exemption", name)
		require.Contains(t, taken, want.barrier,
			"%s: %s must be called inside the section and before the deciding read, and must barrier", name, want.barrier)
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
		"Push":   {"Service.push": ""},
		"Remove": {"Service.withdraw": ""},
		"Ack":    {"Service.ack": ""},
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

// inSection reports whether fn's calls to `call` are made under jobLock — which
// is a question about the CALL SITES and not about the function: drainDeadLetter
// takes the lock and claims the dead letter before it, so "drainDeadLetter
// locks" is true and says nothing about which of its two queue writes is
// covered. Every call site must be covered, because one of two that is not is
// still a hole.
//
// A SECTION IS Lock..Unlock, not "after the first Lock". A body that reads under
// the lock, releases it, and then makes the write its read decided is the
// original shape of run(), and a check that only knew where the Lock was called
// it covered.
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
	if f.sectioned() {
		positions := f.posns[call]
		if len(positions) == 0 {
			return false, fn + " does not call " + call
		}
		for _, at := range positions {
			if !f.covered(at) {
				return false, fn + " calls " + call + " outside its jobLock section"
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
		return false, fn + " holds no lock and nothing in this package calls it"
	}
	for n := range up {
		if ok, chain := inSectionFrom(all, n, bare(fn), seen); !ok {
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
			if !ok || fn.Body == nil || qualify(fn) == "Service.record" {
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

	require.ElementsMatch(t, []string{"Service.create", "Service.retry", "Service.finish"}, pairing,
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
// is written by a function that holds jobLock, or by finish, whose own callers
// all do.
//
// THE ENUMERATION IS BY KEY, NOT BY IDENT. A second builder returning the same
// key let settle write a job record while this list still read
// [create, begin, retry, finish].
func TestEveryWriteOfAJobRecordIsInsideASection(t *testing.T) {
	all := pkgFuncs(t)
	recordKeys := sameKeyAs(classes(t), "jobRecordKey")

	var writers []string
	for n, f := range all {
		if recordKeys[n] || n == "Service.record" {
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
		[]string{"Service.create", "Service.begin", "Service.retry", "Service.finish"}, writers)

	for _, n := range []string{"Service.create", "Service.begin", "Service.retry"} {
		require.True(t, all[n].sectioned(), "%s writes a record and must hold jobLock", n)
		require.True(t, all[n].covered(all[n].at["commit"]),
			"%s writes a record inside its section, not after releasing it", n)
	}

	// finish is the shared terminal write, so it is its CALLERS that must hold
	// the lock — it takes none itself, because two of them would deadlock.
	require.False(t, all["Service.finish"].sectioned())
	for name, f := range callers(all, "finish") {
		held, chain := inSection(all, name, "finish")
		require.True(t, held, "%s writes a terminal record outside its section — %s", name, chain)
		require.True(t, f.sectioned(), "%s writes a terminal record and must hold jobLock", name)
	}
}

// THE RUN IS REGISTERED INSIDE THE SECTION THAT CLAIMED IT, and two arguments
// in this package rest their whole weight on that.
//
// runner.go's begin says a cancel serialized AFTER the claim's section must
// find the run registered, or it writes a terminal record while a build it
// could have stopped runs on to its own deadline. jobs.go's withdraw swallows
// queue.ErrClaimed on the strength of it: the first of the three exits by which
// a claimed item always leaves is the holder's own ack, and what makes that
// prompt is the stopRun inside cancel's section reaching a run this node
// registered inside begin's.
//
// IT WAS CHECKED HERE AND ONLY HERE, and that was the hole. The scan asked
// whether the assignment sat inside SOME jobLock span, which is the one thing a
// scan can ask and cannot answer: a registration handed to run() as a closure
// built inside the section and invoked after begin returns is inside the span
// textually and outside the section in every way that matters, and PAIR 1 —
// whose last assertion is written for exactly that harm — lost the race it
// depends on once in two thousand runs.
//
// So the write goes through register(), which takes the read that decided it
// (section.go), and what is left here is the ENUMERATION: who may touch this
// map at all. That is a question about the source and this is where it is
// answered.
func TestTheClaimAndTheRegistrationAreOneSection(t *testing.T) {
	all := pkgFuncs(t)

	writers := map[string][]token.Pos{}
	for n, f := range all {
		if at := f.writesTo["running"]; len(at) > 0 {
			writers[n] = at
		}
	}
	require.ElementsMatch(t, []string{"Service.register", "Service.forget"}, names(writers),
		"register records the run and forget drops it; a third writer of this map is a third "+
			"opinion about what this node is running, and one that carries no reading")

	// register is the checked path, so the write it makes must come after the
	// question it asks. A map write that reached the store would be caught by
	// commit; this one is caught here or nowhere.
	register := all["Service.register"]
	asked, ok := register.at["Live"]
	require.True(t, ok, "register must ask whether the section that decided the claim is still held")
	for _, at := range writers["Service.register"] {
		require.Greater(t, at, asked, "and must ask before it writes, not after")
	}

	// And begin is its one caller, inside the section that claimed the job and
	// after the entry that wrote the record Claimed, so the two are one
	// decision.
	require.ElementsMatch(t, []string{"Service.begin"}, names(callers(all, "register")),
		"the registration belongs to the claim that decided it")

	begin := all["Service.begin"]
	claim, wrote := begin.at["commit"]
	require.True(t, wrote, "begin must write the claim itself, or there is no section to be inside of")
	for _, at := range begin.posns["register"] {
		require.True(t, begin.covered(at),
			"begin must register the run inside its own jobLock section, or a cancel serialized "+
				"after that section finds nothing to stop")
		require.Greater(t, at, claim,
			"and after the entry that writes the record Claimed, so the two are one decision")
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

// EVERY WRITE GOES THROUGH THE READ THAT DECIDED IT. commit is this package's
// only path to Store.Batch; push, ack and withdraw are the only paths to the
// three queue writes that pair with a record write; and register is the only
// path to the map that says what this node is running. Each takes a READING,
// and asks it, before it writes.
//
// THE PARAMETER IS THE POINT. ack used to take a section — the identity of
// whatever holding was running at the instant of the write rather than of the
// read that decided it — and "hand it a different live section" was therefore
// an expression this package could write, and did: a drop decision split from
// its ack across two sections destroyed an item the caller had been told it
// had. A write that takes a reading cannot be handed a section at all.
//
// THIS IS THE STATIC HALF OF section.go. It says a write is impossible to make
// without a name; Live() says whether the thing named is still running at the
// instant of the write. Neither is the other: a scan cannot see when, and a
// runtime check cannot see a site nobody exercised.
//
// attemptSink.write is the one exemption and it is named rather than implicit:
// a log or asset row is content keyed by (job, attempt), written DURING the
// build and outside every section on purpose, and no read of a job record
// decides it.
func TestEveryWriteIsMadeThroughTheReadThatDecidedIt(t *testing.T) {
	all := pkgFuncs(t)
	pkg := parsePackage(t)

	require.ElementsMatch(t, []string{"Service.commit", "attemptSink.write"},
		names(callers(all, "Batch")),
		"an entry written outside commit() is an entry that names no reading — and a reading is "+
			"the only thing that can say the section it was decided in is still the one running")

	// the checked path -> the write it is the only path to. register's is an
	// assignment rather than a call, which is exactly why it had no path at
	// all until now.
	writes := map[string]string{
		"Service.commit":   "Batch",
		"Service.push":     "Push",
		"Service.ack":      "Ack",
		"Service.withdraw": "Remove",
		"Service.register": "",
	}
	for fn, verb := range writes {
		f, known := all[fn]
		require.True(t, known, "%s is gone, and with it a checked path to a write", fn)
		require.True(t, f.calls["Live"],
			"%s writes without asking whether the section that decided the write is still held", fn)
		if verb == "" {
			continue
		}
		require.Less(t, f.at["Live"], f.at[verb],
			"%s asks after it has already written, which answers a question it no longer has", fn)
	}

	// AND EACH TAKES A READING, which is the parameter the whole round is
	// about: a write that takes a section names the holding it is being MADE
	// in, and a write that takes a reading names the read it carries out.
	// TestTheOnlyThingASectionIsPassedToIsARead is the same claim over the
	// whole package rather than over this list.
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if _, checked := writes[qualify(fn)]; !checked {
				continue
			}
			var takes []string
			for _, field := range fn.Type.Params.List {
				takes = append(takes, types.ExprString(field.Type))
			}
			require.Contains(t, takes, "reading",
				"%s must take the read that decided the write, not the section it is made in — "+
					"it takes (%s)", qualify(fn), strings.Join(takes, ", "))
		}
	}
}

// THE ONLY THING A SECTION IS PASSED TO IS A READ.
//
// The token says which holding of jobLock is running; a reading says which read
// a write carries out. Every function that takes the first where it needs the
// second is the ack this round replaced: handed a live section, it wrote — and
// the section it was handed was the writer's own, minted after the read that
// decided the write had been left. Six lines put that back as a second helper
// beside the checked one, and the runtime cannot tell the two apart, because
// from inside a section they are identical.
//
// So it is a question about the source, and this is the answer: record() takes
// a section because a read is what a section is FOR, and nothing else takes
// one.
func TestTheOnlyThingASectionIsPassedToIsARead(t *testing.T) {
	var takers []string
	for _, file := range parsePackage(t).Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			for _, field := range fn.Type.Params.List {
				if types.ExprString(field.Type) == "section.Section" {
					takers = append(takers, qualify(fn))
				}
			}
		}
	}
	require.ElementsMatch(t, []string{"Service.record"}, takers,
		"a function that takes a section and writes is vouching for the write with the holding "+
			"it is being made in, which is the identity of whatever is running rather than of "+
			"the read that decided it")
}

// A READ THAT TAKES NO SECTION DECIDES NO WRITE, and there is one of them.
// unsectioned() is the zero section, and job() — the read shape — is the only
// caller: a transition that reached for it would be reading with a stamp that
// answers nothing, which is a write that cannot be made rather than a write
// that is not checked, but it would be a transition with no section at all.
func TestTheOnlyReadThatTakesNoSectionIsTheOneThatDecidesNothing(t *testing.T) {
	all := pkgFuncs(t)
	require.ElementsMatch(t, []string{"Service.job"}, names(callers(all, "unsectioned")),
		"a read outside every section belongs to the one shape that writes nothing")
}

// THERE IS ONE SECTION LOCK IN THIS PACKAGE AND IT IS jobLock. A second one is
// a second exclusion over the same job table — two writers that serialize
// against nothing — and every scan in this file that matches Enter and Leave by
// VERB rather than by the ident they hang off rests on there being only one.
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
				if !ok || value.Type == nil || types.ExprString(value.Type) != "section.Lock" {
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
}

// A READING IS ONLY EVER MADE BY THE READ THAT TOOK IT. The identity in it is
// the whole check, so a reading assembled anywhere else — at a write site, out
// of whatever section is running — is a write vouching for itself.
//
// THIS SCAN LOOKED FOR COMPOSITE LITERALS, and a field assignment is not one.
// `r.at = at` re-stamped a read taken in a closed section with the section
// running now, and retry split into a reopen and a queueRetry that way ran
// green. There is no field to assign now — reading is section.Reading, whose
// fields are unexported in another package — so what is enumerated here is the
// CALL that makes one: section.Read, and its one caller.
//
// Both halves are cheap and neither is the other: the literal scan catches a
// reading that becomes this package's own type again, and the caller list
// catches a second read handing out a stamp.
func TestAReadingIsOnlyMadeByTheReadThatTookIt(t *testing.T) {
	all := pkgFuncs(t)
	require.ElementsMatch(t, []string{"Service.record"}, names(callers(all, "Read")),
		"section.Read is the only thing that stamps a read with a section, and record() is the "+
			"only read of a job record there is — a second caller is a stamp put on by something "+
			"that read nothing")

	var literals []string
	for _, file := range parsePackage(t).Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				if name := types.ExprString(lit.Type); name == "reading" || strings.HasPrefix(name, "section.Reading") {
					literals = append(literals, qualify(fn))
				}
				return true
			})
		}
	}
	require.Empty(t, literals,
		"a reading built rather than read is an identity nothing took — and one this package "+
			"could assemble again if the type came back into it")
}

// A SECTION IS ENTERED ONCE AND LEFT ON THE WAY OUT.
//
// Two jobLock.Enter() calls in one body are TWO sections, and a body that reads
// in the first and writes in the second is exactly what "the call is inside
// SOME span" reports as covered — the shape that put the defect back with the
// suite green. A plain Leave is the other half of it: it is what lets a body
// release the lock in the middle and keep going.
//
// This does not make the runtime check redundant and is not made redundant by
// it. It fails at review time, on the shape, before anything runs; Live() fails
// at the instant of a write however the body was arranged — including from a
// deferred call or a closure, which are inside the span by every measure a scan
// has.
func TestASectionIsEnteredOnceAndLeftOnTheWayOut(t *testing.T) {
	for name, f := range pkgFuncs(t) {
		require.LessOrEqual(t, len(f.enters), 1,
			"%s enters jobLock %d times: a read taken in one of them and a write made in another "+
				"are two sections, which is the whole defect", name, len(f.enters))
		require.Empty(t, f.plainLeaves,
			"%s leaves jobLock somewhere other than on the way out — a section released in the "+
				"middle of a body is a section whose later writes were decided in it and made after it", name)
		if len(f.enters) == 1 {
			require.Len(t, f.posns["Leave"], 1,
				"%s enters a section and leaves it %d times", name, len(f.posns["Leave"]))
		}
	}
}
