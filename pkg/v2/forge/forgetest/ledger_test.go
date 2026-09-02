package forgetest

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/tools/go/packages"
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
// TWO KINDS, AND ONLY ONE OF THEM NEEDS A FAULT. Holding a step to the same
// bar as a claim produces faults nobody wants; letting a claim call itself a
// step is the same defect this file exists to close, one level up. So the one
// exemption is spelled, it is narrow, and it is checked.
//
//	CONTRACT — a call that fails the suite. A claim about the SUBJECT: the
//	driver, and the probes its author wired up. Needs a fault.
//
//	PLUMBING — a marker, which is any function asking for a step. A step the
//	suite takes on its way to a question; see plumbing.go. Needs no fault, and
//	MUST NOT HAVE ONE: a step a proof can kill was a claim somebody demoted,
//	and that is a build failure naming the proof. It must also be ON ITS WAY
//	somewhere — a real assertion after it in the same function, or a function
//	that returns the value a caller asks its question with.
//
//	THE ONE EXCUSE FOR A STEP THAT FIRES, and the two things that hold it shut.
//	A proof may list a step it STOPS, because an honest cascade exists: a
//	Subject declaring a capability its driver has not got turns the next call
//	into an *UnsupportedError and the suite really did not get there. Left free,
//	that list was a way to hide a live claim — respell it as a step, move its
//	saying one list over, tree green. So a stop must now be BOTH a capability
//	refusal, which only a marker emits and only for an *UnsupportedError, and
//	COLLATERAL, in a function this proof does not also name a claim in.
//
// ─────────────────────────────────────────────────────────────────────────────
//
// THE POPULATION IS FOUND BY TYPE, AND IT USED TO BE FOUND BY SPELLING.
//
// This file matched the literal identifiers "assert", "require" and "t" at the
// call site and had a table of call names beside them. Anything spelled
// otherwise was not in the population at all — and the population is what every
// rule below is computed over, so an assertion outside it needs no message, no
// proof and no fault, and nothing anywhere says so.
//
// Measured: a conformance subtest in a new file importing testify as `check`,
// with three assertions no proof could kill, left the tree at exit 0, gofmt
// clean and vet clean, and left this file's own census UNCHANGED at "145
// assertions: 105 contract, 40 plumbing" while `go test -v` showed the subtest
// running. Renaming a tb receiver from `t` to `tt` did the same. The ledger was
// BLIND, not permissive; the exemption had moved from a comment to an import
// alias and got cheaper to type than the comment had been.
//
// So the population is resolved with go/types, and a call is an assertion
// because of WHAT IT IS:
//
//	a call to a function DECLARED OUTSIDE THIS PACKAGE that takes something it
//	can fail through — a parameter whose type has an Errorf. That is testify's
//	TestingT, and it is testify under any alias, any dot-import, any fork and
//	any wrapper written outside this package.
//
//	a call to one of the methods that fail a TB — Error, Errorf, Fatal, Fatalf,
//	Fail, FailNow — on a receiver that has an Errorf, whatever the receiver is
//	named at the call site.
//
//	a call to a MARKER, which is any function asking for a step. Not three
//	names: a type, so the fourth marker is seen the day it is written.
//
// A function declared INSIDE this package is never a leaf: its body is walked,
// so a wrapper does not hide the assertion in it — the assertion is counted
// where it is written, and the wrapper is the place a proof must name.
//
// THE THREE ANCHORS THIS LEAVES are a type name, six method names and "outside
// this package", and none of them is a call-site spelling. `step` is looked up
// by name and its absence is a failure of its own; Errorf cannot be renamed and
// still fail a test; and "outside this package" is not a name at all.

// kind is which of the two an entry is.
type kind int

const (
	contract kind = iota
	plumbing
)

func (k kind) String() string {
	if k == plumbing {
		return "plumbing"
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
}

// failing is the set of methods that fail a testing.TB. A receiver can be
// called anything and a package can be imported under any alias, but a method
// that stops a test is one of these six: the name is the interface's, not the
// call site's, and renaming it does not compile.
var failing = map[string]bool{
	"Error": true, "Errorf": true, "Fatal": true, "Fatalf": true,
	"Fail": true, "FailNow": true,
}

// source is this suite's own package, type-checked. Everything the ledger
// reads about the population comes from here.
type source struct {
	pkg      *packages.Package
	reporter *types.Interface // anything with an Errorf: testify's TestingT, tb, *testing.T
	step     types.Type       // the marker's question type
}

var (
	loadOnce sync.Once
	loaded   *source
	loadErr  error

	overlaid   sync.Mutex
	overlaidBy = map[string]*source{}
)

// suiteSource type-checks the suite's own source. Test files are not part of
// it: this file, and the proofs beside it, are the thing doing the asserting.
//
// The load is hermetic on purpose. GOPROXY=off makes a module that is not
// already in the cache a loud failure rather than a network fetch from inside a
// test, and GOWORK=off keeps a workspace somebody happens to have from
// deciding which source this reads.
func suiteSource(t *testing.T) *source {
	t.Helper()
	loadOnce.Do(func() { loaded, loadErr = loadSuite(nil) })
	if loadErr != nil {
		t.Fatalf("type-checking the suite's own source, which is the population every rule below is computed over: %v", loadErr)
	}
	return loaded
}

// with is this same package with one more file in it, type-checked the same
// way and read by the same rules.
//
// It is how a rule that reads the SOURCE gets a fault behind it. The suite
// proves an assertion by injecting a driver fault and requiring the assertion
// to fire; a rule about the shape of the source is proven the same way, by
// showing the ledger a source that violates it and requiring the ledger to say
// so. A rule whose violating source it stays quiet about is decoration, which
// is the sentence this whole file is built on.
func (s *source) with(t *testing.T, name, content string) *source {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("finding the suite's own directory, to show the ledger a source that breaks its rules: %v", err)
	}
	path := filepath.Join(dir, name)
	key := path + "\x00" + content

	overlaid.Lock()
	defer overlaid.Unlock()
	if got, ok := overlaidBy[key]; ok {
		return got
	}
	got, err := loadSuite(map[string][]byte{path: []byte(content)})
	if err != nil {
		t.Fatalf("type-checking the suite's own source with %s added: %v", name, err)
	}
	overlaidBy[key] = got
	return got
}

func loadSuite(overlay map[string][]byte) (*source, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports,
		Dir:     ".",
		Env:     append(os.Environ(), "GOPROXY=off", "GOWORK=off"),
		Overlay: overlay,
	}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		return nil, err
	}
	if len(pkgs) != 1 {
		return nil, fmt.Errorf("loading the suite's own package answered %d packages", len(pkgs))
	}
	pkg := pkgs[0]
	if len(pkg.Errors) > 0 {
		return nil, fmt.Errorf("%v", pkg.Errors)
	}
	if pkg.TypesInfo == nil || len(pkg.Syntax) == 0 {
		return nil, fmt.Errorf("the suite's own package came back with no type information")
	}
	obj := pkg.Types.Scope().Lookup("step")
	if obj == nil {
		return nil, fmt.Errorf("this package declares no `step` type, which is what marks a marker; see plumbing.go. Without it every step reads as a claim")
	}
	return &source{pkg: pkg, reporter: reporterInterface(), step: obj.Type()}, nil
}

// reporterInterface is `interface{ Errorf(string, ...any) }`, built rather than
// imported because it must match three unrelated declarations: testify's
// TestingT, this package's tb, and testing.TB. Anything a call can fail
// through has it.
func reporterInterface() *types.Interface {
	anyT := types.NewInterfaceType(nil, nil)
	anyT.Complete()
	sig := types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewVar(token.NoPos, nil, "format", types.Typ[types.String]),
			types.NewVar(token.NoPos, nil, "args", types.NewSlice(anyT)),
		), nil, true)
	iface := types.NewInterfaceType([]*types.Func{
		types.NewFunc(token.NoPos, nil, "Errorf", sig),
	}, nil)
	iface.Complete()
	return iface
}

// failure is what one call in the source turns out to be.
type failure struct {
	spelling string // how it reads at the call site, for a message
	at       int    // which argument carries the message, or -1
	kind     kind
}

// howItFails resolves a call to its declared function and says whether that
// function is one of the ways this suite fails, where its message sits, and
// which of the two kinds it is. It answers by TYPE: an alias, a renamed
// receiver, a dot-import and a fork are all the same object underneath.
func (s *source) howItFails(call *ast.CallExpr) (failure, bool) {
	fn, recv := s.callee(call)
	if fn == nil {
		return failure{}, false
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return failure{}, false
	}
	spelling := s.spell(call, fn)

	// A marker: any function that asks for a step. The question is where its
	// message is, which is what a step says it was on its way to.
	if at := s.stepParam(sig); at >= 0 {
		return failure{spelling: spelling, at: at, kind: plumbing}, true
	}

	// A method that fails a TB, on a receiver that can be failed through.
	if sig.Recv() != nil && failing[fn.Name()] && recv != nil && types.Implements(recv, s.reporter) {
		return failure{spelling: spelling, at: messageArg(sig), kind: contract}, true
	}

	// A leaf assertion: declared outside this package and handed something it
	// can fail through. This is testify, under whatever name.
	if fn.Pkg() != nil && fn.Pkg() != s.pkg.Types && takesAReporter(sig, s.reporter) {
		return failure{spelling: spelling, at: messageArg(sig), kind: contract}, true
	}
	return failure{}, false
}

// callee resolves what a call actually calls, and the receiver it is called on.
func (s *source) callee(call *ast.CallExpr) (*types.Func, types.Type) {
	info := s.pkg.TypesInfo
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.SelectorExpr:
		if sel, ok := info.Selections[fun]; ok {
			fn, _ := sel.Obj().(*types.Func)
			return fn, info.TypeOf(fun.X)
		}
		fn, _ := info.Uses[fun.Sel].(*types.Func)
		return fn, nil
	case *ast.Ident:
		fn, _ := info.Uses[fun].(*types.Func)
		return fn, nil
	}
	return nil, nil
}

// spell is how a call reads where it is written, which is what a failure
// message should quote back. It is a label and never an identity.
func (s *source) spell(call *ast.CallExpr, fn *types.Func) string {
	if sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr); ok {
		if x, ok := ast.Unparen(sel.X).(*ast.Ident); ok {
			return x.Name + "." + sel.Sel.Name
		}
		return sel.Sel.Name
	}
	return fn.Name()
}

// stepParam is where a marker's question sits, or -1 for a function that asks
// for no step.
func (s *source) stepParam(sig *types.Signature) int {
	for i := 0; i < sig.Params().Len(); i++ {
		if types.Identical(sig.Params().At(i).Type(), s.step) {
			return i
		}
	}
	return -1
}

// takesAReporter reports whether a function is handed something it can fail
// through — testify's TestingT and everything shaped like it.
func takesAReporter(sig *types.Signature, reporter *types.Interface) bool {
	for i := 0; i < sig.Params().Len(); i++ {
		if types.Implements(sig.Params().At(i).Type(), reporter) {
			return true
		}
	}
	return false
}

// messageArg is which argument carries the message, read off the signature
// rather than out of a table of call names.
//
// The two shapes are told apart by the parameter BEFORE the variadic. Errorf
// and every marker read (format string, args ...any), so the message is that
// string. testify reads (t, expected, actual, msgAndArgs ...any), so the
// message is the head of the variadic itself. A call whose message argument is
// not a string literal ends up saying nothing, and an assertion that says
// nothing is a failure of its own — so a signature this reads wrongly is loud.
func messageArg(sig *types.Signature) int {
	n := sig.Params().Len()
	if !sig.Variadic() || n < 2 {
		return -1
	}
	if basic, ok := sig.Params().At(n - 2).Type().(*types.Basic); ok && basic.Kind() == types.String {
		return n - 2
	}
	return n - 1
}

// isMarkerDecl reports whether a declared function IS a marker. Its body fails
// the suite — that is what it is for — but a failure inside a marker is the
// marker firing and not an assertion of its own, so the walk steps over it.
func (s *source) isMarkerDecl(fn *ast.FuncDecl) bool {
	obj, _ := s.pkg.TypesInfo.Defs[fn.Name].(*types.Func)
	if obj == nil {
		return false
	}
	sig, ok := obj.Type().(*types.Signature)
	return ok && s.stepParam(sig) >= 0
}

// called is what a call calls, whatever that is: a function, a method, or a
// closure a function bound to a name. Objects and not names, so two closures
// spelled the same in two files are two things.
func (s *source) called(call *ast.CallExpr) types.Object {
	info := s.pkg.TypesInfo
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.SelectorExpr:
		if sel, ok := info.Selections[fun]; ok {
			return sel.Obj()
		}
		return info.Uses[fun.Sel]
	case *ast.Ident:
		return info.Uses[fun]
	}
	return nil
}

// asserts reports whether a body makes a claim of its own — a real assertion
// and not a step.
func (s *source) asserts(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if f, ok := s.howItFails(call); ok && f.kind == contract {
			found = true
		}
		return true
	})
	return found
}

// position spells a node the way a stack frame does.
func (s *source) position(p token.Pos) token.Position { return s.pkg.Fset.Position(p) }

func (s *source) site(p token.Pos) string {
	pos := s.position(p)
	return fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line)
}

// population is every assertion in the suite, read out of the type-checked
// source.
func population(t *testing.T) []assertion {
	t.Helper()
	return populationOf(suiteSource(t))
}

func populationOf(src *source) []assertion {
	var out []assertion
	for _, file := range src.pkg.Syntax {
		enclosing := ""
		ast.Inspect(file, func(n ast.Node) bool {
			if fn, ok := n.(*ast.FuncDecl); ok {
				if src.isMarkerDecl(fn) {
					return false
				}
				enclosing = filepath.Base(src.position(fn.Pos()).Filename) + ":" + fn.Name.Name
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			f, ok := src.howItFails(call)
			if !ok {
				return true
			}
			out = append(out, assertion{
				site: src.site(call.Pos()),
				call: f.spelling,
				says: literalArg(call, f.at),
				in:   enclosing,
				kind: f.kind,
			})
			return true
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].site < out[j].site })
	return out
}

// literalArg is the message a call carries, or "" for a call whose message is
// not written down where it is made.
func literalArg(call *ast.CallExpr, at int) string {
	if at < 0 || at >= len(call.Args) {
		return ""
	}
	lit, ok := call.Args[at].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return strings.Trim(lit.Value, "`\"")
	}
	return s
}

// constantArg is the value of an argument the compiler already knows — a
// string literal, and a typed constant like forge.CapList, which is how one
// call site of a shared helper is told from another.
func constantArg(info *types.Info, arg ast.Expr) (string, bool) {
	tv, ok := info.Types[arg]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(tv.Value), true
}

// trace is where testify writes the assertion's own call site. It is the first
// frame, because everything above it is the suite's own stack.
var trace = regexp.MustCompile(`Error Trace:\s+\S*?([^/\s]+\.go:\d+)`)

// attribute turns what the recorder captured into the assertions that produced
// it, and keeps the words each one said — the ledger reads those words to tell
// a step the suite could not take from a claim somebody demoted. A failure it
// cannot place is a failure this file cannot reason about, so it says so rather
// than dropping it.
func attribute(pop []assertion, msgs []string) (map[string][]string, []complaint) {
	bySite := make(map[string]assertion, len(pop))
	for _, a := range pop {
		bySite[a.site] = a
	}
	var said []complaint
	fired := make(map[string][]string)
	for _, msg := range msgs {
		if g := trace.FindStringSubmatch(msg); g != nil {
			if _, ok := bySite[g[1]]; !ok {
				said = append(said, complaint{ruleFailureFromNowhere, g[1],
					"a failure came from here, which is not an assertion this file found in the suite's source:\n" + msg})
				continue
			}
			fired[g[1]] = append(fired[g[1]], msg)
			continue
		}
		// No trace: the suite's own Errorf and Fatalf, and the markers, which
		// are placed by their message instead. It has to be exactly one, and
		// messages are unique, so it is.
		var hits []string
		for _, a := range pop {
			if a.says != "" && containsFormat(msg, a.says) {
				hits = append(hits, a.site)
			}
		}
		if len(hits) != 1 {
			said = append(said, complaint{ruleUnplaceable, fmt.Sprintf("%v", hits),
				fmt.Sprintf("a failure names %d assertions and must name one; it is unattributable, so nothing here can say which assertion is load-bearing:\n%s", len(hits), msg)})
			continue
		}
		fired[hits[0]] = append(fired[hits[0]], msg)
	}
	return fired, said
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
	for _, run := range verb.Split(format, -1) {
		if len(strings.TrimSpace(run)) >= 4 {
			runs = append(runs, run)
		}
	}
	if len(runs) == 0 {
		runs = []string{format}
	}
	return runs
}

var verb = regexp.MustCompile(`%[-+# 0-9.*]*[a-zA-Z]`)
