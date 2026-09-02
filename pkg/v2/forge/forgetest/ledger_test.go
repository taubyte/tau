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
	step     types.Type       // the marker's question type

	// faults is every fault the fake forge can be told to have, by the value
	// it carries and the constant it is declared as. It is read out of that
	// package's source rather than kept in a list here, because a list is a
	// thing you can forget to add to — which is the whole reason the
	// population is read out of source too.
	faults map[string]string
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
	// The fake forge is loaded beside the suite so its faults can be counted.
	// Its import path comes from the type itself and not from a string here.
	faultType := reflect.TypeOf(fake.Fault(""))
	pkgs, err := packages.Load(cfg, ".", faultType.PkgPath())
	if err != nil {
		return nil, err
	}
	if len(pkgs) != 2 {
		return nil, fmt.Errorf("loading the suite and the fake forge answered %d packages", len(pkgs))
	}
	var pkg, faked *packages.Package
	for _, p := range pkgs {
		if p.PkgPath == faultType.PkgPath() {
			faked = p
			continue
		}
		pkg = p
	}
	if pkg == nil || faked == nil {
		return nil, fmt.Errorf("loading the suite and the fake forge answered %v", pkgs)
	}
	for _, p := range []*packages.Package{pkg, faked} {
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
	faults, err := faultsOf(faked, faultType.Name())
	if err != nil {
		return nil, err
	}
	return &source{pkg: pkg, reporter: reporterInterface(), step: obj.Type(), faults: faults}, nil
}

// faultsOf is every constant of the fake forge's fault type, by value and by
// the name it is declared under. A fault is a question the fake forge can be
// told to answer wrongly, and the ledger reads them all so that one nothing
// proves is visible.
func faultsOf(pkg *packages.Package, typeName string) (map[string]string, error) {
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

// population is every CASE in the suite, read out of the type-checked source.
func population(t *testing.T) []assertion {
	t.Helper()
	return populationOf(suiteSource(t))
}

func populationOf(src *source) []assertion {
	callables, order := src.callables()
	callers := src.callers(callables, order)

	var out []assertion
	seenStep := map[string]bool{}
	for _, c := range order {
		for _, call := range src.assertionsIn(c, callables) {
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

// callables is every function, method and named closure in the suite, in a
// stable order.
func (s *source) callables() (map[types.Object]*callable, []*callable) {
	byObj := map[types.Object]*callable{}
	var order []*callable
	add := func(obj types.Object, name string, body *ast.BlockStmt, sig *types.Signature, lit *ast.FuncLit) {
		if obj == nil || body == nil || sig == nil || byObj[obj] != nil {
			return
		}
		var params []*types.Var
		for i := 0; i < sig.Params().Len(); i++ {
			params = append(params, sig.Params().At(i))
		}
		c := &callable{obj: obj, name: name, file: filepath.Base(s.position(body.Pos()).Filename),
			body: body, params: params, lit: lit}
		byObj[obj] = c
		order = append(order, c)
	}
	for _, file := range s.pkg.Syntax {
		ast.Inspect(file, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.FuncDecl:
				if s.isMarkerDecl(v) {
					return false
				}
				obj, _ := s.pkg.TypesInfo.Defs[v.Name].(*types.Func)
				if obj == nil {
					return true
				}
				sig, _ := obj.Type().(*types.Signature)
				add(obj, v.Name.Name, v.Body, sig, nil)
			case *ast.AssignStmt:
				if len(v.Lhs) != 1 || len(v.Rhs) != 1 {
					return true
				}
				lit, ok := v.Rhs[0].(*ast.FuncLit)
				if !ok {
					return true
				}
				id, ok := v.Lhs[0].(*ast.Ident)
				if !ok {
					return true
				}
				obj := s.pkg.TypesInfo.Defs[id]
				if obj == nil {
					obj = s.pkg.TypesInfo.Uses[id]
				}
				sig, _ := s.pkg.TypesInfo.TypeOf(lit).(*types.Signature)
				add(obj, id.Name, lit.Body, sig, lit)
			}
			return true
		})
	}
	return byObj, order
}

// callers is who calls what, which is how a case knows where it came through.
func (s *source) callers(byObj map[types.Object]*callable, order []*callable) map[*callable][]edge {
	out := map[*callable][]edge{}
	for _, c := range order {
		s.walkOwn(c, byObj, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if to := byObj[s.called(call)]; to != nil {
				out[to] = append(out[to], edge{from: c, to: to, call: call})
			}
			return true
		})
	}
	return out
}

// walkOwn walks the part of a callable's body that is ITS OWN: a named closure
// inside it is walked in its own right and skipped here, and an inline literal
// is part of the body it is written in.
func (s *source) walkOwn(c *callable, byObj map[types.Object]*callable, f func(ast.Node) bool) {
	ast.Inspect(c.body, func(n ast.Node) bool {
		if lit, ok := n.(*ast.FuncLit); ok {
			for _, other := range byObj {
				if other.lit == lit {
					return false
				}
			}
		}
		return f(n)
	})
}

// assertionsIn is every failing call written in a callable's own body.
func (s *source) assertionsIn(c *callable, byObj map[types.Object]*callable) []*ast.CallExpr {
	var out []*ast.CallExpr
	s.walkOwn(c, byObj, func(n ast.Node) bool {
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
