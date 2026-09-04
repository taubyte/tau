package backend

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
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
	// recv is the receiver's type name: "Service", "held", "entry", or "" for
	// a function with none. IT IS THE WHOLE OF WHAT "INSIDE A SECTION" MEANS
	// NOW for a write: a method on held is reachable only from a body that was
	// handed one.
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

// rowVerbs are the two calls that MAKE A ROW: backend/row's Set and Delete, on
// the entry the row goes into. There is no third, and there is no free-standing
// row to make one out of — row.Entry's slice is unexported and its elements'
// contents are another package's, so a row exists only where one of these two
// is called.
//
// THIS IS A NAME AND IT IS WRITTEN DOWN AS ONE. What every census below reads
// is the identifier at a call site; what it stands for is the act of composing
// a row. An alias is the obvious respelling, and it is caught here rather than
// by luck: a method value `mk := e.ops.Set` is a selector, so the function that
// takes it appears in callers(all, "Set") exactly as a direct call would — and a
// call through `mk` fails the key check above, because `mk(…)` is not a call to
// a store.go builder. TestARespeltRowMakerIsStillCounted drives both.
var rowVerbs = map[string]bool{"Set": true, "Delete": true}

// neverTaken are the three backend/row verbs that may appear ONLY as the thing
// being called: the two that make a row, and the one that hands an entry's rows
// to a store.
//
// A METHOD VALUE IS NOT A CALL, and that is the whole of why this exists.
// `mk := e.ops.Set` leaves the function reading as an ordinary caller of Set,
// and the call through mk has the callee name "mk", so every census keyed on
// the verb walks past the key it names. `ap := e.ops.Apply` is worse: stashed in
// a package-level var and invoked later, it applies the entry's rows OUTSIDE the
// gate that refuses a section which has returned — the one property no scan can
// see and the runtime holds. Both were measured GREEN before this line.
var neverTaken = map[string]bool{"Set": true, "Delete": true, "Apply": true}

// barrierVerbs are the pkg/queue calls that take a barrier of their own:
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
		sampleID      = "0123456789012345678901234567890123456789abc"
		sampleProject = "project-of-" + testRepo
	)
	at := time.Unix(1756000000, 0).UTC()

	return map[string]string{
		"seg":                seg(sampleID),
		"stamp":              stamp(1),
		"jobPrefix":          jobPrefix(sampleID),
		"jobRecordKey":       jobRecordKey(sampleID),
		"projectIndexPrefix": projectIndexPrefix(sampleProject),
		"projectIndexKey":    projectIndexKey(sampleProject, sampleID),
		"assetKey":           assetKey("prod", sampleID),
		"artifactKey":        artifactKey(sampleProject, "prod", sampleID),
		"artifactPrefix":     artifactPrefix(sampleProject),
		"openIndexPrefix":    openIndexPrefix(testRepo),
		"openIndexKey":       openIndexKey(testRepo, sampleID),
		"openIndexRoot":      openIndexRoot(),
		"refLastKey":         refLastKey(testRepo, testRef),
		"terminalIndexKey":   terminalIndexKey(at, sampleID),
	}
}

// storeDeclares is every builder store.go declares: no receiver, exactly one
// string result. unseg, parseOpenIndexKey and idFromIndexKey return two or
// three values and are readers rather than builders; merged returns a map, and
// encode, with, status and terminal have receivers.
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
// boundary. Those are the only expressions a row may be built on.
func wholeKeys(all map[string]string) map[string]bool {
	out := map[string]bool{}
	for name, key := range all {
		if strings.HasPrefix(key, rootSegment+"/") && !strings.HasSuffix(key, "/") {
			out[name] = true
		}
	}
	return out
}

// indexRowKeys is EVERY ROW OF THIS PACKAGE'S THAT IS NOT THE RECORD AND NOT AN
// ATTEMPT ROW: the open index reconcile repairs and MaxOpenPerRepo counts, the
// project index a caller lists by, the retention index, the ref anchor, and the
// artifact row that says what a deployment currently runs. A
// write of one is neither a record write nor a queue write, and without a
// column of its own it was invisible to the table. It is a union of CLASSES, so
// a second spelling of any of them is in it.
//
// THE ANCHOR WAS IN NONE OF THE CENSUSES UNTIL THE NINTH ROUND, and that is
// what made it the unrecoverable half of that round's escape: a transition that
// wrote only a ref anchor answered "no, this read decides no index write" to
// the table below, and the reach walk never saw the key at all. It is not a row
// a listing walks — it is read by key, by the next build of that ref — but it
// is a row a job's transition writes, which is what these censuses are about.
// TestEveryKeyThisPackageWritesIsInSomeCensus is what keeps the next one from
// being missed the same way.
func indexRowKeys(all map[string]string) map[string]bool {
	out := map[string]bool{}
	for _, like := range []string{"openIndexKey", "projectIndexKey", "terminalIndexKey", "refLastKey", "artifactKey"} {
		for name := range sameKeyAs(all, like) {
			out[name] = true
		}
	}
	return out
}

// AND NO KEY THIS PACKAGE WRITES IS IN NO CENSUS.
//
// Every scan in this file asks its question of a CLASS of key — is this the
// record, is it one of the listings — and a builder in neither is a row that
// every one of those scans walks past. refLastKey was exactly that for eight
// rounds: excluded from the record class because it is not a record, and absent
// from the index class because nothing lists it. It is the row that decides
// what the NEXT build of a ref diffs from.
//
// So the classification is closed here rather than assumed: a builder added to
// store.go is invoked by sampleKeys (classes() says so), and then it is in
// exactly one of the two censuses or this fails with its name in the message.
//
// THERE WERE THREE, and the third was the attempt rows — a log or asset row per
// entry, written by the one writer outside every section. There is no such row
// and no such KEY: a log is a file (services/build/livelog) and its cid is a
// field of the record, so the class is gone rather than empty.
//
// THE WRITER OUTSIDE EVERY SECTION IS NOT GONE, THOUGH, and saying it was is
// how it went unwatched for a round. It writes BYTES to the asset store now,
// during the build, and bytes compose no key — so it is invisible to this
// census by construction rather than by accident, and the census that does
// watch it is TestEveryWriteToTheAssetStoreIsNamedAndPlaced.
func TestEveryKeyThisPackageWritesIsInSomeCensus(t *testing.T) {
	keys := classes(t)
	whole := wholeKeys(keys)
	censuses := []map[string]bool{
		sameKeyAs(keys, "jobRecordKey"),
		indexRowKeys(keys),
	}

	for name := range keys {
		in := 0
		for _, census := range censuses {
			if census[name] {
				in++
			}
		}
		if whole[name] {
			require.Equal(t, 1, in,
				"%s builds a key this package writes and exactly one census must count it — a "+
					"builder in none is a row every scan in this file walks past, and one in two "+
					"is a row two scans disagree about", name)
			continue
		}
		require.Zero(t, in,
			"%s builds a prefix or a fragment rather than a whole key, so no census of ROWS may "+
				"count it: a listing is not a write", name)
	}
}

// assembles is every function that BUILDS a composite literal of the named
// type, which is a different question from which functions return one: a chain
// method returns the entry it was handed, and that is not a second way to get
// one. It is the question the round that made the handle a closure already
// asked of held, asked of the two types the writes now hang off.
func assembles(all map[string]funcs, name string) []string {
	var out []string
	for n, f := range all {
		if len(f.literals[name]) > 0 || len(f.literals["&"+name]) > 0 {
			out = append(out, n)
		}
	}
	return out
}

// recvName is what a method calls its receiver — "e", "s", "h", "k". A check
// that a key is built from the handle's own subject has to spell the receiver,
// and spelling it wrong would make the check vacuous.
func recvName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 || len(fn.Recv.List[0].Names) != 1 {
		return ""
	}
	return fn.Recv.List[0].Names[0].Name
}

// qualify names a declaration the way the tables below do: "Service.cancel",
// "entry.commit", or a bare "jobRecordKey" for a function with no receiver.
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

// ── THE SCAN'S OWN INPUT ─────────────────────────────────────────────────
//
// EVERY CHECK IN THIS FILE IS A SCAN OVER THIS PACKAGE'S SOURCE, AND NOTHING
// SAID WHICH SOURCE. parsePackage filtered by suffix and asserted only that a
// package called "backend" came back, so one line inside the filter —
//
//	return !strings.HasSuffix(fi.Name(), "_test.go") && fi.Name() != "errors.go"
//
// took a file out of EVERY census at once. Go lets a method on any type be
// declared in ANY file of its package, so that is not a theoretical hole: a new
// write method on the handle, declared in errors.go rather than in section.go,
// fails by name in the committed tree and is GREEN with that one line added.
// One line, and it reaches further than any single line the doc used to count:
// hiding that same write through the doors below takes two of them hollowed.
//
// SO THE SCAN OWNS ITS INPUT, and it owns it from three sides that disagree in
// different ways: backendFiles is what it EXPECTS, sourceFiles is what is
// ACTUALLY THERE, and the parser answers with what it READ. A file added to
// this package is an edit to the list below; a file the filter drops is a
// failure by name; and a file added and dropped in one go — the two-line
// version of the same attack — is caught by the list not naming it.
var backendFiles = []string{
	"authz.go", "config.go", "doc.go", "errors.go", "jobid.go", "jobs.go",
	"leader.go", "meter.go", "output.go", "queue.go", "reconcile.go", "rpc.go",
	"runner.go", "section.go", "service.go", "store.go",
}

// sourceFiles is the DIRECTORY'S OWN ANSWER: every .go file here that is not a
// test. It applies no build constraints on purpose — the parser reads a file
// the build might exclude, and a scan that sees MORE than the build is the safe
// direction of that difference, where a scan that sees less is the hole above.
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

// parsePackage parses this package's own non-test source, AND CHECKS THAT WHAT
// IT PARSED IS WHAT IS THERE.
func parsePackage(t *testing.T) *ast.Package {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)
	require.Contains(t, parsed, "backend")

	var read []string
	for name := range parsed["backend"].Files {
		read = append(read, filepath.Base(name))
	}
	require.ElementsMatch(t, backendFiles, read,
		"the scan read a different set of files than the one it expects: a file added to this "+
			"package belongs in backendFiles, and a file the filter skips is a file every census "+
			"in here is blind to")
	require.ElementsMatch(t, backendFiles, sourceFiles(t),
		"and the files on disk are that same set — a file added to the package and left out of "+
			"backendFiles is one the parser could be told to skip without any list disagreeing")

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
// package's own functions or pkg/queue's prepare().
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
//
// THE STORE IS REACHED BY Apply AND NOT BY Batch. Round ten moved the batch
// behind a type: an entry's rows are a row.Entry, and row.Entry.Apply takes the
// STORE rather than the ops, so this package names raft's batch nowhere. That
// the name Batch appears in no body here is itself checked, in
// TestEveryWriteIsMadeThroughTheHandleThatReadTheRecord.
func entryWrite(all map[string]funcs, call string) bool {
	if call == "Apply" || pairedQueueWrites[call] {
		return true
	}
	for name := range reach(all, call, theRead) {
		if name == "Apply" || pairedQueueWrites[name] {
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
		[]string{"jobReader.First", "jobReader.Again", "Service.job"},
		names(callers(all, "readRecord")),
		"a fourth reader is either a section that did not go through the source enter names, or "+
			"a read outside every section — and a read outside every section decides nothing, "+
			"because there is no handle attached to it")

	// AND THE SOURCE IS THE ONLY THING A SECTION READS THROUGH. enter names one
	// and does not read; the two reads on it are the section's first and its
	// second, and they differ only in the barrier.
	require.False(t, all["Service.enter"].calls["readRecord"],
		"the entry point names the source; it does not take the read itself, or there would be "+
			"a second answer to what a holding carries")
	require.ElementsMatch(t, []string{"jobReader.Subject", "jobReader.First", "jobReader.Again"},
		methodsOn(t, "jobReader"),
		"the source is three methods: what the section is about, and its two reads")

	// AND ALL THREE MUST NAME THE SAME FIELD. A source is two answers — what
	// this section is about, and what it reads — and nothing in the type makes
	// them agree. Divergent, the handle composes every key from one job while
	// the body decides on another's record, which is the defect this whole
	// mechanism exists to make unspellable, reintroduced at the door. Measured,
	// it does not even fail: the suite HANGS, because a transition deciding on
	// a record that is never the one it wrote loops.
	subject := reads(t, "jobReader.Subject", "id")
	require.NotEmpty(t, subject, "the source must say what it is about by naming a field")
	for _, read := range []string{"jobReader.First", "jobReader.Again"} {
		require.Equal(t, subject, reads(t, read, "readRecord"),
			"%s must read the field Subject names, or a section is about one job and decides "+
				"on another's record", read)
	}
}

// reads is what a method hands to `call`, or returns when call is "" — as a
// source expression, so that "the same field" is a comparison rather than a
// hope. Subject is a bare return, so it is matched by the field it names.
func reads(t *testing.T, method, call string) string {
	t.Helper()

	var out []string
	for _, file := range parsePackage(t).Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || qualify(fn) != method || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.ReturnStmt:
					for _, result := range v.Results {
						if sel, ok := result.(*ast.SelectorExpr); ok && sel.Sel.Name == call {
							out = append(out, types.ExprString(sel))
						}
					}
				case *ast.CallExpr:
					if calleeName(v.Fun) == call && len(v.Args) == 1 {
						out = append(out, types.ExprString(v.Args[0]))
					}
				}
				return true
			})
		}
	}
	require.Len(t, out, 1, "%s must name %q exactly once", method, call)
	return out[0]
}

// fieldsOn is every field name declared on a struct type in this package.
//
// IT EXISTS SO THAT A CHECK ON A FIELD FAILS CLOSED. Two of the enumerations
// below say "nothing assigns this field", which is require.Empty over a map
// keyed by the field's NAME — so a field renamed and reassigned would leave the
// check looking at a key nothing writes and passing on an empty answer. Asking
// the type whether the field is still there is what turns that into a failure.
func fieldsOn(t *testing.T, recv string) []string {
	t.Helper()

	var out []string
	for _, file := range parsePackage(t).Files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name.Name != recv {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, field := range st.Fields.List {
					for _, name := range field.Names {
						out = append(out, name.Name)
					}
				}
			}
		}
	}
	return out
}

// methodsOn is every method declared on a type in this package.
func methodsOn(t *testing.T, recv string) []string {
	t.Helper()

	var out []string
	for _, file := range parsePackage(t).Files {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && receiver(fn) == recv {
				out = append(out, qualify(fn))
			}
		}
	}
	return out
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
		for verb, positions := range writesIn(all, f) {
			for _, at := range positions {
				require.Greater(t, at, reread,
					"%s: %s is decided before the read its barrier covers", name, verb)
			}
		}
	}

	// AND THE BARRIER IS THE SOURCE'S, before the read every body decides on.
	// It was nine lines in nine transitions, then one line in the entry point,
	// and it is now a field of the thing that answers "what is this section
	// about and what does it read" — which is where the difference between the
	// two entry points belongs, because that is the whole of the difference.
	require.True(t, barriers(all, "lead"), "lead() must barrier, or no section has one")
	for _, read := range []string{"jobReader.First", "jobReader.Again"} {
		src := all[read]
		require.Less(t, src.at["lead"], src.at["readRecord"],
			"%s must barrier BEFORE the read a body's writes are decided on", read)
	}

	// THE SECOND READ ALWAYS BARRIERS, whichever entry point was used, and that
	// is why mend's exemption is safe: its one caller reads without one and then
	// decides on a read that has one. The link from reread to Again runs through
	// the holding, which is an interface call in another package and not
	// something any scan over this package can follow — so what is pinned here
	// are its two ends. The other is below: reread is the ONLY thing that asks a
	// holding to read again.
	require.False(t, all["jobReader.Again"].calls["barrier"],
		"Again must not consult the flag: a second read that skipped the barrier when the "+
			"first did is the exemption spreading from one branch to the whole section")
	require.True(t, all["jobReader.First"].calls["barrier"],
		"and First must, or withJob and withJobUnbarriered are the same door")
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
// IT IS A TYPE NOW RATHER THAN A SCAN, and that is what the round that removed
// the arbitrary key bought here. The pairing used to be counted: every mention
// of an open-row builder had to sit inside a []raft.BatchOp literal that also
// mentioned a record builder, which was a fact about two idents being near each
// other in a composite literal. There are no such literals left. open() and
// dropOpen() are methods on `stated` — an entry that CARRIES this job's record
// — and the only thing that produces one is record(), which puts that record
// into the very same entry. An open row in an entry that writes no record is
// not a scan failure any more; it does not compile.
//
// AND THE EXCEPTION IS NAMED RATHER THAN INVISIBLE. mend's own entry deletes an
// open row and writes no record, on purpose: the record it read is finished or
// gone, and the row is the only thing left to remove. So it is the one method
// that touches an open-row key from a bare entry, and its callers are
// enumerated — an exemption scoped by the shape of an argument is an exemption
// anybody can reach for.
func TestARecordAndItsOpenRowMoveInOneEntry(t *testing.T) {
	all := pkgFuncs(t)
	openRowKeys := sameKeyAs(classes(t), "openIndexKey")

	var touches []string
	for name, f := range all {
		if openRowKeys[name] {
			continue
		}
		for builder := range openRowKeys {
			if f.calls[builder] {
				touches = append(touches, name)
				break
			}
		}
	}
	require.ElementsMatch(t,
		[]string{"stated.open", "stated.dropOpen", "entry.dropOrphanedOpen"}, touches,
		"an open-index row is built in three places: the two that move it with the record, and "+
			"the one that drops an orphan")

	for _, name := range []string{"stated.open", "stated.dropOpen"} {
		require.Equal(t, "stated", all[name].recv,
			"%s must hang off an entry that carries the record, or the pairing is back to a scan", name)
	}
	require.ElementsMatch(t, []string{"entry.record"}, assembles(all, "stated"),
		"a second way to MAKE a `stated` is a second way to write an open row without the record "+
			"it belongs to — which is the whole of what this type is for")
	require.Equal(t, "entry", all["entry.record"].recv)

	require.Equal(t, "entry", all["entry.dropOrphanedOpen"].recv)
	require.ElementsMatch(t, []string{"Service.mend"}, names(callers(all, "dropOrphanedOpen")),
		"the orphan delete is reconcile's alone: a second caller is a transition dropping the "+
			"open row of a job whose record it has not shown to be gone")
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

// EVERY KEY A ROW NAMES IS BUILT WHERE THE ROW IS, AND NOTHING TAKES ONE.
//
// A row comes to exist in exactly two ways — row.Entry.Set and row.Entry.Delete
// (backend/row) — and the key each is handed must be a call to one of store.go's
// builders. Not "a builder or a parameter": A PARAMETER IS THE HOLE.
//
// THAT ESCAPE HATCH WAS REAL AND IT WAS USED, and both the door and the writer
// behind it are gone now. The check used to read "a builder here, or a
// parameter every caller builds", and the sink's write() was the one door
// through it: a method taking (key, cid), scoped by the SHAPE of its argument,
// belonging to the one writer in this package outside every section. The census
// one test down then exempted that owner unconditionally from the check that a
// key names its writer's own subject — `if owner == sink { return true }` —
// because a door that takes a key cannot satisfy a census about which keys a
// writer may compose. One line inside it,
//
//	set(refLastKey("github.com/evil/repo", "refs/heads/main"), []byte("0000…"))
//
// left all seven build packages green and moved another repository's ref anchor
// on the happy path of every build. What that writer existed for was a log row
// per line; a log is a FILE now (services/build/livelog) and its cid is a field
// of the record, written by the section that settles the job. So there is no
// parameter branch here, no exemption one test down, and no writer outside a
// section for either of them to be about.
//
// mend's dropOrphanedOpen still takes the REPOSITORY of a row whose record is
// gone — a parameter, and the named exception — but it is not a key: it is one
// segment, handed to a builder that composes the rest from the handle's own
// subject, and TestARecordAndItsOpenRowMoveInOneEntry enumerates its one caller.
func TestEveryKeyAnEntryWritesIsBuiltWhereTheEntryIs(t *testing.T) {
	pkg := parsePackage(t)
	builders := wholeKeys(classes(t))

	made := 0
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 || !rowVerbs[calleeName(call.Fun)] {
					return true
				}
				made++
				require.True(t, keyBuilder(builders, call.Args[0]),
					"%s: %s(%s) — the key a row names must be built by one of store.go's "+
						"builders, right here. A key that arrives as a parameter is a door "+
						"scoped by the shape of its argument, and anybody can walk through one",
					qualify(fn), calleeName(call.Fun), types.ExprString(call.Args[0]))
				return true
			})
		}
	}
	require.Positive(t, made, "this package makes rows, so a scan that found none read the wrong tree")

	// AND A ROW MAKER IS ONLY EVER CALLED, NEVER TAKEN.
	//
	// THIS IS THE ALIAS, AND IT IS THE ONE RESPELLING THAT BEAT EVERY CENSUS
	// ABOVE. `mk := s.ops.Set` inside stated.anchor, and then
	//
	//	mk(refLastKey("github.com/evil/repo", "refs/heads/main"), []byte("0000…"))
	//
	// beside the anchor's own row: the method VALUE is a selector, so the
	// function still reads as a caller of Set and the table of who may make a
	// row is unchanged — and the call through `mk` has the callee name "mk", so
	// every scan keyed on the verb walks straight past the key it names. It was
	// GREEN across all seven build packages, measured.
	//
	// AND THE SAME IS TRUE OF Apply ONE FLOOR UP: `ap := e.ops.Apply`, stashed
	// in a package-level var and called after the section closed, applies the
	// entry's rows outside the gate — and commit is an allowed caller of Apply,
	// so no enumeration notices. That was GREEN too.
	//
	// A method value is not a call, so the fix is not another list: these verbs
	// may appear ONLY as the thing being called. Then an alias is a failure by
	// name where it is taken, and a call through one is a key no builder built.
	for _, file := range pkg.Files {
		called := map[token.Pos]bool{}
		ast.Inspect(file, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					called[sel.Sel.NamePos] = true
				}
				if id, ok := call.Fun.(*ast.Ident); ok {
					called[id.NamePos] = true
				}
			}
			return true
		})
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || !neverTaken[sel.Sel.Name] || called[sel.Sel.NamePos] {
				return true
			}
			require.Fail(t, "one of backend/row's verbs is taken as a value rather than called",
				"%s at %v — a method value is not a call, so what is done through it is done in a "+
					"function whose name the call does not carry, while the function that TOOK it "+
					"still reads as an ordinary caller of %s", types.ExprString(sel), sel.Sel.NamePos, sel.Sel.Name)
			return true
		})
	}
}

// A RESPELLING OF A ROW MAKER IS STILL COUNTED, and this is the check on the
// check: every census over the rows reads an IDENTIFIER, so the honest thing is
// to measure what happens when the identifier is moved.
//
// A method value is a selector wherever it is taken, so the function that takes
// one is in callers(all, verb) exactly as a caller would be; and a call THROUGH
// it hands a key the scan cannot classify, which the key census fails on rather
// than passes. Both halves are asserted against the real scan.
func TestARespeltRowMakerIsStillCounted(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "aliased.go", `package backend

func (e *entry) aliased(key string) {
	mk := e.ops.Set
	mk(key, nil)
	bk := refLastKey
	e.ops.Set(bk("github.com/evil/repo", "refs/heads/main"), nil)
}
`, 0)
	require.NoError(t, err)

	fn := file.Decls[0].(*ast.FuncDecl)
	f := newFuncs(qualify(fn), receiver(fn))
	scan(&f, fn.Body)

	require.True(t, f.calls["Set"],
		"a method value taken off the entry must count as naming Set, or an alias is a row "+
			"maker no census of names can see")

	// And the alias of a key builder is caught from the other side: the census
	// of what a row may be handed asks for a CALL TO A BUILDER, so `bk(…)` is a
	// failure rather than something it walks past.
	builders := wholeKeys(classes(t))
	var handed []ast.Expr
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && rowVerbs[calleeName(call.Fun)] && len(call.Args) > 0 {
			handed = append(handed, call.Args[0])
		}
		return true
	})
	require.Len(t, handed, 1, "the direct call is the one the key census sees")
	require.False(t, keyBuilder(builders, handed[0]),
		"a key built through an aliased builder is not a call to one, so the census fails on it")
}

// The write side, from the other direction: every entry that SETS a job record
// is written inside the section that read it — which is now ONE METHOD, because
// the key is composed rather than spelled.
//
// THE ENUMERATION IS BY KEY, NOT BY IDENT. A second builder returning the same
// key let settle write a job record while this list still read
// [create, begin, retry, finish].
//
// AND IT USED TO BE FOUR NAMES. create, begin and retry each built the record's
// key themselves, inside their own bodies, from an id they had in scope — and
// begin built it from the id in the RECORD rather than from the id its section
// was entered on, which is the same expression only as long as the store agrees
// with itself. There is one writer of a record key in this package now, it is a
// method on an entry, and the id it uses is the section's.
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
	require.ElementsMatch(t, []string{"entry.record"}, writers,
		"a second place that names a job record key is a second place that decides which job an "+
			"entry is about")

	// AND IT NAMES THE SECTION'S JOB. This is the whole of the round: the key
	// is the handle's subject, and the record's own id is overwritten with it,
	// so a body that hands over a record it read somewhere else writes THIS job
	// rather than that one.
	record := all["entry.record"]
	require.Equal(t, "entry", record.recv)
	require.Contains(t, record.writesTo, "ID",
		"record() must stamp the subject onto the record it encodes, or a record can still say "+
			"it is a job the section was not entered on")

	// The four transitions that write one all open a section to do it, and
	// ends() is the shared terminal write, so it is a method on the handle and
	// its callers are the ones that must open the section.
	require.ElementsMatch(t,
		[]string{"Service.create", "Service.begin", "Service.retry", "held.ends", "held.logged"},
		names(callers(all, "record")))
	for name := range callers(all, "record") {
		held, chain := inBody(all, name, "record")
		require.True(t, held, "%s writes a record outside its section — %s", name, chain)
	}
	for name := range callers(all, "ends") {
		held, chain := inBody(all, name, "ends")
		require.True(t, held, "%s writes a terminal record outside its section — %s", name, chain)
	}
}

// EVERY KEY A ROW NAMES IS THE SUBJECT ITS WRITER WAS CONSTRUCTED ON, and this
// is the clause the whole ladder was missing until the eighth round and did not
// hold for every writer until the tenth.
//
// Seven rounds answered WHEN a write was made and WHO made it. None answered
// ABOUT WHAT: commit took []raft.BatchOp and finish took extra ...raft.BatchOp,
// so a body inside a live, unforgeable, correctly-barriered section could put
// one more line in its entry — a row at jobRecordKey(someOtherJob) — and pass
// every static guard and every runtime refusal, because the holding it wrote
// through was genuinely live. Nothing anywhere compared an id inside a key with
// the id the read was taken on.
//
// So no key is spelled at a call site any more. Each ROW is a method, it
// composes its own key from the subject its writer carries, and this is where
// the population of those methods is closed.
//
// AND THERE IS ONE SUBJECT NOW, BECAUSE THERE IS ONE WRITER. There were two:
// an entry's rows are about the job its section was entered on and read it out
// of the handle, h.id(), and the SINK's were about the attempt it was
// constructed for and read it off a field of its own. The sink was exempt from
// this census for two rounds — one line, `if owner == sink { return true }` —
// because its door TOOK A KEY, and a door that takes a key cannot answer a
// census about which keys its owner may compose. The exemption was
// unconditional, so a single line inside the sink's own rows,
//
//	set(refLastKey("github.com/evil/repo", "refs/heads/main"), []byte("0000…"))
//
// moved another repository's ref anchor on the happy path of every build, from
// a writer this file had placed outside every section BY DESIGN. It was brought
// inside the census in the round after that, and it is DELETED now: what it
// existed for was a replicated row per log line, and a log is a file whose cid
// the settling section writes (output.go). Every row this package makes is an
// entry's, and every entry's is the section's.
//
// IT IS SCANNED OVER THE WHOLE PACKAGE, not over the entry's own methods, and
// that was a correction one round needed: scanned over entry and stated alone, a
// row appended from a method on the HANDLE was invisible, and
// `j.writes().put(refLastKey("github.com/evil/repo", …), …)` inside ends() was
// measured GREEN against the whole suite. A row exists only where Set or Delete
// is called, so that is what is counted, wherever it is.
func TestEveryKeyAnEntryWritesNamesTheSectionsOwnJob(t *testing.T) {
	builders := wholeKeys(classes(t))

	// THE ONE ROW A JOB OWNS THAT IS NOT KEYED BY ITS id, and why. Naming it is
	// what keeps it from being a general escape: everything else must show the
	// subject in the key it builds.
	notKeyedByTheJob := map[string]string{
		"refLastKey": "the ref anchor is keyed by (repository, ref), because the thing that reads " +
			"it is the NEXT build of that ref, which is a different job. All three of its parts " +
			"come off the record THIS SECTION READ, so the anchor an entry can move is the one " +
			"belonging to the job the read was taken on.",
	}

	// AND ONE MORE, WHOSE PARTS COME FROM THIS RUN'S OWN OUTPUT. The artifact
	// row says what a deployment now runs, so it is keyed by (project,
	// deployment, resource) and read by a WORKER rather than by any job. Its
	// project comes off the record this section read; its other two are fields
	// of the `produced` value this same entry is writing onto that record, so
	// the rows it makes and the record it writes name the same artifacts by
	// construction. They are a typed pair rather than a parsed string
	// precisely so that can be said here.
	fromThisRunsOutput := map[string]string{
		"artifactKey": "the artifact row is keyed by (project, deployment, resource): the project " +
			"comes off the record THIS SECTION READ, and the other two are fields of the produced " +
			"value this entry is writing onto that record.",
	}

	// HOW EACH WRITER NAMES ITS SUBJECT, spelled against the receiver so that a
	// method that renamed its receiver cannot make the check vacuous.
	subjects := map[string]string{
		"entry":  "h.id()",
		"stated": "h.id()",
	}

	rows := map[string][]string{}
	for _, file := range parsePackage(t).Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			owner, e := qualify(fn), recvName(fn)

			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 || !rowVerbs[calleeName(call.Fun)] {
					return true
				}

				subject, known := subjects[receiver(fn)]
				require.True(t, known,
					"%s makes a row and hangs off %q, which names no subject a key can be "+
						"composed from — every writer of a row is constructed on something it "+
						"cannot assign", owner, receiver(fn))
				require.NotEmpty(t, e, "%s must name its receiver, or the check below is vacuous", owner)

				key, ok := call.Args[0].(*ast.CallExpr)
				require.True(t, ok && keyBuilder(builders, key),
					"%s writes %s, which is not a key store.go builds", owner, types.ExprString(call.Args[0]))

				builder := calleeName(key.Fun)
				rows[owner] = append(rows[owner], builder)

				var args []string
				for _, arg := range key.Args {
					args = append(args, types.ExprString(arg))
				}
				if why, exempt := notKeyedByTheJob[builder]; exempt {
					for _, arg := range args {
						require.True(t, strings.HasPrefix(arg, e+".h.rec()."),
							"%s builds %s from %q, which is neither the section's subject nor a "+
								"field of the record this section READ. %s", owner, builder, arg, why)
					}
					return true
				}
				if why, exempt := fromThisRunsOutput[builder]; exempt {
					for _, arg := range args {
						// THE OUTPUT'S PARTS ARE FIELDS OF A LOOP VARIABLE over
						// this run's assets, never a literal and never a
						// composed string: a key part that was neither the
						// record's nor this run's is exactly the escape this
						// census exists for.
						require.True(t,
							strings.HasPrefix(arg, e+".h.rec().") ||
								strings.HasPrefix(arg, "ref."),
							"%s builds %s from %q, which is neither a field of the record this "+
								"section READ nor a field of what this run produced. %s",
							owner, builder, arg, why)
					}
					return true
				}
				require.Contains(t, args, e+"."+subject,
					"%s builds %s from %v, and none of those is the subject its writer was "+
						"constructed on — a key a row did not take from its own writer is a row "+
						"about somebody else", owner, builder, args)
				return true
			})
		}
	}

	// AND EVERY ROW THIS PACKAGE CAN WRITE IS IN THIS TABLE. A row is made
	// nowhere but a call to Set or Delete, so a method added anywhere — on the
	// entry, on the handle, on the sink, on a Service — fails here until
	// somebody has said which row it is and shown that it names its own subject.
	require.Equal(t, map[string][]string{
		"entry.record":           {"jobRecordKey"},
		"entry.dropRetained":     {"terminalIndexKey"},
		"entry.dropOrphanedOpen": {"openIndexKey"},
		"stated.open":            {"openIndexKey"},
		"stated.dropOpen":        {"openIndexKey"},
		"stated.inProject":       {"projectIndexKey"},
		"stated.retained":        {"terminalIndexKey"},
		"stated.anchor":          {"refLastKey"},
		"stated.artifacts":       {"artifactKey"},
	}, rows, "one row kind per method, and every method named")

	// AND THOSE TWO VERBS ARE THE ONLY THINGS THAT MAKE A ROW AT ALL. row.Entry's
	// slice is unexported in another package and its elements carry another
	// package's structs, so a row exists only where one of them is called, and a
	// batch exists nowhere in this package at all.
	//
	// THIS HOLDS A NAME. What is compared is the identifier at a call site, and
	// the act it stands for is composing a row. It is not defeated by taking the
	// method value — a selector counts wherever it appears, and
	// TestARespeltRowMakerIsStillCounted measures that — but it is a name, and a
	// name is the shape three rounds of this package's history were beaten by.
	all := pkgFuncs(t)
	require.ElementsMatch(t, []string{
		"entry.record", "stated.open", "stated.inProject", "stated.retained", "stated.anchor",
		"stated.artifacts",
	}, names(callers(all, "Set")),
		"a row with a value in it is made by a method in the table above and by nothing else — "+
			"including a method that only takes the method VALUE, which counts here exactly as a "+
			"call does")
	require.ElementsMatch(t, []string{
		"stated.dropOpen", "entry.dropRetained", "entry.dropOrphanedOpen",
	}, names(callers(all, "Delete")),
		"and a row removed likewise")

	// AND THE ROWS ARE NEVER REPLACED, only added to. There is no append here
	// any more — an entry's rows are a value whose slice this package cannot
	// name — so what is left to say is that nothing ASSIGNS the field: an entry
	// handed the rows of another entry writes a set of rows none of the methods
	// above composed, and an assignment is the only way to do it.
	require.Contains(t, fieldsOn(t, "entry"), "ops",
		"an entry's rows are the field the check below names, so a rename would make it vacuous")
	for name, f := range all {
		require.Empty(t, f.writesTo["ops"],
			"%s assigns an entry's rows rather than adding to them, which is a batch composed "+
				"somewhere the table above cannot see", name)
	}
	require.ElementsMatch(t, []string{"held.writes"}, assembles(all, "entry"),
		"and an entry is made in one place, so the rows a commit applies are the ones its own "+
			"methods put there")
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

// EVERY WRITE IS A METHOD ON THE HANDLE OR ON ITS ENTRY, AND EVERY ONE OF THEM
// WRITES INSIDE under().
//
// entry.commit is this package's only path to a replicated batch; push, ack and
// withdraw are the only paths to the three queue writes that pair with a record
// write; and register and stop are the only paths to the map that says what
// this node is running. Each hangs off a handle — an entry carries the handle
// that made it and nothing else can make one — so a helper that wants to write
// takes the handle, which is a parameter list rather than a scan; and each
// makes its write inside under(), which refuses a handle whose section has
// returned.
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
// AND THE ARGUMENT IS GONE FROM THAT DOOR, WHICH IS ROUND TEN. This used to read
// what each caller HANDED Store.Batch and require it be a call to row.Ops —
// which is the outermost callee's NAME, and two spellings of one act:
//
//	store.Batch(append(row.Ops(e.ops), forged), t)   -> "append", failed
//	store.Batch(row.Ops(append(e.ops, forged)), t)   -> "Ops",    PASSED
//
// Moving the append one level in defeated it, and with the forged key spliced as
// a string literal there was no builder call for the key census to classify
// either: eight lines in one file put a row moving another repository's ref
// anchor on every replicated entry this service writes, with all nine of the
// doc's ends intact. So the rows are handed to a store by row.Entry.Apply, which
// takes the STORE and not the rows, and Batch is named NOWHERE in this package —
// which is what is checked, instead of what its argument was spelled.
//
// AND THERE IS NO EXEMPTION LEFT IN THIS ONE. There was one, named rather than
// implicit: a log or asset row was content keyed by (job, attempt), written
// DURING the build by a writer outside every section on purpose, and a whole
// test existed to check the clauses of that sentence. There is no such row —
// a log is a file, and its cid reaches the raft store once, from the section
// that settles the job — so the list below is the whole population of ROW
// writers rather than that population minus one.
//
// THE WRITE THAT WAS EXEMPT MOVED RATHER THAN STOPPED. A build still produces
// content while it runs and still cannot hold jobLock while it does, so the
// bytes go to the ASSET store from outside every section — which this census
// cannot see, because it is a census of rows, and which
// TestEveryWriteToTheAssetStoreIsNamedAndPlaced is. An exemption deleted along
// with its subject is a class closed; an exemption deleted because the subject
// moved to another store is the same hole one room over.
func TestEveryWriteIsMadeThroughTheHandleThatReadTheRecord(t *testing.T) {
	all := pkgFuncs(t)

	require.ElementsMatch(t, []string{"entry.commit"},
		names(callers(all, "Apply")),
		"an entry applied outside commit() is an entry no section decided — and a handle is the "+
			"only thing that can say the section it was decided in is still running")

	// AND NOTHING IN THIS PACKAGE NAMES Batch AT ALL.
	//
	// Store.Batch takes []raft.BatchOp, because that is raft's own signature and
	// this package does not get to change it — so a call to it is a place where
	// a raw op is spellable again, and the previous round's guard read the NAME
	// of whatever expression was in its first argument. There is no such call
	// now: row.Entry.Apply takes the store, so a batch has no expression here,
	// and this asks the question with no argument in it.
	require.Empty(t, names(callers(all, "Batch")),
		"a call to Store.Batch is an entry composed where nothing above can see it — the rows "+
			"reach a store through row.Entry.Apply, which takes the store rather than them")

	// the checked path -> the write it is the only path to. register's and
	// stop's are a map rather than a call, which is exactly why they had no
	// path at all until they were methods here.
	writes := map[string]string{
		"entry.commit":  "Apply",
		"held.push":     "Push",
		"held.ack":      "Ack",
		"held.withdraw": "Remove",
		"held.register": "running",
		"held.stopRun":  "running",
	}
	// THE HANDLE AND THE THINGS THAT CARRY ONE. An entry is made by held.writes
	// and holds the handle that made it; a stated is made by entry.record and
	// holds that entry. Neither is reachable without a live handle, and both
	// are enumerated below.
	carriesTheHandle := map[string]bool{"held": true, "entry": true, "stated": true}

	for fn, verb := range writes {
		f, known := all[fn]
		require.True(t, known, "%s is gone, and with it a checked path to a write", fn)
		require.True(t, carriesTheHandle[f.recv],
			"%s must hang off the handle, or a caller with no section can reach the write", fn)
		require.NotEmpty(t, f.gated, "%s writes without the gate that refuses a returned section", fn)
		for _, at := range f.posns[verb] {
			require.True(t, covered(f.gated, at),
				"%s makes its write outside under(), which answers a question it no longer has", fn)
		}
	}

	// AND THE THINGS THAT CARRY A HANDLE COME FROM ONE PLACE EACH. A second
	// constructor for either is a second way to hold a live section's authority
	// without having been handed it.
	require.ElementsMatch(t, []string{"held.writes"}, assembles(all, "entry"),
		"an entry is made by the handle and nowhere else")
	require.ElementsMatch(t, []string{"entry.record"}, assembles(all, "stated"))

	// AND NOTHING REACHES PAST THE HANDLE INTO THE HOLDING. Write, Reread and
	// Subject are backend/section's three doors and each has exactly one caller
	// here: the gate every write is an argument to, mend's second read, and the
	// accessor every key an entry writes is composed from.
	require.ElementsMatch(t, []string{"held.under"}, onTheHolding(t, "Write"),
		"the gate is the only thing that asks the holding to write")
	require.ElementsMatch(t, []string{"held.reread"}, onTheHolding(t, "Reread"),
		"and the one second read is the only thing that replaces what a holding carries")
	require.ElementsMatch(t, []string{"held.id"}, onTheHolding(t, "Subject"),
		"and the subject is read in one place, which is what every key is built from — a "+
			"second reader of it is a second answer to what this section is about")

	// AND NOTHING ELSE IS A WRITE METHOD. A seventh method on the handle or its
	// entry that reached a store or the map without the gate is the hole this
	// list would not notice, so the list is closed from the other side too.
	for name, f := range all {
		if !carriesTheHandle[f.recv] {
			continue
		}
		if _, checked := writes[name]; checked {
			continue
		}
		// "write" WAS IN THIS SET AND IS NOT ANY MORE. It was the sink's door,
		// added when a method on the handle that built a foreign sink and wrote
		// through it turned out to reach another job's attempt prefix past
		// every verb below. There is no such door and no such prefix: the verb
		// named nothing once the sink was deleted, and a check on a name that
		// stands for nothing is worse than no check, because it reads like one.
		for verb := range map[string]bool{
			"Apply": true, "Batch": true, "Push": true, "Ack": true,
			"Remove": true, "Nack": true, "running": true,
		} {
			require.False(t, f.calls[verb],
				"%s writes %s and is not one of the checked paths above", name, verb)
		}
	}
}

// THE ASSET STORE IS THE OTHER STORE, AND IT IS WRITTEN FROM OUTSIDE EVERY
// SECTION — WHICH IS WHY IT NEEDS A CENSUS OF ITS OWN.
//
// The round that deleted attemptSink said it deleted the CLASS: the one writer
// in this package outside every section, gone, and four ends with it. That was
// true of the class as the sentence defined it — a replicated row — and this
// package has two stores. What replaced the sink opens the second one:
// Assets.Put is durable on another node before it returns (config.go), it is
// the only door in this package that leaves this machine at all, and a build
// calls it WHILE IT RUNS, from the runner's goroutine, with no section
// anywhere.
//
// AND NOTHING SAW IT. Measured on the committed tree of this round, before this
// test existed: a second door on the Service in output.go —
//
//	func (s *Service) watched(jobID string, from int64) {
//		s.assetStore.Put(context.Background(), strings.NewReader(...))
//	}
//
// called from Follow, is one durable object per viewer that no record names,
// made from a path with no section, in the file that replaced the sink. Every
// package under services/build was GREEN, all fifteen censuses in this file
// included. It is the sink's own shape one store over: content nothing has to
// agree about, written by whoever felt like it, and unreferenced — and nothing
// in this service deletes an asset (config.go says so out loud), so an orphan
// made here is permanent.
//
// NEITHER OF THEM MAY BE INSIDE A SECTION, and that is not a preference. A
// section is jobLock, which is ONE LOCK OVER THE WHOLE JOB TABLE: every
// transition of every job on this node takes it. A Put inside one holds a claim
// loop, a webhook's Enqueue, a user's Cancel and a reconcile pass behind a
// round trip to another machine — for the length of a compile in Asset's case,
// and for up to PublishTimeout in publish's.
//
// AND ONE OF THEM WAS, FOR A ROUND. publish had been an ARGUMENT to settle,
// evaluated before the call and therefore before the ack, and the round that
// fixed that moved it inside settle's body — which put the store's round trip
// under jobLock on the happy path of every build, in the same commit whose own
// comments said in three places that it must not be. It is two sections now
// with the publish between them, so the ack is still first and the lock is
// still not held across the network: settle says why the order is that one and
// what it costs.
//
// THE TWO DOORS ARE NOT THE SAME KIND OF WRITE, which is the whole reason both
// are allowed:
//
//	runOutput.Asset    CONTENT, during the run, outside every section. Its cid
//	                   goes into a map in the run's own goroutine and no
//	                   further. Storing it is idempotent and content-addressed,
//	                   so a run that later fails costs the store the same bytes
//	                   twice under one id.
//	runOutput.publish  THE POINTER'S BYTES, between settle's two sections:
//	                   after the ack, because a cid that reaches the store on a
//	                   delivery this node no longer holds is an object no record
//	                   will ever name, and before the record, because the cid it
//	                   returns is a field of the entry that settles the job.
func TestEveryWriteToTheAssetStoreIsNamedAndPlaced(t *testing.T) {
	all := pkgFuncs(t)

	// WHO MAY NAME THE STORE AT ALL. This is the field rather than the verb, so
	// a door that reaches it and calls something else on it — a store that
	// grows a second method, a wrapper — fails here without anybody having
	// thought of that method's name.
	// New is in the population because an Option now configures an options
	// value and NEW assembles the Service from it — so the constructor names
	// every injected dependency exactly once, by construction. That is the
	// point of the shape rather than a leak: there is no other way to put a
	// store on a Service.
	require.ElementsMatch(t, []string{"New", "WithAssets", "runOutput.Asset", "runOutput.publish"},
		names(callers(all, "assetStore")),
		"a fifth function names the asset store: the constructor that assembles it, the option "+
			"that carries it, the run's own content door and the publish inside settle's section "+
			"are the whole population, and anything else is a write to another node that no "+
			"section decided")

	// AND WHO MAY CALL IT. The field check above cannot see a door HANDED the
	// store — `func stash(a Assets, …)` names no field — and this one cannot
	// see a second method on it, so neither is redundant.
	require.ElementsMatch(t, []string{"runOutput.Asset", "runOutput.publish"},
		names(callers(all, "Put")),
		"a third caller of Put stores bytes on another node from a place this file has not "+
			"placed, and a store this package cannot unreference keeps them")

	// THE ONE THAT SHIPS THE LOG IS SETTLE'S, AND IT IS BETWEEN SETTLE'S TWO
	// SECTIONS. Its position has been wrong in both directions, one round each,
	// and this is the check that pins the only place it is right:
	//
	//	BEFORE THE ACK   `settle(d, out.publish(ctx), err)` — an argument, so
	//	                 evaluated before the call — shipped a whole log to the
	//	                 store and then wrote no record naming it whenever the
	//	                 ack it was about to make would have failed: a claim that
	//	                 had expired, a leadership flap. Measured behaviourally
	//	                 by the two runner tests that assert a lost delivery
	//	                 ships nothing.
	//	INSIDE A SECTION the fix for that put it in settle's body, which held
	//	                 jobLock — the whole node's job table — across the round
	//	                 trip to another machine. Measured behaviourally by
	//	                 TestASlowAssetStoreDoesNotStallEveryOtherJobOnTheNode.
	//
	// So this asserts the position, and both of the tests above catch a move.
	require.ElementsMatch(t, []string{"Service.settle"}, names(callers(all, "publish")),
		"a publish outside settle is a log shipped on a delivery nothing has acked, which is the "+
			"orphan the shutdown path already declines to make")

	settle := all["Service.settle"]
	require.Len(t, settle.bodies, 2,
		"settle is the ack's section, the publish, and the record's section — one section means "+
			"one of the two is in the wrong place")
	shipping := settle.posns["publish"]
	require.Len(t, shipping, 1, "the log is handed over once, on one path")
	require.False(t, covered(settle.bodies, shipping[0]),
		"settle publishes inside one of its own sections, which holds jobLock — every job on "+
			"this node — across a round trip to the asset store")
	require.Greater(t, shipping[0], settle.bodies[0].to,
		"settle publishes before the section that acks, so a delivery this node had already lost "+
			"still ships a log no record will name")
	require.Less(t, shipping[0], settle.bodies[1].from,
		"settle publishes after the section that records, so the cid cannot reach the entry that "+
			"settles the job and a log this node could not read back cannot fail it")
	require.True(t, covered([]span{settle.bodies[0]}, settle.at["ack"]),
		"the ack is not in the first of settle's sections, so it is not what the publish is past")

	// AND "OUTSIDE SETTLE'S OWN SECTIONS" IS NOT "OUTSIDE EVERY SECTION" until
	// settle's caller is asked. A settle called from inside somebody else's
	// body holds jobLock through all three of its parts, and every position
	// checked above is still exactly where it is.
	require.ElementsMatch(t, []string{"Service.run"}, names(callers(all, "settle")),
		"a second caller of settle is a second place the publish's position is decided")
	require.Empty(t, all["Service.run"].bodies,
		"run opens a section of its own, so everything settle does inside it — the publish "+
			"included — is under jobLock however settle is spelled")

	// AND THE ONE OUTSIDE EVERY SECTION IS NOT THIS PACKAGE'S TO CALL. Asset is
	// a door for the RUNNER — a stranger's build, handing over bytes it made —
	// and a caller here would be this package storing something of its own from
	// outside a section, which is the sink again with the store swapped.
	require.Empty(t, names(callers(all, "Asset")),
		"something in this package puts an asset: the door outside every section belongs to the "+
			"runner, and what it may leave behind is content and not a decision")

	// AND THE VERB IS ONLY EVER CALLED, NEVER TAKEN. This is the one respelling
	// that walked past every line above, and it is `mk := e.ops.Set` one store
	// over: `door := o.s.assetStore.Put`, taken inside a caller this file
	// ALLOWS and stashed in a package-level var, leaves the taker reading as an
	// ordinary caller and gives the call through it the name of a variable
	// nobody enumerated. Measured GREEN against the four lines above before
	// this one was written.
	require.Empty(t, takenAsAValue(t, "Put"),
		"the asset store's verb is taken as a value rather than called, so what is done through "+
			"it is done in a function whose name the call does not carry")

	// AND WHAT THE OUTSIDE DOOR LEAVES BEHIND REACHES A RECORD BY ONE PATH.
	// Asset writes the cid into the run's own map and publish reads it, inside
	// the section, into the value settle records. A third toucher is a cid
	// spent somewhere no section is.
	require.Contains(t, fieldsOn(t, "runOutput"), "built",
		"the run's cids are the field the two checks below name, so a rename would make them vacuous")
	written := map[string][]token.Pos{}
	for n, f := range all {
		if at := f.writesTo["built"]; len(at) > 0 {
			written[n] = at
		}
	}
	require.ElementsMatch(t, []string{"runOutput.Asset"}, names(written),
		"a second writer of the run's cids is a second opinion about what this run produced")
	require.ElementsMatch(t, []string{"runOutput.Asset", "runOutput.publish"}, names(callers(all, "built")),
		"and a third toucher of them is a cid read somewhere the section that records it cannot see")
}

// takenAsAValue is every place a method named verb appears as a VALUE rather
// than as the thing being called.
//
// A METHOD VALUE IS NOT A CALL. The function that takes one still reads as an
// ordinary caller of the verb, and the call THROUGH it carries the name of
// whatever the value was assigned to — so an enumeration of who may call
// something walks past the place it is actually called. It is how a row maker
// escaped three censuses at once (rowVerbs, above), and it is why the door onto
// the asset store is asked the same question.
func takenAsAValue(t *testing.T, verb string) []string {
	t.Helper()

	var out []string
	for _, file := range parsePackage(t).Files {
		called := map[token.Pos]bool{}
		ast.Inspect(file, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					called[sel.Sel.NamePos] = true
				}
			}
			return true
		})
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != verb || called[sel.Sel.NamePos] {
				return true
			}
			out = append(out, types.ExprString(sel))
			return true
		})
	}
	return out
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

	require.ElementsMatch(t, []string{"Service.enter"}, assembles(all, "held"),
		"a handle assembled anywhere but the one place that took the lock is a section nobody entered")

	// AND THE SUBJECT IS NAMED IN THE SAME PLACE, once. jobReader is what
	// answers "which job is this section about" and "what does it read", and it
	// is built where the lock is taken — so a section is entered on a job by
	// the same expression that takes the exclusion, and there is no second one.
	require.ElementsMatch(t, []string{"Service.enter"}, assembles(all, "jobReader"),
		"a source assembled anywhere else is a section about a job the door did not name")

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
