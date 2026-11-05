package jobs

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

// Pins the population of writers rather than any one member: a new
// read-modify-write site over a job would otherwise go unseen by every
// interleaving test. Writes are methods on a handle that exists only inside a
// section's closure (section.go), so these scans ask who may write and which key,
// at review time; whether a write happened while still allowed is the runtime's,
// measured in section_test.go. Key builders are enumerated from events.go and
// store.go and classified by the key they return, and package-level declarations
// are scanned too.

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
	// recv is the receiver's type name: a method on held is reachable only from a body
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
	// idents is every bare identifier named, so "the lock is touched in one place" is
	// about the lock, not a method name like Do.
	idents map[string][]token.Pos
	// returnsHandle is whether it hands a *held back to its caller, which would
	// be a second door onto one.
	returnsHandle bool
}

// packageLevel is the frame package-level declarations are attributed to; every
// enumeration rejects it.
const packageLevel = "(package level)"

// pairedQueueWrites pair with an event; Claim and Sweep do not.
var pairedQueueWrites = map[string]bool{"Push": true, "Remove": true, "Ack": true, "Nack": true}

// rowVerbs are jobs/row's Set and Delete, the only calls that make a row. A
// method value alias still counts its taker as a caller, and a call through it
// fails the key check (TestARespeltRowMakerIsStillCounted).
var rowVerbs = map[string]bool{"Set": true, "Delete": true}

// neverTaken may appear only as the callee: a method value would hide the call
// from every census, and a stashed Apply could run after its section returned.
var neverTaken = map[string]bool{"Set": true, "Delete": true, "Apply": true}

// barrierVerbs are the pkg/queue calls that take a barrier of their own:
// every one of them goes through prepare(), which refuses a follower and then
// blocks until everything committed is applied.
var barrierVerbs = map[string]bool{
	"Push": true, "Remove": true, "Ack": true, "Nack": true, "Claim": true, "Sweep": true,
}

// Key builders are enumerated from events.go and store.go and classified by the
// key they return, so two names producing one key are one builder; the
// enumeration is checked against the invocation table.

// sampleKeys invokes every builder those files declare, with one canonical set of
// inputs so that two builders producing the same key produce the same string.
func sampleKeys() map[string]string {
	const (
		sampleID      = "0123456789012345678901234567890123456789abc"
		sampleProject = "project-of-" + testRepo
		sampleWriter  = "builder"
	)
	at := time.Unix(1756000000, 0).UTC()
	instant := at.UnixNano()

	return map[string]string{
		"seg":          seg(sampleID),
		"stamp":        stamp(1),
		"assetKey":     assetKey("prod", sampleID),
		"eventPrefix":  eventPrefix(sampleID),
		"eventKey":     eventKey(sampleID, instant, sampleWriter),
		"eventName":    eventName(instant, sampleWriter),
		"projectRow":   projectRow(sampleProject, sampleID),
		"projectRows":  projectRows(sampleProject),
		"openRoot":     openRoot(),
		"openRows":     openRows(testRepo),
		"openRowsOf":   openRowsOf(testRepo, sampleID),
		"openRow":      openRow(testRepo, sampleID, instant, sampleWriter),
		"openRowNamed": openRowNamed(testRepo, sampleID, eventName(instant, sampleWriter)),
		"anchorRows":   anchorRows(testRepo, testRef, testEnvironment),
		"anchorRow":    anchorRow(testRepo, testRef, testEnvironment, instant, sampleID, sampleWriter),
		"liveRows":     liveRows(sampleProject, testEnvironment, sampleID),
		"liveRow":      liveRow(sampleProject, testEnvironment, sampleID, instant, sampleID, sampleWriter),
		"besideName":   besideName(instant, sampleID, sampleWriter),
		"terminalRow":  terminalRow(at, sampleID),
		"terminalRows": terminalRows(),
	}
}

// keyFiles are the files that build keys.
var keyFiles = []string{"events.go", "store.go"}

// storeDeclares is every builder the key files declare: no receiver, one string
// result.
func storeDeclares(t *testing.T) []string {
	t.Helper()

	var out []string
	read := 0
	for name, file := range parsePackage(t).Files {
		if !slices.Contains(keyFiles, filepath.Base(name)) {
			continue
		}
		read++
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
	}
	require.Equal(t, len(keyFiles), read, "events.go and store.go are the files that build a key")
	return out
}

// classes maps every builder to the key it produces. It fails when the key files
// and the invocation table above disagree, because a builder nothing calls cannot
// be classified and an unclassified builder is exactly the hole.
func classes(t *testing.T) map[string]string {
	t.Helper()
	samples := sampleKeys()
	require.ElementsMatch(t, storeDeclares(t), names(samples),
		"a key file declares a builder sampleKeys does not invoke, or invokes one it no longer declares — "+
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
		if strings.HasPrefix(key, "/"+rootSegment+"/") && !strings.HasSuffix(key, "/") {
			out[name] = true
		}
	}
	return out
}

// indexRowKeys is every row that is not an event: the open, project and retention
// rows, the ref anchor, and the live row. A union of classes, so a second spelling
// is in it.
func indexRowKeys(all map[string]string) map[string]bool {
	out := map[string]bool{}
	for _, like := range []string{"openRow", "projectRow", "terminalRow", "anchorRow", "liveRow"} {
		for name := range sameKeyAs(all, like) {
			out[name] = true
		}
	}
	return out
}

func TestEveryKeyThisPackageWritesIsInSomeCensus(t *testing.T) {
	keys := classes(t)
	whole := wholeKeys(keys)
	censuses := []map[string]bool{
		sameKeyAs(keys, "eventKey"),
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

// assembles is every function that builds a composite literal of the named type.
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

// qualify names a declaration the way the tables below do: "server.cancel",
// "entry.commit", or a bare "eventKey" for a function with no receiver.
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

// The scan owns its input from three sides: backendFiles is what it expects,
// sourceFiles what is on disk, and the parser what it read. A file a filter
// silently dropped would take it out of every census at once.
var backendFiles = []string{
	"authz.go", "config.go", "dispatch.go", "doc.go", "errors.go", "events.go", "host.go", "jobid.go", "jobs.go", "local.go",
	"leader.go", "meter.go", "output.go", "queue.go", "reconcile.go", "rpc.go",
	"runner.go", "section.go", "service.go", "store.go",
}

// sourceFiles is every non-test .go file here, ignoring build constraints: seeing
// more than the build is the safe direction.
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
	require.Contains(t, parsed, "jobs")

	var read []string
	for name := range parsed["jobs"].Files {
		read = append(read, filepath.Base(name))
	}
	require.ElementsMatch(t, backendFiles, read,
		"the scan read a different set of files than the one it expects: a file added to this "+
			"package belongs in backendFiles, and a file the filter skips is a file every census "+
			"in here is blind to")
	require.ElementsMatch(t, backendFiles, sourceFiles(t),
		"and the files on disk are that same set — a file added to the package and left out of "+
			"backendFiles is one the parser could be told to skip without any list disagreeing")

	return parsed["jobs"]
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
				// Package-level declarations are attributed to one frame, which every enumeration
				// rejects.
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
// skipped wherever the question is "does this WRITE an event", because every
// section reads the job on the way in.
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

// theRead is the one read of a job, skipped by every question about writes: a
// section takes it on the way in, so counting it would make every transition an
// event writer.
var theRead = map[string]bool{"readRecord": true}

func bare(qualified string) string {
	if i := strings.LastIndex(qualified, "."); i >= 0 {
		return qualified[i+1:]
	}
	return qualified
}

// writesRecord reports whether a site's read decides an entry that writes one of
// the job's events, wherever in the call tree that entry is built. It asks for
// the KEY rather than for one spelling of it.
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

// barriers reports whether a call takes a barrier, directly, through this
// package, or through pkg/queue's prepare().
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

// entryWrite reports whether a call ends in an entry or a paired queue write. The
// store is reached by row.Entry.Apply, never Batch.
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

func TestOnlyOneFunctionReadsAJob(t *testing.T) {
	all := pkgFuncs(t)
	keys := classes(t)
	eventKeys := sameKeyAs(keys, "eventKey")

	// The events are listed in one place, and every other read of a job folds them.
	var listers []string
	for n, f := range all {
		if eventKeys[n] || !f.calls["eventPrefix"] {
			continue
		}
		listers = append(listers, n)
	}
	require.ElementsMatch(t, []string{"journal.events", "entry.erase"}, listers,
		"every read of a job must go through journal.events, or the population below is incomplete; "+
			"erase lists the events it deletes")
	require.ElementsMatch(t, []string{"journal.read"}, names(callers(all, "events")))
	require.ElementsMatch(t, []string{"server.readRecord", "journal.newest"}, names(callers(all, "read")),
		"a job is read by the sections' one reader and by the anchor and live readers, which "+
			"decide no write")
	for _, builder := range names(eventKeys) {
		for n, f := range all {
			if n == "entry.event" || n == "entry.erase" || eventKeys[n] {
				continue
			}
			require.False(t, f.calls[builder],
				"%s names an event key, which only the entry that writes the event, and the one "+
					"that erases an expired job, may", n)
		}
	}

	require.ElementsMatch(t,
		[]string{"jobReader.First", "server.job", "server.follow"},
		names(callers(all, "readRecord")),
		"another reader is either a section that did not go through the source enter names, or "+
			"a read outside every section — and a read outside every section decides nothing, "+
			"because there is no handle attached to it: job answers a caller, follow which "+
			"runner to read a log from")

	// The source is the only thing a section reads through; enter names one and does
	// not read.
	require.False(t, all["server.enter"].calls["readRecord"],
		"the entry point names the source; it does not take the read itself, or there would be "+
			"a second answer to what a holding carries")
	require.ElementsMatch(t, []string{"jobReader.Subject", "jobReader.First"},
		methodsOn(t, "jobReader"),
		"the source is two methods: what the section is about, and its read")

	// Both must name the same field: a source whose subject and read diverge would
	// compose keys for one job while deciding on another's (measured: the suite hangs).
	subject := reads(t, "jobReader.Subject", "id")
	require.NotEmpty(t, subject, "the source must say what it is about by naming a field")
	require.Equal(t, subject, reads(t, "jobReader.First", "readRecord"),
		"First must read the field Subject names, or a section is about one job and decides "+
			"on another's events")
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

// fieldsOn is every field declared on a struct type, so a "nothing assigns this
// field" check fails closed after a rename.
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

// site is one section and the writes its read decides, a column per kind of
// write.
type site struct {
	// record: the read decides an entry that writes one of the job's events.
	record bool
	// queue: the read decides a push, a withdrawal or an ack.
	queue bool
	// index: the read decides a write or delete of an index row.
	index bool
	// unbarriered says it entered through withJobUnbarriered, which reads WITHOUT
	// the queue's barrier — so it may decide no event, and must say why what it
	// does decide is safe.
	unbarriered bool
	why         string
}

func TestEveryReadModifyWriteOfAJobRecordIsOneSection(t *testing.T) {
	population := map[string]site{
		// jobs.go
		"server.cancel": {record: true, queue: true, index: true},
		"server.retry":  {record: true, queue: true, index: true},
		// queue.go
		"server.resume":            {queue: true},
		"server.create":            {record: true, queue: true, index: true},
		"server.createEnvironment": {record: true, queue: true, index: true},
		// runner.go
		// A claim, or the ending of a job whose events never arrived, retained.
		"server.begin":  {record: true, queue: true, index: true},
		"server.plan":   {record: true, queue: true, index: true},
		"server.settle": {record: true, queue: true, index: true},
		// Settling's first section, plan's and settle's alike: the ack, and a reopened
		// job's item back.
		"server.taken":           {queue: true},
		"server.drainDeadLetter": {record: true, queue: true, index: true},
		// The erase of an expired job, or the drop of a retention row it outlived.
		"server.expireJob": {record: true, index: true},
		// reconcile.go — the one section that reads without a barrier, since the
		// pass took one for all of them. barrier_test.go measures the number.
		"server.mend": {queue: true, index: true, unbarriered: true,
			why: "a barrier before every row's read is one replicated entry per open job per " +
				"pass, forever. What mend deletes is named by the events its read saw, or is " +
				"older than the cutoff, so a read behind the queue cannot drop a row it did not " +
				"see written; its push is deduplicated by the queue"},
	}

	all := pkgFuncs(t)
	keys := classes(t)
	recordKeys, rows := sameKeyAs(keys, "eventKey"), indexRowKeys(keys)

	found := map[string]funcs{}
	for entry := range sectionEntries {
		for name, f := range callers(all, entry) {
			if name == "server.withJob" || name == "server.withJobUnbarriered" {
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

		require.NotEmpty(t, want.why, "%s: a section that reads without a barrier must say why that is safe", name)
		require.False(t, want.record, "%s: a read without the barrier decides no event", name)
	}

	// The barrier is the source's, taken before the read every body decides on.
	require.True(t, barriers(all, "lead"), "lead() must barrier, or no section has one")
	src := all["jobReader.First"]
	require.Less(t, src.at["lead"], src.at["readRecord"],
		"First must barrier BEFORE the read a body's writes are decided on")
	require.True(t, src.calls["barrier"],
		"and consult the flag, or withJob and withJobUnbarriered are the same door")
}

func TestEveryQueueWriteIsInsideTheSectionThatDecidedIt(t *testing.T) {
	// verb -> the functions of this package that call it -> why it is outside a
	// section, or "" when it is inside one.
	sites := map[string]map[string]string{
		"Push":   {"held.push": ""},
		"Remove": {"held.withdraw": ""},
		"Ack":    {"held.ack": ""},
		"Nack":   {},
		"Claim": {
			"server.dispatch": "the queue's own transition, taken for a free runner under the " +
				"queue's own group lock and decided on no job. The event it pairs with is " +
				"begin's, and THAT is a section: a claim whose job turns out to be finished is " +
				"dropped there and acked there.",
			"server.drainDeadLetter": "the same, one queue over. The dead letter's group lock is " +
				"what makes its claim exclusive; the event it pairs with is inside the " +
				"section below it.",
		},
		"Sweep": {
			"server.step": "moves expired claims on and hands nothing out. It reads no job " +
				"and decides nothing on one.",
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

// inSection reports whether fn's calls to `call` are inside a section: fn is a
// method on the handle and makes the call inside under().
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

// inBody reports whether fn's calls to `call` are inside one of its own sections:
// a method on held is by construction; a function with no section of its own only
// if all its callers are.
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

func TestAnEventAndItsOpenRowMoveInOneEntry(t *testing.T) {
	all := pkgFuncs(t)
	openRowKeys := sameKeyAs(classes(t), "openRow")
	require.True(t, openRowKeys["openRowNamed"], "two spellings of one key are one builder")

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
		[]string{"stated.open", "entry.dropRead", "entry.dropOrphanedOpen"}, touches,
		"an open row is built in three places: the one that writes it beside its event, the one "+
			"that drops the rows of events a read saw, and the one that drops a row with no events")

	require.Equal(t, "stated", all["stated.open"].recv,
		"open must hang off an entry that carries the event, or the row is a claim about a job "+
			"nothing wrote")
	require.ElementsMatch(t, []string{"entry.event"}, assembles(all, "stated"),
		"a second way to MAKE a `stated` is a second way to write an open row without the event "+
			"it belongs to — which is the whole of what this type is for")
	require.Equal(t, "entry", all["entry.event"].recv)

	require.ElementsMatch(t, []string{"stated.dropOpen", "entry.dropClosedOpen", "entry.erase"}, names(callers(all, "dropRead")))
	require.ElementsMatch(t, []string{"server.mend"}, names(callers(all, "dropClosedOpen")))
	require.ElementsMatch(t, []string{"server.mend"}, names(callers(all, "dropOrphanedOpen")),
		"the orphan delete is reconcile's alone: a second caller is a transition dropping the "+
			"open row of a job whose events it has not shown to be absent")
}

// keyBuilder reports whether an expression calls one of the whole-key builders,
// the only expressions that may name a key an entry writes.
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
					"%s: %s(%s) — the key a row names must be built by one of the key files' "+
						"builders, right here. A key that arrives as a parameter is a door "+
						"scoped by the shape of its argument, and anybody can walk through one",
					qualify(fn), calleeName(call.Fun), types.ExprString(call.Args[0]))
				return true
			})
		}
	}
	require.Positive(t, made, "this package makes rows, so a scan that found none read the wrong tree")

	// A row maker is only ever called, never taken: a method value alias (`mk :=
	// s.ops.Set`, `ap := e.ops.Apply`) would read as an ordinary caller and hide the
	// key it names.
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
			require.Fail(t, "one of jobs/row's verbs is taken as a value rather than called",
				"%s at %v — a method value is not a call, so what is done through it is done in a "+
					"function whose name the call does not carry, while the function that TOOK it "+
					"still reads as an ordinary caller of %s", types.ExprString(sel), sel.Sel.NamePos, sel.Sel.Name)
			return true
		})
	}
}

func TestARespeltRowMakerIsStillCounted(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "aliased.go", `package jobs

func (e *entry) aliased(key string) {
	mk := e.ops.Set
	mk(key, nil)
	bk := anchorRows
	e.ops.Set(bk("github.com/evil/repo", "refs/heads/main", "prod"), nil)
}
`, 0)
	require.NoError(t, err)

	fn := file.Decls[0].(*ast.FuncDecl)
	f := newFuncs(qualify(fn), receiver(fn))
	scan(&f, fn.Body)

	require.True(t, f.calls["Set"],
		"a method value taken off the entry must count as naming Set, or an alias is a row "+
			"maker no census of names can see")

	// An aliased key builder is caught from the other side: `bk(…)` is not a call to a
	// builder.
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

func TestEveryEventIsWrittenInsideASection(t *testing.T) {
	all := pkgFuncs(t)
	eventKeys := sameKeyAs(classes(t), "eventKey")

	var writers []string
	for n, f := range all {
		if eventKeys[n] {
			continue
		}
		for builder := range eventKeys {
			if f.calls[builder] && f.calls["Set"] {
				writers = append(writers, n)
				break
			}
		}
	}
	require.ElementsMatch(t, []string{"entry.event"}, writers,
		"a second place that writes an event key is a second place that decides which job an "+
			"entry is about")

	// The event's key is the handle's subject, and the folded record's id is
	// overwritten with it.
	event := all["entry.event"]
	require.Equal(t, "entry", event.recv)
	require.Contains(t, event.writesTo, "ID",
		"event() must stamp the subject onto the record it hands on, or a record can still say "+
			"it is a job the section was not entered on")

	// One method per kind of event, each on the entry, and every caller of one is
	// inside a section.
	kinds := []string{
		"entry.created", "entry.claimed", "entry.settled", "entry.cancelled", "entry.retried", "entry.loggedEvent",
	}
	require.ElementsMatch(t, kinds, names(callers(all, "event")))
	for _, kind := range kinds {
		for name := range callers(all, bare(kind)) {
			held, chain := inBody(all, name, bare(kind))
			require.True(t, held, "%s writes an event outside its section — %s", name, chain)
		}
	}
	require.ElementsMatch(t, []string{"server.create", "server.createEnvironment"}, names(callers(all, "created")))
	require.ElementsMatch(t, []string{"server.begin", "entry.settled"}, names(callers(all, "claimed")),
		"a claim is begin's, and the claim a settle of an unclaimed job opens")
	for name := range callers(all, "ends") {
		held, chain := inBody(all, name, "ends")
		require.True(t, held, "%s ends a job outside its section — %s", name, chain)
	}
}

func TestEveryKeyAnEntryWritesNamesTheSectionsOwnJob(t *testing.T) {
	builders := wholeKeys(classes(t))

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
					"%s writes %s, which is not a key the key files build", owner, types.ExprString(call.Args[0]))

				builder := calleeName(key.Fun)
				rows[owner] = append(rows[owner], builder)

				// EVERY ROW IS KEYED BY ITS JOB: the anchor and the live row, which a
				// different job reads, carry the job they were written beside too.
				var args []string
				for _, arg := range key.Args {
					args = append(args, types.ExprString(arg))
				}
				require.Contains(t, args, e+"."+subject,
					"%s builds %s from %v, and none of those is the subject its writer was "+
						"constructed on — a key a row did not take from its own writer is a row "+
						"about somebody else", owner, builder, args)
				return true
			})
		}
	}

	// Every row this package can write is in this table, so a new Set or Delete caller
	// fails here until classified.
	require.Equal(t, map[string][]string{
		"entry.event":             {"eventKey"},
		"entry.erase":             {"eventKey", "projectRow", "terminalRow", "liveRow", "anchorRow"},
		"entry.dropStaleRetained": {"terminalRow"},
		"entry.dropRetained":      {"terminalRow"},
		"entry.dropRead":          {"openRowNamed"},
		"entry.dropOrphanedOpen":  {"openRowNamed"},
		"stated.open":             {"openRow"},
		"stated.inProject":        {"projectRow"},
		"stated.retained":         {"terminalRow"},
		"stated.anchor":           {"anchorRow"},
		"stated.artifacts":        {"liveRow"},
	}, rows, "one row kind per method, and every method named")

	// Set and Delete are the only things that make a row; row.Entry's slice is
	// unexported.
	all := pkgFuncs(t)
	require.ElementsMatch(t, []string{
		"entry.event", "stated.open", "stated.inProject", "stated.retained", "stated.anchor",
		"stated.artifacts",
	}, names(callers(all, "Set")),
		"a row with a value in it is made by a method in the table above and by nothing else — "+
			"including a method that only takes the method VALUE, which counts here exactly as a "+
			"call does")
	require.ElementsMatch(t, []string{
		"entry.dropRead", "entry.dropRetained", "entry.dropOrphanedOpen", "entry.erase", "entry.dropStaleRetained",
	}, names(callers(all, "Delete")),
		"and a row removed likewise")

	// Rows are only added to: nothing assigns an entry's `ops`.
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

// A KEY WITH A VALUE IS NEVER WRITTEN BY TWO NODES WITH DIFFERENT CONTENT
// (AGENTS.md rule 1): every row that carries a value names the event it was
// written beside — its instant and its writer — and every other row is empty, so
// two writers of one key write the same thing.
func TestNoTwoWritersCanPutDifferentContentAtOneKey(t *testing.T) {
	carriesAValue := map[string]bool{}
	empty := map[string]bool{}
	for _, file := range parsePackage(t).Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || calleeName(call.Fun) != "Set" || len(call.Args) != 2 {
					return true
				}
				key, ok := call.Args[0].(*ast.CallExpr)
				require.True(t, ok, "%s sets a key no builder made", qualify(fn))
				builder := calleeName(key.Fun)
				if id, ok := call.Args[1].(*ast.Ident); ok && id.Name == "nil" {
					empty[builder] = true
					return true
				}
				carriesAValue[builder] = true
				var args []string
				for _, arg := range key.Args {
					args = append(args, types.ExprString(arg))
				}
				require.True(t, slices.ContainsFunc(args, func(a string) bool { return strings.HasSuffix(a, "journal.writer") }),
					"%s puts a value at %s(%v), whose key names no writer: two nodes writing it "+
						"write one key, and one of them is lost", qualify(fn), builder, args)
				return true
			})
		}
	}
	require.Equal(t, map[string]bool{"eventKey": true, "anchorRow": true, "liveRow": true}, carriesAValue)
	require.Equal(t, map[string]bool{"openRow": true, "projectRow": true, "terminalRow": true}, empty)
}

func TestTheClaimAndTheRegistrationAreOneSection(t *testing.T) {
	all := pkgFuncs(t)

	require.ElementsMatch(t,
		[]string{"held.register", "held.stopRun", "server.forget"},
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
	require.ElementsMatch(t, []string{"held.register", "server.forget"}, names(writers))

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
	require.ElementsMatch(t, []string{"server.begin"}, names(callers(all, "register")),
		"the registration belongs to the claim that decided it")

	begin := all["server.begin"]
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
	require.ElementsMatch(t, []string{"server.cancel"}, names(callers(all, "stopRun")),
		"a second caller of the stop is a second opinion about which read decided to end a build")
	held, chain := inBody(all, "server.cancel", "stopRun")
	require.True(t, held, "cancel stops a run outside the section that decided it — %s", chain)
}

func TestTheClaimLoopAndTheRunsItHandsToRunnersAreTheOnlyGoroutines(t *testing.T) {
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
	// A RUN HANDED TO A RUNNER is a goroutine of its own, one per runner holding a
	// job, so a leader can keep N runners busy. Every write it makes is a section
	// under jobLock, the one lock over the whole job table that the rpc goroutines'
	// writes already take, so the transitions still exclude each other.
	require.ElementsMatch(t, []string{"loop", "runOn"}, started,
		"a third goroutine in this package is a writer no census here has named")
	require.ElementsMatch(t, []string{"server.dispatch"}, names(callers(pkgFuncs(t), "runOn")),
		"a run is handed to a runner only where a free one was taken")
}

func TestEveryWriteIsMadeThroughTheHandleThatReadTheRecord(t *testing.T) {
	all := pkgFuncs(t)

	require.ElementsMatch(t, []string{"entry.commit"},
		names(callers(all, "Apply")),
		"an entry applied outside commit() is an entry no section decided — and a handle is the "+
			"only thing that can say the section it was decided in is still running")

	// Nothing here names Batch: row.Entry.Apply takes the store, so no raw op is
	// spellable.
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
	// The handle and the things carrying one: entry is made by held.writes, stated by
	// entry.event.
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

	// Each carrier has one constructor.
	require.ElementsMatch(t, []string{"held.writes"}, assembles(all, "entry"),
		"an entry is made by the handle and nowhere else")
	require.ElementsMatch(t, []string{"entry.event"}, assembles(all, "stated"))

	// jobs/section's two doors (Write, Subject) each have one caller here.
	require.ElementsMatch(t, []string{"held.under"}, onTheHolding(t, "Write"),
		"the gate is the only thing that asks the holding to write")
	require.ElementsMatch(t, []string{"held.id"}, onTheHolding(t, "Subject"),
		"and the subject is read in one place, which is what every key is built from — a "+
			"second reader of it is a second answer to what this section is about")

	// And nothing else is a write method.
	for name, f := range all {
		if !carriesTheHandle[f.recv] {
			continue
		}
		if _, checked := writes[name]; checked {
			continue
		}
		for verb := range map[string]bool{
			"Apply": true, "Batch": true, "Push": true, "Ack": true,
			"Remove": true, "Nack": true, "running": true,
		} {
			require.False(t, f.calls[verb],
				"%s writes %s and is not one of the checked paths above", name, verb)
		}
	}
}

func TestNothingHereWritesToTheAssetStore(t *testing.T) {
	all := pkgFuncs(t)

	// A RUN'S BYTES ARE ITS RUNNER'S TO KEEP (services/runner): this package files
	// the cids it is handed, and stores nothing.
	for _, verb := range []string{"Put", "Asset", "assetStore"} {
		require.Empty(t, names(callers(all, verb)),
			"something in this package names %s: a write to another node that no section decided", verb)
	}

	// The log is sealed from settle and plan only, between their sections: before the
	// ack it would keep a log on a lost claim, inside a section it stalls the node.
	require.ElementsMatch(t, []string{"server.settle", "server.plan", "server.discard"}, names(callers(all, "seal")),
		"a seal elsewhere is a log kept on a delivery nothing has acked")

	settle := all["server.settle"]
	require.Len(t, settle.bodies, 2,
		"settle is the record's section and the section that files the log, its ack's being taken's")
	sealing := settle.posns["seal"]
	require.Len(t, sealing, 1, "the log is kept once, on one path")
	require.False(t, covered(settle.bodies, sealing[0]),
		"settle seals inside one of its own sections, which holds jobLock — every job on this "+
			"node — across a round trip to the runner")
	require.Greater(t, sealing[0], settle.bodies[0].to,
		"settle seals before the section that records, so the log cannot end with what the run "+
			"left the job as")
	require.Less(t, sealing[0], settle.bodies[1].from,
		"settle files the log before it is kept, naming a cid nothing holds")
	require.Less(t, settle.at["taken"], settle.bodies[0].from, "the ack's section comes first")

	// And settle's caller holds no section, or every position above is moot.
	require.ElementsMatch(t, []string{"server.run"}, names(callers(all, "settle")),
		"a second caller of settle is a second place the seal's position is decided")
	require.Empty(t, all["server.run"].bodies,
		"run opens a section of its own, so everything settle does inside it — the seal "+
			"included — is under jobLock however settle is spelled")
}

// takenAsAValue is every place a method named verb appears as a value rather than
// as the callee.
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

// onTheHolding is every function calling verb on a handle's holding
// (<anything>.in.verb(...)), matching the field so another type's Write does not
// count.
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

func TestAHandleIsOnlyMadeByTheOneThatTookTheLock(t *testing.T) {
	all := pkgFuncs(t)

	require.ElementsMatch(t, []string{"server.enter"}, assembles(all, "held"),
		"a handle assembled anywhere but the one place that took the lock is a section nobody entered")

	// The subject is named once, where the lock is taken.
	require.ElementsMatch(t, []string{"server.enter"}, assembles(all, "jobReader"),
		"a source assembled anywhere else is a section about a job the door did not name")

	touches := map[string]bool{}
	for name, f := range all {
		if len(f.idents["jobLock"]) > 0 {
			touches[name] = true
		}
	}
	require.ElementsMatch(t, []string{"server.enter"}, names(touches),
		"the lock is named in one place and that place is the one that hands out a handle — a "+
			"second mention of it is a second door onto a live holding, including one declared "+
			"at the package level, where a scan over function bodies would not have seen it. "+
			"IT IS THE LOCK THAT IS COUNTED AND NOT THE VERB: sync.Once has a Do as well, and "+
			"matching the method name put Close on this list")

	require.ElementsMatch(t, []string{"server.withJob", "server.withJobUnbarriered"},
		names(callers(all, "enter")),
		"the two entry points are the two barriers policies, and nothing else opens a section")

	for name, f := range all {
		require.False(t, f.returnsHandle,
			"%s hands a handle back to its caller, which is a live section outliving the call "+
				"that took it — the one thing withJob's shape exists to make impossible", name)
	}
}

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
