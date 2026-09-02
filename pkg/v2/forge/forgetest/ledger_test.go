package forgetest

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/tools/go/packages"

	"github.com/taubyte/tau/pkg/v2/forge/fake"
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
//	EVERY CONTRACT CASE MUST BE NAMED BY A PROOF, and a proof is a subject that
//	is wrong in a named way plus the COMPLETE list of complaints the suite makes
//	about it. Naming is by message fragment, and a fragment must match exactly
//	one case in the source — so a saying that is a substring of somebody else's
//	message is a build failure and not a test that passes on the wrong
//	assertion.
//
//	AND EVERY FAULT MUST BE NAMED BY A PROOF, which is the same closure read
//	backwards and the half that was missing. Demoting an assertion to a step and
//	deleting the proof that named it left a contract dead, a fault stranded and
//	the tree at exit 0 — measured. An assertion needs a fault behind it; a fault
//	needs a proof in front of it; neither direction alone closes the loop.
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
//
// ─────────────────────────────────────────────────────────────────────────────
//
// AND THE WALK WENT ROUND FOUR OF THEM, BECAUSE IT WAS A LIST OF PLACES A BODY
// HAD BEEN SEEN RATHER THAN THE STATEMENT THAT A BODY IS A BODY.
//
// The resolution was by type and the WALK was still by shape. Four measured, all
// four live in the shared suite, all four with this file's own census unchanged
// at 144 contract cases and the tree at exit 0, gofmt clean, vet clean:
//
//	a package-level `var f = func(t tb, ...) { assert... }`. The walk matched
//	*ast.FuncDecl and *ast.AssignStmt, and a *ast.ValueSpec is neither, so the
//	body was never entered at all.
//
//	an assertion written inside a MARKER, whose body the walk stepped over
//	entirely — and a marker is "any function that asks for a step", so adding a
//	step parameter exempted everything written beside it.
//
//	a call into another package handed the METHOD that fails a test rather than
//	the reporter: outside.Check(t.Errorf, cond, msg). The callee's signature has
//	no Errorf in it, so nothing about the declaration said it could fail
//	anything. The same helper taking a reporter was caught.
//
//	`var truthy = assert.True`, then truthy(t, cond, msg). The name resolved to
//	a *types.Var and the walk stopped there. The exemption had moved from an
//	import alias to a one-line var and got cheaper to type than the alias was.
//
// THE CLASS IS "A CALL THAT CAN FAIL THE TEST, HOWEVER IT WAS NAMED OR BOUND",
// and it is stated in three parts rather than patched in four places:
//
//	A BODY IS A BODY WHEREVER IT IS. Go holds one in exactly two places — a
//	declaration and a literal. Every declaration is walked, markers included; a
//	literal is walked by the function that contains it, and a literal nothing
//	contains is walked in its own right, whatever it was bound to or bound into.
//	That is not a list of binding forms: it is the lexical statement that
//	nothing is left over, and the ledger reports any body it does not reach.
//
//	A MARKER IS A FUNCTION LIKE ANY OTHER. Its OWN failure — a failure made
//	through the reporter it was handed, reporting the QUESTION it was handed —
//	is the marker firing, and that one call is the step. Everything else written
//	in a marker is a claim and is in the population like any other claim.
//
//	A VALUE DOES NOT STOP BEING TESTIFY BY BEING GIVEN A SHORTER NAME, and a
//	failure handed over without its reporter is still a failure. A name is
//	followed to what it was bound to; a method value of one of the six is a way
//	to fail the test wherever it is passed or called; and where the binding
//	cannot be seen at all, Errorf's own shape answers.
//
// WHAT THIS DOES NOT COVER, said plainly.
//
// A failure produced by something that is neither a testify-shaped call, a TB
// failure method nor a marker — a panic, an os.Exit, a t.Skip that hides a
// question — is not in the population and no rule here is computed over it.
//
// A value that reaches a call through a struct field, a map, a slice or a
// channel used to be followed only as far as its TYPE: Errorf's shape was read
// as a failure and the other five methods' shapes — func(...any) and func() —
// were not, because those are the shape of every callback in the language and
// reading them as failures would make every deferred close a claim. Measured,
// that was reachable: a struct holding assert.NotEmpty and a tb's FailNow,
// called in a walked body, carried two claims with the census unchanged and the
// tree at exit 0. It is closed at the other end now — a way to fail may be
// handed over and may be given a name, but it may not be PUT DOWN in a field,
// an element, a channel or a caller's hands, where it is still a selector and
// answers for what it is. See waysToFailPutDown. A PARAMETER was never in this
// hole: a parameter is followed to what its call sites hand it.
//
// AND THE MARKER EXEMPTION IS NARROWER THAN IT WAS, NOT GONE. A claim written
// as a marker's OWN firing — through the reporter the marker was handed,
// reporting the question it was handed — is still read as the marker firing and
// is still not in the population. It is silent only while NO fault reaches it:
// the moment one does, the failure attributes to the step at the marker's call
// site, which no proof names, and the fired-unnamed rule fails the build naming
// the proof — measured. What is left is a claim no fault in this suite can
// reach, which is the orphan condition, in the one place the orphan rule cannot
// see it.
//
// MEASURED AS AN A/B, so the size of it is on the record. One claim, two
// spellings, in mustReach. As an assertion —
//
//	assert.NotContains(t, err.Error(), "ledger-fault-canary", "...")
//
// — the orphan rule names it at plumbing.go:103 and the tree is GO_TEST_EXIT=1.
// As the marker's own firing, `if strings.Contains(err.Error(), canary) { the
// same Fatalf the marker already makes }`, the tree is GO_TEST_EXIT=0.
//
// AND IT IS NOT CLOSABLE BY SHAPE, which is worth stating rather than leaving
// as an open item somebody will try to close. Every candidate rule was
// considered against that measurement and each fails:
//
//	"a marker's firing may be guarded only by its own parameters" — the
//	evasion above reads err, which is a parameter. So does every honest
//	firing. The two are the same shape.
//
//	"a marker fires in exactly one place" — costs mustReach a refactor and
//	buys one token: the smuggled condition moves into the guard of the single
//	firing, `if err != nil || <the claim>`.
//
//	"a marker's firing must be reachable by a fault" — closes it completely,
//	and is the exact reverse of the argument plumbing.go is built on, which is
//	that demanding a fault behind "the harness started" produces faults nobody
//	wants. That is a decision about what this suite's vocabulary IS, and it is
//	the owner's, not this file's.
//
// So the bound is stated instead: the exemption is worth one condition inside
// three functions of ten lines each, every one of which is read as plumbing by
// anyone who opens the file it lives in.
//
// Nothing in a _test.go file beside this one is in the population either: those
// are the thing doing the asserting. And the loader is the go command, so a
// source this package cannot type-check is a loud failure of the ledger rather
// than a quietly smaller population.

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

// assertion is one CASE: a failure-producing call in this suite's own source,
// reached through one particular chain of call sites.
//
// A case and a site are not the same thing, and the ledger used to count the
// site. reject in optional.go is one closure with two assertion sites shared by
// nine table rows; assertRepository is one function with five, reached from six
// places. A proof needed ONE row to fire and the site was proven, so every one
// of the nine rows and half the six call sites could be deleted with the tree
// at exit 0 — measured. A table-driven assertion is one site and many
// contracts.
//
// So the unit is the case, the words of a case are its format with the constant
// arguments of its own call chain substituted in, and a proof names a case by
// those words. Deleting a row deletes the case, the words its proof names stop
// existing, and the proof fails saying so.
//
// A helper reached from several places therefore has to carry which place it
// was reached from into what it says — the row's name, the fixture's name, the
// capability. Where it does not, two cases say the same thing, and that is the
// collision rule, which fails the build naming both.
type assertion struct {
	id    string // the case: this site, and the chain that reaches it
	site  string // "run.go:729", the way a stack frame spells it
	where string // the call site this case comes through, for a human
	call  string // "assert.Error"
	says  string // what it says HERE, with this chain's constants filled in
	in    string // "capabilities", the function it is written in
	kind  kind
}

// callable is a function, a method, or a closure a function bound to a name:
// anything a case can pass through on its way to an assertion.
type callable struct {
	obj    types.Object
	name   string
	file   string
	body   *ast.BlockStmt
	params []*types.Var
	lit    *ast.FuncLit // set for a named closure, so its parent skips its body
}

// edge is one call of one callable.
type edge struct {
	from *callable
	to   *callable
	call *ast.CallExpr
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
	fails    *types.Signature // Errorf's own shape, for a failure whose binding is invisible
	step     types.Type       // the marker's question type

	// The walk, the bindings and the marker firings are computed once per
	// source and read by everything below. They are cached here and not
	// recomputed per question because every rule is read over the same source,
	// and because a walk that answers differently at two call sites is a walk
	// with two definitions of what a body is.
	walkOnce sync.Once
	theWalk  *walk

	boundOnce sync.Once
	bound     map[types.Object][]ast.Expr

	firingOnce sync.Once
	firing     map[*ast.CallExpr]bool

	// faults is every fault the fake forge can be told to have, by the value
	// it carries and the constant it is declared as. It is read out of that
	// package's source rather than kept in a list here, because a list is a
	// thing you can forget to add to — which is the whole reason the
	// population is read out of source too.
	faults map[string]string

	// rules is every rule name this file DECLARES, read the same way and for
	// the same reason. A rule constant that no registry entry carries is a rule
	// nothing reads, and the registry is a list somebody can shorten.
	rules map[string]string

	// harnesses is every name the three things that RUN the rules are declared
	// under, read the same way again. The rules were closed and the loops that
	// execute them were not, and a loop nothing runs is every rule turned off.
	harnesses map[string]string
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
		Dir: ".",
		Env: append(os.Environ(), "GOPROXY=off", "GOWORK=off"),
		// The test files are type-checked as well, not because they are part of
		// the population — they are the thing doing the asserting — but because
		// the ledger's own rule names are declared in them and are read out of
		// source rather than trusted to a list.
		Tests:   true,
		Overlay: overlay,
	}
	// The fake forge is loaded beside the suite so its faults can be counted.
	// Its import path comes from the type itself and not from a string here.
	faultType := reflect.TypeOf(fake.Fault(""))
	pkgs, err := packages.Load(cfg, ".", faultType.PkgPath())
	if err != nil {
		return nil, err
	}
	var pkg, faked, tested *packages.Package
	for _, p := range pkgs {
		switch {
		case p.PkgPath == faultType.PkgPath() && p.ID == p.PkgPath:
			faked = p
		case p.PkgPath != suitePath:
			continue
		case p.ID == p.PkgPath:
			pkg = p // the suite itself, without its test files
		case strings.HasSuffix(p.ID, ".test]"):
			tested = p // the same package WITH them, where the rules are declared
		}
	}
	if pkg == nil || faked == nil || tested == nil {
		return nil, fmt.Errorf("loading the suite, its tests and the fake forge answered %d packages and not the three needed", len(pkgs))
	}
	for _, p := range []*packages.Package{pkg, faked, tested} {
		if len(p.Errors) > 0 {
			return nil, fmt.Errorf("%v", p.Errors)
		}
	}
	if pkg.TypesInfo == nil || len(pkg.Syntax) == 0 {
		return nil, fmt.Errorf("the suite's own package came back with no type information")
	}
	obj := pkg.Types.Scope().Lookup("step")
	if obj == nil {
		return nil, fmt.Errorf("this package declares no `step` type, which is what marks a marker; see plumbing.go. Without it every step reads as a claim")
	}
	faults, err := constantsOf(faked, faultType.Name())
	if err != nil {
		return nil, err
	}
	rules, err := constantsOf(tested, reflect.TypeOf(ruleName("")).Name())
	if err != nil {
		return nil, err
	}
	harnesses, err := constantsOf(tested, reflect.TypeOf(harnessName("")).Name())
	if err != nil {
		return nil, err
	}
	report := reporterInterface()
	return &source{pkg: pkg, reporter: report, fails: report.Method(0).Type().(*types.Signature),
		step: obj.Type(), faults: faults, rules: rules, harnesses: harnesses}, nil
}

// suitePath is this package, taken from a type in it rather than written out.
var suitePath = reflect.TypeOf(assertion{}).PkgPath()

// constantsOf is every constant of one named string type in a package, by value
// and by the name it is declared under. It reads the fake forge's faults and
// this file's own rule names, and for the same reason in both cases: a
// population kept in a list beside the code is a population somebody forgets to
// add to, or quietly shortens.
func constantsOf(pkg *packages.Package, typeName string) (map[string]string, error) {
	scope := pkg.Types.Scope()
	obj := scope.Lookup(typeName)
	if obj == nil {
		return nil, fmt.Errorf("%s declares no %s type, which is what a fault is", pkg.PkgPath, typeName)
	}
	out := map[string]string{}
	for _, name := range scope.Names() {
		c, ok := scope.Lookup(name).(*types.Const)
		if !ok || !types.Identical(c.Type(), obj.Type()) || c.Val().Kind() != constant.String {
			continue
		}
		out[constant.StringVal(c.Val())] = name
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s declares no %s at all", pkg.PkgPath, typeName)
	}
	return out, nil
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

// howItFails resolves a call and says whether it is one of the ways this suite
// fails, where its message sits, and which of the two kinds it is.
//
// It answers by what a call IS and not by how it reads: an alias, a renamed
// receiver, a dot-import, a fork, a shorter name given to testify by a var, and
// a failure handed over without the reporter it belongs to are all the same
// thing underneath, and each of them was once an exemption nobody had to write
// down.
func (s *source) howItFails(call *ast.CallExpr) (failure, bool) {
	// A marker's OWN failure — through the reporter it was handed, reporting
	// the question it was handed — is the marker firing, and the marker is
	// already one entry at each of its call sites. It is the one thing excused
	// from the population, and it is excused by what it says rather than by
	// which function it is written in: a claim written beside it is a claim.
	if s.markerFirings()[call] {
		return failure{}, false
	}
	fn, recv := s.callee(call)
	sig, ok := s.signatureOf(call, fn)
	if !ok {
		return failure{}, false
	}
	spelling := s.spell(call)

	// A marker: any function that asks for a step. The question is where its
	// message is, which is what a step says it was on its way to.
	if at := s.stepParam(sig); at >= 0 {
		return failure{spelling: spelling, at: at, kind: plumbing}, true
	}

	// A method that fails a TB, on a receiver that can be failed through.
	if fn != nil && recv != nil && failing[fn.Name()] && types.Implements(recv, s.reporter) {
		return failure{spelling: spelling, at: messageArg(sig), kind: contract}, true
	}

	// Or the thing being called IS a way to fail the test: the same six
	// methods reached through a name, a field or a value rather than through
	// the receiver they belong to.
	if s.isAWayToFail(call.Fun, 0) {
		return failure{spelling: spelling, at: messageArg(sig), kind: contract}, true
	}

	// A leaf assertion: a body this package does not hold, handed something it
	// can fail through. Handed a REPORTER, which is testify under whatever
	// name; or handed the METHOD that fails one without the reporter it belongs
	// to, which is a call whose signature says nothing about failing anything
	// and whose call site says all of it.
	if fn != nil && fn.Pkg() != nil && fn.Pkg() != s.pkg.Types &&
		(takesAReporter(sig, s.reporter) || s.handedAWayToFail(call)) {
		return failure{spelling: spelling, at: messageArg(sig), kind: contract}, true
	}
	return failure{}, false
}

// callee resolves what a call actually calls, and the receiver it is called on.
// A name bound to a function is followed to that function: a value does not
// stop being testify by being given a shorter name, and `var truthy =
// assert.True` was measured moving the exemption from an import alias to a
// one-line var.
func (s *source) callee(call *ast.CallExpr) (*types.Func, types.Type) {
	info := s.pkg.TypesInfo
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.SelectorExpr:
		if sel, ok := info.Selections[fun]; ok {
			if fn, ok := sel.Obj().(*types.Func); ok {
				return fn, info.TypeOf(fun.X)
			}
			return s.followsTo(sel.Obj(), 0)
		}
		if fn, ok := info.Uses[fun.Sel].(*types.Func); ok {
			return fn, nil
		}
		return s.followsTo(info.Uses[fun.Sel], 0)
	case *ast.Ident:
		if fn, ok := info.Uses[fun].(*types.Func); ok {
			return fn, nil
		}
		return s.followsTo(info.Uses[fun], 0)
	}
	return nil, nil
}

// followsTo is the function a NAME ultimately calls, read out of what this
// package binds that name to.
//
// A name bound to a function LITERAL resolves to nothing here, on purpose: that
// literal is a body in this package and is walked in its own right, so the
// assertion in it is counted where it is written and counting the call as well
// would count it twice.
func (s *source) followsTo(obj types.Object, depth int) (*types.Func, types.Type) {
	v, ok := obj.(*types.Var)
	if !ok || depth > 4 {
		return nil, nil
	}
	info := s.pkg.TypesInfo
	for _, bound := range s.bindings()[v] {
		switch e := ast.Unparen(bound).(type) {
		case *ast.SelectorExpr:
			if sel, ok := info.Selections[e]; ok {
				if fn, ok := sel.Obj().(*types.Func); ok {
					return fn, info.TypeOf(e.X)
				}
				continue
			}
			if fn, ok := info.Uses[e.Sel].(*types.Func); ok {
				return fn, nil
			}
			if fn, recv := s.followsTo(info.Uses[e.Sel], depth+1); fn != nil {
				return fn, recv
			}
		case *ast.Ident:
			if fn, ok := info.Uses[e].(*types.Func); ok {
				return fn, nil
			}
			if fn, recv := s.followsTo(info.Uses[e], depth+1); fn != nil {
				return fn, recv
			}
		}
	}
	return nil, nil
}

// signatureOf is the signature of what a call calls: the resolved function's
// where there is one, and otherwise the type of the expression being called —
// which is what answers for a func-typed field, a map entry or anything else
// whose binding is not visible from here.
func (s *source) signatureOf(call *ast.CallExpr, fn *types.Func) (*types.Signature, bool) {
	if fn != nil {
		if sig, ok := fn.Type().(*types.Signature); ok {
			return sig, true
		}
	}
	t := s.pkg.TypesInfo.TypeOf(ast.Unparen(call.Fun))
	if t == nil {
		return nil, false
	}
	sig, ok := t.Underlying().(*types.Signature)
	return sig, ok
}

// isAWayToFail reports whether an expression IS one: a reporter, or one of the
// six methods that fail one, however it is named or bound.
//
// outside.Check(t.Errorf, cond, msg) hands the failure over WITHOUT handing the
// reporter over, and what it is handed to can fail the test as surely as if it
// had been handed t. A name bound to that method is the same value with a
// shorter name. Where the binding cannot be seen at all — a struct field, a map
// entry, another package's var — Errorf's own SHAPE answers, and only Errorf's:
// the other five are shaped like every callback in the language.
func (s *source) isAWayToFail(e ast.Expr, depth int) bool {
	if e == nil || depth > 4 {
		return false
	}
	e = ast.Unparen(e)
	info := s.pkg.TypesInfo

	// A reporter itself: t, tb, testify's TestingT, anything with an Errorf.
	if t := info.TypeOf(e); t != nil && types.Implements(t, s.reporter) {
		return true
	}

	// A METHOD of one, answered by its name on the interface and never by its
	// shape — Logf is Errorf's twin in shape, and a step that logs is not a
	// step that fails.
	if sel, ok := e.(*ast.SelectorExpr); ok {
		if m, ok := info.Selections[sel]; ok && m.Kind() == types.MethodVal {
			fn, ok := m.Obj().(*types.Func)
			return ok && failing[fn.Name()] && types.Implements(m.Recv(), s.reporter)
		}
	}

	// A name this package binds to one of those.
	if obj := s.objectOf(e); obj != nil {
		for _, bound := range s.bindings()[obj] {
			if s.isAWayToFail(bound, depth+1) {
				return true
			}
		}
	}

	// And the shape, for a value bound somewhere this cannot read.
	if t := info.TypeOf(e); t != nil {
		if sig, ok := t.Underlying().(*types.Signature); ok && types.Identical(sig, s.fails) {
			return true
		}
	}
	return false
}

// handedAWayToFail reports whether a call is GIVEN one in an argument.
// takesAReporter reads the callee's signature and cannot see this: a parameter
// of type func(string, ...any) has no Errorf method, so nothing about the
// declaration says the call can fail a test, and everything about the argument
// does.
func (s *source) handedAWayToFail(call *ast.CallExpr) bool {
	for _, arg := range call.Args {
		if s.isAWayToFail(arg, 0) {
			return true
		}
	}
	return false
}

// putDown is one place a way to fail was left where nothing follows it.
type putDown struct {
	site  string
	where string
}

// waysToFailPutDown is every one of them: a struct field, a map or a slice
// element, a channel, or a caller's hands.
//
// THIS IS THE OTHER HALF OF "A VALUE DOES NOT STOP BEING TESTIFY BY BEING GIVEN
// A SHORTER NAME". A name is followed to what it was bound to, and a parameter
// is followed to what its call sites hand it; both are bindings this package
// can read. A FIELD IS NOT. At the call site the value's type is func(...any)
// or func() — the shape of every callback in the language — so reading those
// shapes as failures would make every deferred close a claim, and reading only
// Errorf's shape, which is what the walk does, leaves the other five and every
// testify function invisible.
//
// Measured before this existed: a struct holding assert.NotEmpty and a tb's
// FailNow, built and called in a walked body of a non-test file, carried two
// claims about the subject — one with a message, one bare — with this file's
// census unchanged at 183 cases and the tree at GO_TEST_EXIT=0. Neither was in
// the population, so neither needed a fault, a message or a proof.
//
// So it is refused where the value is PUT DOWN rather than guessed at where it
// is called. There it is still a selector or a name and answers for what it is.
// A way to fail is handed over, never stored.
//
// A REPORTER is not one of these. A call through a tb is answered by its type
// wherever the tb was kept, so keeping one is followed and is allowed; what is
// refused is one of the six METHODS, and any function that takes a reporter,
// left somewhere the walk cannot reach.
func (s *source) waysToFailPutDown() []putDown {
	var out []putDown
	look := func(e ast.Expr, where string) {
		if e == nil {
			return
		}
		t := s.pkg.TypesInfo.TypeOf(e)
		if t == nil {
			return
		}
		sig, ok := t.Underlying().(*types.Signature)
		if !ok {
			return
		}
		if s.isAWayToFail(e, 0) || takesAReporter(sig, s.reporter) {
			out = append(out, putDown{s.site(e.Pos()), where})
		}
	}
	for _, file := range s.pkg.Syntax {
		ast.Inspect(file, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.CompositeLit:
				for _, el := range v.Elts {
					if kv, ok := el.(*ast.KeyValueExpr); ok {
						el = kv.Value
					}
					look(el, "a struct field, a map entry or a slice element")
				}
			case *ast.AssignStmt:
				for i, lhs := range v.Lhs {
					switch lhs.(type) {
					case *ast.SelectorExpr, *ast.IndexExpr:
						if i < len(v.Rhs) {
							look(v.Rhs[i], "a field or an element assigned to")
						}
					}
				}
			case *ast.SendStmt:
				look(v.Value, "a channel")
			case *ast.ReturnStmt:
				for _, e := range v.Results {
					look(e, "a caller's hands")
				}
			}
			return true
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].site < out[j].site })
	return out
}

// objectOf is what an expression names, where it names anything.
func (s *source) objectOf(e ast.Expr) types.Object {
	info := s.pkg.TypesInfo
	switch v := ast.Unparen(e).(type) {
	case *ast.SelectorExpr:
		if sel, ok := info.Selections[v]; ok {
			return sel.Obj()
		}
		return info.Uses[v.Sel]
	case *ast.Ident:
		if obj := info.Uses[v]; obj != nil {
			return obj
		}
		return info.Defs[v]
	}
	return nil
}

// bindings is every value a name in this package is given: the right-hand side
// of the declaration or the assignment that binds it. It is read out of source
// for the same reason the population is — a name is not a value, and following
// one to what it was bound to is the difference between reading `var truthy =
// assert.True` as testify and reading it as nothing at all.
func (s *source) bindings() map[types.Object][]ast.Expr {
	s.boundOnce.Do(func() {
		out := map[types.Object][]ast.Expr{}
		bind := func(name *ast.Ident, value ast.Expr) {
			obj := s.pkg.TypesInfo.Defs[name]
			if obj == nil {
				obj = s.pkg.TypesInfo.Uses[name]
			}
			if obj != nil && value != nil {
				out[obj] = append(out[obj], value)
			}
		}
		for _, file := range s.pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.ValueSpec:
					for i, name := range v.Names {
						if i < len(v.Values) {
							bind(name, v.Values[i])
						}
					}
				case *ast.AssignStmt:
					if len(v.Lhs) != len(v.Rhs) {
						return true
					}
					for i, lhs := range v.Lhs {
						if id, ok := lhs.(*ast.Ident); ok {
							bind(id, v.Rhs[i])
						}
					}
				}
				return true
			})
		}
		s.bound = out
	})
	return s.bound
}

// markerFirings is every call that IS a marker rather than a claim written in
// one: a failure made through the reporter the marker was handed, reporting the
// QUESTION it was handed.
//
// The walk used to step over a marker's whole body, which meant a step
// parameter exempted everything written beside it — and a marker is any
// function that asks for a step, so the exemption cost one parameter. A marker
// is a function like any other now: its own firing is the step, already one
// entry at each of its call sites, and every other assertion in it is a claim
// with a fault to find.
func (s *source) markerFirings() map[*ast.CallExpr]bool {
	s.firingOnce.Do(func() {
		out := map[*ast.CallExpr]bool{}
		for _, file := range s.pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				body, sig := s.bodyAndSignature(n)
				if body == nil || sig == nil {
					return true
				}
				at := s.stepParam(sig)
				if at < 0 {
					return true
				}
				question := sig.Params().At(at)
				for _, call := range callsDirectlyIn(body) {
					fn, recv := s.callee(call)
					if fn == nil || recv == nil || !failing[fn.Name()] || !types.Implements(recv, s.reporter) {
						continue
					}
					if s.mentions(call, question) {
						out[call] = true
					}
				}
				return true
			})
		}
		s.firing = out
	})
	return s.firing
}

// mentions reports whether a call names an object anywhere in it.
func (s *source) mentions(call *ast.CallExpr, obj types.Object) bool {
	found := false
	ast.Inspect(call, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && s.pkg.TypesInfo.Uses[id] == obj {
			found = true
		}
		return true
	})
	return found
}

// bodyAndSignature is what a node holds if it holds a function: a declaration
// and a literal are the two places Go keeps a body, and this answers for both.
func (s *source) bodyAndSignature(n ast.Node) (*ast.BlockStmt, *types.Signature) {
	switch v := n.(type) {
	case *ast.FuncDecl:
		obj, _ := s.pkg.TypesInfo.Defs[v.Name].(*types.Func)
		if obj == nil {
			return nil, nil
		}
		sig, _ := obj.Type().(*types.Signature)
		return v.Body, sig
	case *ast.FuncLit:
		sig, _ := s.pkg.TypesInfo.TypeOf(v).(*types.Signature)
		return v.Body, sig
	}
	return nil, nil
}

// spell is how a call reads where it is written, which is what a failure
// message should quote back. It is a label and never an identity.
func (s *source) spell(call *ast.CallExpr) string {
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.SelectorExpr:
		if x, ok := ast.Unparen(fun.X).(*ast.Ident); ok {
			return x.Name + "." + fun.Sel.Name
		}
		return fun.Sel.Name
	case *ast.Ident:
		return fun.Name
	}
	return types.ExprString(call.Fun)
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

// population is every CASE in the suite, read out of the type-checked source.
func population(t *testing.T) []assertion {
	t.Helper()
	return populationOf(suiteSource(t))
}

func populationOf(src *source) []assertion {
	w := src.walk()
	callers := src.callers(w)

	var out []assertion
	seenStep := map[string]bool{}
	for _, c := range w.order {
		for _, call := range src.assertionsIn(c, w) {
			f, _ := src.howItFails(call)
			site := src.site(call.Pos())

			// A step needs no fault, so splitting one into cases buys nothing
			// and costs the ledger a way to tell two of them apart. One entry,
			// at the site where it is written.
			if f.kind == plumbing {
				if seenStep[site] {
					continue
				}
				seenStep[site] = true
				out = append(out, assertion{
					id: site, site: site, call: f.spelling, in: c.name, kind: plumbing,
					says: literalArg(call, f.at),
				})
				continue
			}

			format := literalArg(call, f.at)
			var args []ast.Expr
			if f.at+1 <= len(call.Args) {
				args = call.Args[f.at+1:]
			}
			for _, chain := range src.chainsFor(c, args, callers) {
				bound, id, where := src.bind(chain), site, ""
				for _, e := range chain {
					where = src.site(e.call.Pos())
					id += " via " + where
				}
				out = append(out, assertion{
					id: id, site: site, where: where, call: f.spelling, in: c.name, kind: contract,
					says: src.render(format, call, f.at, bound),
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// walk is every body in the suite and the way in to each of them.
type walk struct {
	byObj map[types.Object]*callable
	byLit map[*ast.FuncLit]*callable
	order []*callable
}

// walk is every function, method, named closure and stray literal in the suite,
// in a stable order — and it is EVERY body, which is the part that was missing.
//
// Go holds a body in exactly two places: a declaration and a literal. So:
//
//	every declaration is a root, markers included. A marker is a function like
//	any other and a claim written in one is a claim.
//
//	every literal a NAME is bound to is a root, so a case can pass through the
//	name — that is what makes the nine rows behind one closure nine cases.
//
//	every other literal is walked by the function that contains it, and a
//	literal that nothing contains is a root in its own right, whatever it was
//	bound to or bound into: a var, a map, a slice, a struct field, an argument.
//
// The third clause is not a list of binding forms; it is the lexical statement
// that nothing is left over, and unwalkedBodies below reports any body it does
// not reach. A package-level `var f = func(t tb) { assert... }` was measured
// living in the shared suite with the census unchanged, because the walk knew
// two of the places a name can be bound and a ValueSpec was neither.
func (s *source) walk() *walk {
	s.walkOnce.Do(func() { s.theWalk = s.everyBody() })
	return s.theWalk
}

func (s *source) everyBody() *walk {
	w := &walk{byObj: map[types.Object]*callable{}, byLit: map[*ast.FuncLit]*callable{}}
	add := func(obj types.Object, name string, body *ast.BlockStmt, sig *types.Signature, lit *ast.FuncLit) {
		if body == nil || sig == nil {
			return
		}
		if obj != nil && w.byObj[obj] != nil {
			return
		}
		if lit != nil && w.byLit[lit] != nil {
			return
		}
		var params []*types.Var
		for i := 0; i < sig.Params().Len(); i++ {
			params = append(params, sig.Params().At(i))
		}
		c := &callable{obj: obj, name: name, file: filepath.Base(s.position(body.Pos()).Filename),
			body: body, params: params, lit: lit}
		if obj != nil {
			w.byObj[obj] = c
		}
		if lit != nil {
			w.byLit[lit] = c
		}
		w.order = append(w.order, c)
	}
	bindLit := func(name *ast.Ident, value ast.Expr) {
		lit, ok := ast.Unparen(value).(*ast.FuncLit)
		if !ok {
			return
		}
		obj := s.pkg.TypesInfo.Defs[name]
		if obj == nil {
			obj = s.pkg.TypesInfo.Uses[name]
		}
		sig, _ := s.pkg.TypesInfo.TypeOf(lit).(*types.Signature)
		add(obj, name.Name, lit.Body, sig, lit)
	}
	for _, file := range s.pkg.Syntax {
		ast.Inspect(file, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.FuncDecl:
				obj, _ := s.pkg.TypesInfo.Defs[v.Name].(*types.Func)
				if obj == nil {
					return true
				}
				sig, _ := obj.Type().(*types.Signature)
				add(obj, v.Name.Name, v.Body, sig, nil)
			case *ast.ValueSpec:
				for i, name := range v.Names {
					if i < len(v.Values) {
						bindLit(name, v.Values[i])
					}
				}
			case *ast.AssignStmt:
				if len(v.Lhs) != len(v.Rhs) {
					return true
				}
				for i, lhs := range v.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						bindLit(id, v.Rhs[i])
					}
				}
			}
			return true
		})
	}
	// And every literal nothing above reaches, outermost first. This is the
	// clause that makes the walk complete rather than a list of the places a
	// body has been bound so far.
	for {
		stray := s.aBodyNothingWalks(w)
		if stray == nil {
			break
		}
		sig, _ := s.pkg.TypesInfo.TypeOf(stray).(*types.Signature)
		add(nil, "a function literal at "+s.site(stray.Pos()), stray.Body, sig, stray)
		if w.byLit[stray] == nil {
			break // a literal with no signature: nothing more to be done with it
		}
	}
	return w
}

// aBodyNothingWalks is the outermost function literal no root reaches, or nil.
func (s *source) aBodyNothingWalks(w *walk) *ast.FuncLit {
	reached := s.reaches(w)
	var found *ast.FuncLit
	for _, file := range s.pkg.Syntax {
		ast.Inspect(file, func(n ast.Node) bool {
			if found != nil {
				return false
			}
			lit, ok := n.(*ast.FuncLit)
			if !ok {
				return true
			}
			if reached[lit.Body] {
				return true
			}
			found = lit
			return false
		})
		if found != nil {
			return found
		}
	}
	return nil
}

// reaches is every body the roots enter: their own, and every literal written
// inside one that is not a root itself.
func (s *source) reaches(w *walk) map[*ast.BlockStmt]bool {
	out := map[*ast.BlockStmt]bool{}
	for _, c := range w.order {
		out[c.body] = true
		s.walkOwn(c, w, func(n ast.Node) bool {
			if lit, ok := n.(*ast.FuncLit); ok {
				out[lit.Body] = true
			}
			return true
		})
	}
	return out
}

// unwalkedBodies is every body in this package that the given walk does not
// enter, by the site it is written at. Over the walk above it is empty by
// construction; it is computed and reported anyway, because "by construction"
// is a claim about code somebody will edit, and an assertion in a body nothing
// walks is not exempt, not counted and not anywhere.
func (s *source) unwalkedBodies(w *walk) []string {
	reached := s.reaches(w)
	var out []string
	for _, file := range s.pkg.Syntax {
		ast.Inspect(file, func(n ast.Node) bool {
			body, sig := s.bodyAndSignature(n)
			if body == nil || sig == nil || reached[body] {
				return true
			}
			out = append(out, s.site(body.Pos()))
			return true
		})
	}
	sort.Strings(out)
	return out
}

// callers is who calls what, which is how a case knows where it came through.
func (s *source) callers(w *walk) map[*callable][]edge {
	out := map[*callable][]edge{}
	for _, c := range w.order {
		s.walkOwn(c, w, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if to := w.byObj[s.called(call)]; to != nil {
				out[to] = append(out[to], edge{from: c, to: to, call: call})
			}
			return true
		})
	}
	return out
}

// walkOwn walks the part of a callable's body that is ITS OWN: a literal that
// is a root in its own right is walked there and skipped here, and every other
// literal is part of the body it is written in.
func (s *source) walkOwn(c *callable, w *walk, f func(ast.Node) bool) {
	ast.Inspect(c.body, func(n ast.Node) bool {
		if lit, ok := n.(*ast.FuncLit); ok && lit != c.lit && w.byLit[lit] != nil {
			return false
		}
		return f(n)
	})
}

// assertionsIn is every failing call written in a callable's own body.
func (s *source) assertionsIn(c *callable, w *walk) []*ast.CallExpr {
	var out []*ast.CallExpr
	s.walkOwn(c, w, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if _, ok := s.howItFails(call); ok {
			out = append(out, call)
		}
		return true
	})
	return out
}

// chainsFor is every way the suite reaches ONE assertion, as a chain of call
// sites, and it climbs exactly as far as that assertion's WORDS need and no
// further.
//
// The rule is the whole design. An assertion's message may name things its own
// function was handed — the table row, the fixture, the capability, the label a
// caller supplied — and until those are constants the ledger cannot tell one
// case from another. So: while a word is still a parameter somebody else
// decides, climb to whoever decides it; when nothing is left to decide, stop.
//
// Climbing further would be wrong, not merely wasteful. Run calls run from two
// branches, so climbing to the top multiplies EVERY assertion in the suite by
// two, and those are not two contracts — it is one suite started two ways. And
// stopping short would be wrong too: assertRecord is reached through
// assertRepository, which passes down its own caller's word, so one level
// leaves six records saying the same thing.
func (s *source) chainsFor(c *callable, args []ast.Expr, callers map[*callable][]edge) [][]edge {
	type frame struct {
		at    *callable
		chain []edge
		alias map[types.Object]*types.Var // an argument, and the parameter that still decides it
	}
	start := frame{at: c, alias: map[types.Object]*types.Var{}}
	for _, arg := range args {
		id, ok := ast.Unparen(arg).(*ast.Ident)
		if !ok {
			continue
		}
		obj := s.pkg.TypesInfo.Uses[id]
		if v := paramAt(c, obj); v != nil {
			start.alias[obj] = v
		}
	}

	var out [][]edge
	queue := []frame{start}
	for len(queue) > 0 {
		f := queue[0]
		queue = queue[1:]
		if len(f.alias) == 0 || len(callers[f.at]) == 0 || len(f.chain) >= 8 {
			out = append(out, f.chain)
			continue
		}
		for _, e := range callers[f.at] {
			next := frame{at: e.from, chain: append(append([]edge{}, f.chain...), e), alias: map[types.Object]*types.Var{}}
			for obj, param := range f.alias {
				i := indexOf(f.at.params, param)
				if i < 0 || i >= len(e.call.Args) {
					continue
				}
				arg := ast.Unparen(e.call.Args[i])
				if _, ok := constantArg(s.pkg.TypesInfo, arg); ok {
					continue // decided here; nothing left to climb for
				}
				if id, ok := arg.(*ast.Ident); ok {
					if v := paramAt(e.from, s.pkg.TypesInfo.Uses[id]); v != nil {
						next.alias[obj] = v
					}
				}
			}
			queue = append(queue, next)
		}
	}
	return out
}

func paramAt(c *callable, obj types.Object) *types.Var {
	v, ok := obj.(*types.Var)
	if !ok {
		return nil
	}
	for _, p := range c.params {
		if p == v {
			return v
		}
	}
	return nil
}

func indexOf(params []*types.Var, want *types.Var) int {
	for i, p := range params {
		if p == want {
			return i
		}
	}
	return -1
}

// bind is what the constants along one chain of call sites make of the
// arguments an assertion's message names. It is how "a delivery signed an hour
// ago" gets from a table row into the words of the assertion that row is a
// contract of.
func (s *source) bind(chain []edge) map[types.Object]string {
	// Innermost first: each edge decides its callee's parameters, and a
	// parameter passed straight through is decided one level further out.
	bound := map[types.Object]string{}
	alias := map[types.Object]*types.Var{}
	for _, e := range chain {
		next := map[types.Object]string{}
		nextAlias := map[types.Object]*types.Var{}
		for i, arg := range e.call.Args {
			if i >= len(e.to.params) {
				break
			}
			param := e.to.params[i]
			if v, ok := constantArg(s.pkg.TypesInfo, ast.Unparen(arg)); ok {
				next[param] = v
				continue
			}
			if id, ok := ast.Unparen(arg).(*ast.Ident); ok {
				if v := paramAt(e.from, s.pkg.TypesInfo.Uses[id]); v != nil {
					nextAlias[param] = v
				}
			}
		}
		// What this level decided, written onto whatever was waiting for it.
		for obj, param := range alias {
			if v, ok := next[param]; ok {
				bound[obj] = v
				continue
			}
			if v, ok := nextAlias[param]; ok {
				nextAlias[obj] = v
			}
		}
		for param, v := range next {
			bound[param] = v
		}
		alias = nextAlias
	}
	return bound
}

// render is what an assertion says in ONE case: its format, with the arguments
// this chain makes constant written in where their verbs are. An argument the
// chain does not decide keeps its verb, and containsFormat reads what is left
// the same way it always has.
func (s *source) render(format string, call *ast.CallExpr, at int, bound map[types.Object]string) string {
	args := call.Args
	if at+1 <= len(args) {
		args = args[at+1:]
	} else {
		args = nil
	}
	i := -1
	return verb.ReplaceAllStringFunc(format, func(v string) string {
		i++
		if i >= len(args) {
			return v
		}
		value, ok := constantArg(s.pkg.TypesInfo, args[i])
		if !ok {
			if id, ok2 := ast.Unparen(args[i]).(*ast.Ident); ok2 {
				value, ok = bound[s.pkg.TypesInfo.Uses[id]]
			}
		}
		if !ok {
			return v
		}
		if strings.HasSuffix(v, "q") {
			return strconv.Quote(value)
		}
		return value
	})
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

// attribute turns what the recorder captured into the CASES that produced it,
// and keeps the words each one said — the ledger reads those words to tell a
// step the suite could not take from a claim somebody demoted. A failure it
// cannot place is a failure this file cannot reason about, so it says so rather
// than dropping it.
//
// The trace names the SITE. Where a site holds one case that is the answer;
// where it holds several — a helper reached from six places, a closure called
// by nine table rows — the case is the one whose words the message says, which
// is why two cases saying the same thing is a build failure.
func attribute(pop []assertion, msgs []string) (map[string][]string, []complaint) {
	atSite := map[string][]assertion{}
	for _, a := range pop {
		atSite[a.site] = append(atSite[a.site], a)
	}
	var said []complaint
	fired := make(map[string][]string)
	for _, msg := range msgs {
		candidates := pop
		if g := trace.FindStringSubmatch(msg); g != nil {
			candidates = atSite[g[1]]
			if len(candidates) == 0 {
				said = append(said, complaint{ruleFailureFromNowhere, g[1],
					"a failure came from here, which is not an assertion this file found in the suite's source:\n" + msg})
				continue
			}
			if len(candidates) == 1 {
				fired[candidates[0].id] = append(fired[candidates[0].id], msg)
				continue
			}
		}
		var hits []string
		for _, a := range candidates {
			if a.says != "" && containsFormat(msg, a.says) {
				hits = append(hits, a.id)
			}
		}
		if len(hits) != 1 {
			said = append(said, complaint{ruleUnplaceable, fmt.Sprintf("%v", hits),
				fmt.Sprintf("a failure names %d cases and must name one; it is unattributable, so nothing here can say which one is load-bearing:\n%s", len(hits), msg)})
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
