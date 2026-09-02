package forgetest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// THE INVERSE GUARD.
//
// The package comment states the rule one way round: every FAULT must fail the
// suite, which is the only way to know that an assertion asserts. Nothing
// enforced the other way round, and that is the direction the defects kept
// arriving from — an assertion with NO FAULT BEHIND IT, which cannot fail for
// any subject, which reads as a check and is decoration. Three rounds running,
// one was added or a live one was deleted, and every test stayed green: a
// vacuous canary, an assertion replaced by one asserting the opposite, five new
// ones each individually deletable with the tree still at exit 0.
//
// So the suite's assertions are now themselves a population this file walks.
//
//	EVERY CONTRACT ASSERTION MUST BE NAMED BY A PROOF, and a proof is a subject
//	that is wrong in a named way plus the COMPLETE list of complaints the suite
//	makes about it. Naming is by message fragment, and a fragment must match
//	exactly one assertion in the source — so a saying that is a substring of
//	somebody else's message is a build failure and not a test that passes on the
//	wrong assertion.
//
//	THE LIST IS EXACT. What fired under a proof must be what the proof names,
//	no more and no less. That is what makes each named assertion LOAD-BEARING:
//	delete it and the set no longer matches, which is precisely "the self-test
//	notices". A proof naming a complaint some other assertion also makes proves
//	that other assertion instead, and the exactness is what says so.
//
//	AN ASSERTION NO PROOF NAMES FAILS THE BUILD. It is either an assertion no
//	subject can reach, or one whose deletion nothing would notice, and there is
//	no way to tell those apart from the outside — which is the whole point.
//
// THREE KINDS, AND ONLY ONE OF THEM NEEDS A FAULT. Holding a step to the same
// bar as a claim produces faults nobody wants; letting a claim call itself a
// step is the same defect this file exists to close, one level up. So the two
// exemptions are spelled, they are narrow, and each is checked.
//
//	CONTRACT — assert/require/Errorf/Fatalf. A claim about the driver under
//	test. Needs a fault.
//
//	PLUMBING — mustReach/mustAsk/mustHold. A step the suite takes on its way to
//	a question; see plumbing.go. Needs no fault, and MUST NOT HAVE ONE: a step a
//	proof can kill was a claim somebody demoted, and that is a build failure
//	naming the proof. It must also be ON ITS WAY somewhere — a real assertion
//	after it in the same function, or a function that returns the value a
//	caller asks its question with.
//
//	THE ONE EXCUSE FOR A STEP THAT FIRES, and the two things that hold it shut.
//	A proof may list a step it STOPS, because an honest cascade exists: a
//	Subject declaring a capability its driver has not got turns the next call
//	into an *UnsupportedError and the suite really did not get there. Left free,
//	that list was a way to hide a live claim — respell it as a step, move its
//	saying one list over, tree green. So a stop must now be BOTH:
//
//	  a capability refusal — the step's own words must carry
//	  refusedCapability, which only mustReach and mustAsk emit and only for an
//	  *UnsupportedError. A step that fired because a driver answered wrongly or
//	  a probe lied is a complaint about the subject, and a complaint has one
//	  honest spelling.
//
//	  collateral — the step must not be in a function this proof also names a
//	  claim in. A proof's complaint and the place it "could not reach" are not
//	  the same place; when they are, the stop IS the complaint, renamed.
//
//	HELD — an assertion carrying `// held by forge: TestX`. The value is
//	produced by pkg/v2/forge with no driver code between the call and it: a
//	Session's refusal, a field this package stamps, a comparison the core makes
//	on its own. No driver can make one fire, so no fault could ever be written
//	for one — and the fault that CAN break it lives one package down. The
//	marker names the test there that holds it, and TestX must exist. It is not
//	an escape hatch for a claim about a driver, and the same rule as plumbing
//	says so: a held assertion a proof can kill was never held.
//
// The population is read out of the source with go/ast rather than kept in a
// list, because a list is a thing you can forget to add to.

// kind is which of the three an entry is.
type kind int

const (
	contract kind = iota
	plumbing
	held
)

func (k kind) String() string {
	switch k {
	case plumbing:
		return "plumbing"
	case held:
		return "held by forge"
	}
	return "a contract assertion"
}

// assertion is one failure-producing call in this suite's own source.
type assertion struct {
	site string // "run.go:729", the way a stack frame spells it
	call string // "assert.Error"
	says string // the message literal, which is this assertion's name
	in   string // "run.go:capabilities", the function it is written in
	kind kind
	by   string // for held: the test in pkg/v2/forge that holds it
}

// fixedArgs is how many arguments each call takes before its message. testify's
// message is the trailing variadic, so the count is the only way to tell a
// message from an expected value: assert.Contains(t, hay, "needle") has none,
// and assert.Equal(t, want, got, "why") has one. An unknown call name is a
// build failure rather than a guess.
var fixedArgs = map[string]int{
	"assert.Equal": 2, "assert.NotEqual": 2, "assert.ErrorIs": 2, "assert.ErrorAs": 2,
	"assert.Contains": 2, "assert.NotContains": 2, "assert.Len": 2,
	"assert.NoError": 1, "assert.Error": 1, "assert.True": 1, "assert.False": 1,
	"assert.Empty": 1, "assert.NotEmpty": 1, "assert.Nil": 1, "assert.NotNil": 1,
	"assert.Fail": 1,

	"require.Equal": 2, "require.NotEqual": 2, "require.ErrorIs": 2, "require.ErrorAs": 2,
	"require.Contains": 2, "require.NotContains": 2, "require.Len": 2,
	"require.NoError": 1, "require.Error": 1, "require.True": 1, "require.False": 1,
	"require.Empty": 1, "require.NotEmpty": 1, "require.Nil": 1, "require.NotNil": 1,
	"require.Fail": 1,

	// The suite's own two ways of failing, which carry their message first.
	"t.Errorf": 0, "t.Fatalf": 0,

	// And the three ways it says a step is not one. Their question is where a
	// message would be, so they read like everything above.
	"mustReach": 1, "mustAsk": 1, "mustHold": 1,
}

// markers are the functions that MAKE a step a step. Their own bodies fail the
// suite — that is what they are for — but a failure inside a marker is the
// marker firing and not an assertion of its own, so the walk steps over them.
var markers = map[string]bool{"mustReach": true, "mustAsk": true, "mustHold": true}

// heldBy reads `// held by forge: TestX` off the line an assertion is on.
var heldBy = regexp.MustCompile(`//\s*held by forge:\s*(\w+)`)

// population is every assertion in the suite, read out of the source. Test
// files are not part of it: this file, and the proofs beside it, are the thing
// doing the asserting.
func population(t *testing.T) []assertion {
	t.Helper()
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("the suite's own source is not where this test runs from: %v", err)
	}
	var out []assertion
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		marks := marksIn(fset, file)
		enclosing := ""
		ast.Inspect(file, func(n ast.Node) bool {
			if fn, ok := n.(*ast.FuncDecl); ok {
				if markers[fn.Name.Name] {
					return false
				}
				enclosing = filepath.Base(fset.Position(fn.Pos()).Filename) + ":" + fn.Name.Name
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			spelling, at, ok := howItFails(call)
			if !ok {
				return true
			}
			fixed, known := fixedArgs[spelling]
			if !known {
				t.Errorf("%s: %s is a way of failing this file does not know how to read; add it to fixedArgs with the number of arguments it takes before its message",
					fset.Position(call.Pos()), spelling)
				return true
			}
			_ = fixed
			says := ""
			if len(call.Args) > at {
				if lit, ok := call.Args[at].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					says = strings.Trim(lit.Value, "`\"")
				}
			}
			pos := fset.Position(call.Pos())
			a := assertion{
				site: fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line),
				call: spelling,
				says: says,
				in:   enclosing,
			}
			// A mark is read anywhere on the call, because a call this suite
			// writes is routinely three lines long and the marker belongs
			// beside the claim rather than beside the opening parenthesis.
			if markers[spelling] {
				a.kind = plumbing
			} else {
				for line := pos.Line; line <= fset.Position(call.End()).Line; line++ {
					if marks[line] != "" {
						a.kind, a.by = held, marks[line]
						break
					}
				}
			}
			out = append(out, a)
			return true
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].site < out[j].site })
	return out
}

// howItFails names the call and says where its message sits, or reports that
// this call is not one of the suite's ways of failing.
func howItFails(call *ast.CallExpr) (spelling string, at int, ok bool) {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		if !markers[fn.Name] {
			return "", 0, false
		}
		// mustReach(t, err, question, ...), mustAsk(t, err, question, ...) and
		// mustHold(t, ok, question, ...).
		return fn.Name, 2, true
	case *ast.SelectorExpr:
		recv, ok := fn.X.(*ast.Ident)
		if !ok {
			return "", 0, false
		}
		switch recv.Name {
		case "assert", "require":
			spelling = recv.Name + "." + fn.Sel.Name
			return spelling, fixedArgs[spelling] + 1, true
		case "t":
			if fn.Sel.Name != "Errorf" && fn.Sel.Name != "Fatalf" {
				return "", 0, false
			}
			return "t." + fn.Sel.Name, 0, true
		}
	}
	return "", 0, false
}

// marksIn is every `// held by forge: TestX` in a file, by the line it is on.
func marksIn(fset *token.FileSet, file *ast.File) map[int]string {
	out := map[int]string{}
	for _, group := range file.Comments {
		for _, c := range group.List {
			if g := heldBy.FindStringSubmatch(c.Text); g != nil {
				out[fset.Position(c.Pos()).Line] = g[1]
			}
		}
	}
	return out
}

// trace is where testify writes the assertion's own call site. It is the first
// frame, because everything above it is the suite's own stack.
var trace = regexp.MustCompile(`Error Trace:\s+\S*?([^/\s]+\.go:\d+)`)

// attribute turns what the recorder captured into the assertions that produced
// it, and keeps the words each one said — the ledger reads those words to tell
// a step the suite could not take from a claim somebody demoted. A failure it
// cannot place is a failure this file cannot reason about, so it says so rather
// than dropping it.
func attribute(t *testing.T, pop []assertion, msgs []string) map[string][]string {
	t.Helper()
	bySite := make(map[string]assertion, len(pop))
	for _, a := range pop {
		bySite[a.site] = a
	}
	fired := make(map[string][]string)
	for _, msg := range msgs {
		if g := trace.FindStringSubmatch(msg); g != nil {
			if _, ok := bySite[g[1]]; !ok {
				t.Errorf("a failure came from %s, which is not an assertion this file found in the suite's source:\n%s", g[1], msg)
				continue
			}
			fired[g[1]] = append(fired[g[1]], msg)
			continue
		}
		// No trace: the suite's own Errorf and Fatalf, and the two markers,
		// which are placed by their message instead. It has to be exactly one,
		// and messages are unique, so it is.
		var hits []string
		for _, a := range pop {
			if a.says != "" && containsFormat(msg, a.says) {
				hits = append(hits, a.site)
			}
		}
		if len(hits) != 1 {
			t.Errorf("a failure names %d assertions and must name one; it is unattributable, so nothing here can say which assertion is load-bearing:\n%s\n%v", len(hits), msg, hits)
			continue
		}
		fired[hits[0]] = append(fired[hits[0]], msg)
	}
	return fired
}

// containsFormat reports whether a rendered message came from this format
// string: every literal run between the verbs, in order. "%s answered %q" is
// matched by "acme answered \"pay\"" and not by a message that only shares one
// half of it.
func containsFormat(msg, format string) bool {
	for _, chunk := range literalRuns(format) {
		i := strings.Index(msg, chunk)
		if i < 0 {
			return false
		}
		msg = msg[i+len(chunk):]
	}
	return true
}

// literalRuns is a format string's literal halves, with the verbs taken out.
// Runs shorter than four characters are dropped: they carry no identity, and a
// format that is nothing but verbs and punctuation matches everything.
func literalRuns(format string) []string {
	var runs []string
	for _, run := range regexp.MustCompile(`%[-+# 0-9.*]*[a-zA-Z]`).Split(format, -1) {
		if len(strings.TrimSpace(run)) >= 4 {
			runs = append(runs, run)
		}
	}
	if len(runs) == 0 {
		runs = []string{format}
	}
	return runs
}
