package forgetest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestEveryAssertionSaysWhatItMeans is the first half of naming: an assertion
// with no message cannot be named by a proof, and two assertions saying the
// same thing cannot be told apart — which is how a self-test case came to pass
// on an assertion it was not written for.
//
// A held assertion is allowed to say nothing, because nothing here can name it
// anyway: the fault behind it is one package down and so is the test that holds
// it. Whatever it does say still has to be its own.
func TestEveryAssertionSaysWhatItMeans(t *testing.T) {
	pop := population(t)
	for _, a := range pop {
		if a.says == "" && a.kind != held {
			t.Errorf("%s: %s says nothing, so no proof can name it and nothing can know it is load-bearing", a.site, a.call)
		}
	}
	for i, a := range pop {
		for j, b := range pop {
			if i == j || a.says == "" || b.says == "" {
				continue
			}
			if strings.Contains(b.says, a.says) {
				t.Errorf("%s and %s: %q is contained in %q, so a proof naming the first would be answered by the second",
					a.site, b.site, a.says, b.says)
			}
		}
	}
}

// TestEveryPlumbingStepIsOnItsWayToAQuestion. A step is exempt from needing a
// fault because it is not a claim — it is how the suite GETS to a claim. So
// there has to be one: a real assertion after it in the same function, or a
// function that hands its caller the value a question is asked with.
//
// A step that is the last thing a subtest does was never a step. It is a claim
// about the driver wearing a name that excuses it from proof, which is this
// file's own defect, one level up.
func TestEveryPlumbingStepIsOnItsWayToAQuestion(t *testing.T) {
	for _, bad := range stepsGoingNowhere(t) {
		t.Errorf("%s: this step is on its way to nothing — no assertion follows it in %s, and %s returns no value a caller could ask a question with",
			bad.site, bad.fn, bad.fn)
	}
}

// TestEveryHeldAssertionNamesATestThatHoldsIt. The marker's whole claim is that
// the fault behind this assertion lives in pkg/v2/forge and that a named test
// there is standing on it. A name that has been renamed away, or was never
// there, is the marker having become a sentence nobody can check.
func TestEveryHeldAssertionNamesATestThatHoldsIt(t *testing.T) {
	have := testsInForge(t)
	for _, a := range population(t) {
		if a.kind != held {
			continue
		}
		if !have[a.by] {
			t.Errorf("%s: is marked as held by forge's %s, and pkg/v2/forge has no such test",
				a.site, a.by)
		}
	}
}

// TestEveryProofSaysExactlyWhatItProves runs each proof and requires the suite
// to complain about EXACTLY the assertions the proof names. Exactness is what
// makes each named assertion load-bearing: delete it and the set no longer
// matches, which is the self-test noticing.
//
// It is also where a STOP is held to what a stop means. A step is exempt from
// needing a fault, so "respell the claim as a step and move its saying to
// stops" used to be a free edit that hid a live assertion. It is not free here:
// a stopped step must have failed because the session refuses a capability,
// which is the one honest reason the suite can be somewhere it cannot ask its
// question. Anything else that stopped is a complaint about the subject wearing
// a step's name.
func TestEveryProofSaysExactlyWhatItProves(t *testing.T) {
	pop := population(t)
	for _, p := range allProofs() {
		t.Run(p.name, func(t *testing.T) {
			expected := names(t, pop, p)
			stopped := stops(t, pop, p)
			for site := range stopped {
				expected[site] = true
			}
			// A stop is COLLATERAL: the proof's own complaint is somewhere
			// else, and this step is a place the broken subject kept the suite
			// out of. A step in the very function the proof is otherwise about
			// is not collateral — it is where the proof's complaint would have
			// been, which is what a demoted claim looks like from here.
			for site := range stopped {
				for named := range expected {
					if named == site || stopped[named] {
						continue
					}
					if fnOf(pop, named) == fnOf(pop, site) {
						t.Errorf("%s is stopped by this proof and %s is named by it, and both are in %s: a step in the function a proof is about is not a step it could not reach, it is the complaint itself wearing a step's name",
							site, named, fnOf(pop, site))
					}
				}
			}

			rec := record(p.subject(t))
			if !rec.failed {
				t.Fatalf("the suite passed a subject that is wrong in a named way")
			}
			fired := attribute(t, pop, rec.msgs)
			for site, msgs := range fired {
				if !expected[site] {
					t.Errorf("%s fired and is not named by this proof: %s\nan assertion that fires unnamed is one whose deletion this proof would not notice",
						site, sayingAt(pop, site))
					continue
				}
				if !stopped[site] {
					continue
				}
				for _, msg := range msgs {
					if strings.Contains(msg, refusedCapability) {
						continue
					}
					t.Errorf("%s is listed in this proof's stops and did not stop: it fired because the subject answered wrongly, not because the session refuses a capability.\n%s\nA step this proof can make fire for any other reason was never plumbing — it is a claim, and it belongs in says with a fault behind it.",
						site, msg)
				}
			}
			for site := range expected {
				if len(fired[site]) == 0 {
					t.Errorf("%s is named by this proof and did not fire: %s\n%s", site, sayingAt(pop, site), rec.String())
				}
			}
		})
	}
}

// TestEveryProofComplainsAboutSomething. stops accounts for what a broken
// subject PREVENTS; says is what the suite SAYS about it. A proof that only
// prevents has proved nothing — the fault it injects produced no complaint, and
// an assertion whose only proof is a proof like that has no fault behind it
// after all. It is also the shape a demotion leaves behind when the demoted
// claim was the whole of what a fault was written for.
func TestEveryProofComplainsAboutSomething(t *testing.T) {
	pop := population(t)
	for _, p := range allProofs() {
		if len(names(t, pop, p)) == 0 {
			t.Errorf("proof %q names no claim: it stops %v and complains about nothing, so nothing it touches is load-bearing",
				p.name, p.stops)
		}
	}
}

// TestEveryAssertionHasAFaultBehindIt is the guard itself. A contract assertion
// no proof names is an assertion with no fault behind it: either nothing can
// reach it, or its deletion would go unnoticed, and from out here those are the
// same thing.
func TestEveryAssertionHasAFaultBehindIt(t *testing.T) {
	pop := population(t)
	proven := map[string]string{}
	for _, p := range allProofs() {
		for site := range names(t, pop, p) {
			proven[site] = p.name
		}
	}
	var orphans []string
	counts := map[kind]int{}
	for _, a := range pop {
		counts[a.kind]++
		if a.kind != contract || proven[a.site] != "" {
			continue
		}
		orphans = append(orphans, a.site+"\t"+a.call+"\t"+a.says)
	}
	t.Logf("%d assertions: %d contract, %d plumbing, %d held by forge",
		len(pop), counts[contract], counts[plumbing], counts[held])
	sort.Strings(orphans)
	if len(orphans) > 0 {
		t.Errorf("%d of %d contract assertions have no fault behind them:\n%s",
			len(orphans), counts[contract], strings.Join(orphans, "\n"))
	}
}

// names maps a proof's sayings onto the assertions they name, and refuses a
// saying that names anything but one contract assertion. A proof that can name
// a step or a held assertion is a proof saying that assertion IS reachable by a
// driver fault, which is the one thing its marker claims it is not.
func names(t *testing.T, pop []assertion, p proof) map[string]bool {
	t.Helper()
	out := make(map[string]bool, len(p.says))
	for _, saying := range p.says {
		var hits []assertion
		for _, a := range pop {
			if a.says != "" && strings.Contains(a.says, saying) {
				hits = append(hits, a)
			}
		}
		switch len(hits) {
		case 1:
			if hits[0].kind != contract {
				t.Errorf("proof %q says %q, which is %s at %s — a driver fault can kill it, so it is a claim about the driver and must be spelled as one",
					p.name, saying, hits[0].kind, hits[0].site)
				continue
			}
			out[hits[0].site] = true
		case 0:
			t.Errorf("proof %q says %q, which no assertion in the suite says", p.name, saying)
		default:
			var sites []string
			for _, h := range hits {
				sites = append(sites, h.site)
			}
			t.Errorf("proof %q says %q, which %d assertions say: %v — a saying must name one assertion or it proves whichever happens to fire",
				p.name, saying, len(hits), sites)
		}
	}
	return out
}

// stops maps a proof's stops onto the STEPS this subject prevents the suite
// from taking, and refuses one that names a claim. A step and a claim are told
// apart here and nowhere else, so the two lists cannot quietly become one.
func stops(t *testing.T, pop []assertion, p proof) map[string]bool {
	t.Helper()
	out := make(map[string]bool, len(p.stops))
	for _, saying := range p.stops {
		var hits []assertion
		for _, a := range pop {
			if a.says != "" && strings.Contains(a.says, saying) {
				hits = append(hits, a)
			}
		}
		switch len(hits) {
		case 1:
			if hits[0].kind == contract {
				t.Errorf("proof %q stops %q, which is a claim about the driver at %s — a claim this subject makes the suite make belongs in says, where it counts as proof",
					p.name, saying, hits[0].site)
				continue
			}
			out[hits[0].site] = true
		case 0:
			t.Errorf("proof %q stops %q, which no step in the suite says", p.name, saying)
		default:
			var sites []string
			for _, h := range hits {
				sites = append(sites, h.site)
			}
			t.Errorf("proof %q stops %q, which %d places say: %v", p.name, saying, len(hits), sites)
		}
	}
	return out
}

// fnOf is the function an assertion is written in, which is how a stop that is
// collateral is told from one that is the proof's own complaint.
func fnOf(pop []assertion, site string) string {
	for _, a := range pop {
		if a.site == site {
			return a.in
		}
	}
	return "?"
}

func sayingAt(pop []assertion, site string) string {
	for _, a := range pop {
		if a.site == site {
			return a.kind.String() + " " + a.call + " " + a.says
		}
	}
	return "?"
}

// ─────────────────────── reading the shape of the source ───────────────────────

type step struct{ site, fn string }

// stepsGoingNowhere is every mustReach/mustHold with nothing after it. "After"
// means, in the function it is written in: a real assertion, or a call to
// something in this package that makes one. A function that RETURNS a value is
// on its way by construction — the question is its caller's.
func stepsGoingNowhere(t *testing.T) []step {
	t.Helper()
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("the suite's own source is not where this test runs from: %v", err)
	}

	parsed := map[string]*ast.File{}
	asserting := map[string]bool{} // functions whose body makes a claim
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		parsed[name] = file
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || markers[fn.Name.Name] {
				continue
			}
			if asserts(fn) {
				asserting[fn.Name.Name] = true
			}
		}
		// And the closures a function binds to a name and then calls, which is
		// how this suite spells "every wrong delivery, one line each".
		ast.Inspect(file, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
				return true
			}
			lit, ok := assign.Rhs[0].(*ast.FuncLit)
			if !ok {
				return true
			}
			if id, ok := assign.Lhs[0].(*ast.Ident); ok && asserts(lit) {
				asserting[id.Name] = true
			}
			return true
		})
	}

	var out []step
	for name, file := range parsed {
		ast.Inspect(file, func(n ast.Node) bool {
			var body *ast.BlockStmt
			var results *ast.FieldList
			label := "a function literal"
			switch fn := n.(type) {
			case *ast.FuncDecl:
				if markers[fn.Name.Name] {
					return false
				}
				body, results, label = fn.Body, fn.Type.Results, fn.Name.Name
			case *ast.FuncLit:
				body, results = fn.Body, fn.Type.Results
			default:
				return true
			}
			if body == nil || results.NumFields() > 0 {
				return true
			}
			// The steps and the questions in THIS body, ignoring nested
			// function literals — those are walked in their own right.
			var steps, questions []token.Pos
			for _, call := range callsDirectlyIn(body) {
				spelling, _, isFailure := howItFails(call)
				switch {
				case isFailure && markers[spelling]:
					steps = append(steps, call.Pos())
				case isFailure:
					questions = append(questions, call.Pos())
				default:
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && asserting[sel.Sel.Name] {
						questions = append(questions, call.Pos())
					}
					if id, ok := call.Fun.(*ast.Ident); ok && asserting[id.Name] {
						questions = append(questions, call.Pos())
					}
				}
			}
			for _, at := range steps {
				onItsWay := false
				for _, q := range questions {
					if q > at {
						onItsWay = true
					}
				}
				if !onItsWay {
					pos := fset.Position(at)
					out = append(out, step{
						site: filepath.Base(name) + ":" + itoa(pos.Line),
						fn:   label,
					})
				}
			}
			return true
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].site < out[j].site })
	return out
}

// callsDirectlyIn is every call in a body that is not inside a nested function
// literal, because a literal is its own place and is walked as one.
// asserts reports whether a body makes a claim of its own — a real assertion
// and not a step.
func asserts(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if spelling, _, ok := howItFails(call); ok && !markers[spelling] {
				found = true
			}
		}
		return true
	})
	return found
}

func callsDirectlyIn(body *ast.BlockStmt) []*ast.CallExpr {
	var out []*ast.CallExpr
	var walk func(ast.Node)
	walk = func(n ast.Node) {
		ast.Inspect(n, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.FuncLit:
				return false
			case *ast.CallExpr:
				out = append(out, v)
			}
			return true
		})
	}
	walk(body)
	return out
}

// testsInForge is every Test function in pkg/v2/forge, which is what a
// `held by forge:` marker has to name.
func testsInForge(t *testing.T) map[string]bool {
	t.Helper()
	files, err := filepath.Glob("../*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("pkg/v2/forge's own tests are not beside this package: %v", err)
	}
	fset := token.NewFileSet()
	out := map[string]bool{}
	for _, name := range files {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && strings.HasPrefix(fn.Name.Name, "Test") {
				out[fn.Name.Name] = true
			}
		}
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
