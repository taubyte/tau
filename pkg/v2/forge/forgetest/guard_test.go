package forgetest

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"
	"sync"
	"testing"
)

// THE LEDGER IS A PROGRAM, AND IT HAD NO GUARDS OF ITS OWN.
//
// Every rule in this file says the same thing about the suite: a rule with no
// fault behind it is decoration. The rules themselves were exempt from it.
// Measured, one at a time, twelve of twelve survived being hollowed —
// `if len(orphans) > 0` changed to `if false && len(orphans) > 0`, which is
// the orphan report switched off entirely, and the tree stayed at exit 0. So
// did the capability-refusal rule, which was the whole of the previous round's
// fix; so did the collateral rule; so did every complaint names(), stops() and
// attribute() make. Nothing here could tell a rule that holds from a rule that
// has been turned off.
//
// So a rule is a VALUE now, with three parts and no way to have two of them:
//
//	its name, which is what a failure is reported under;
//	its check, a function of the ledger that answers with what it has to say;
//	its violation, a ledger that breaks it, which the self-test below runs it
//	against and requires it to complain about.
//
// A rule whose violation it stays quiet about fails by name. A rule declared
// with no violation fails by name. And the violations are not sketches: a rule
// that reads the SOURCE is shown a real extra file in this very package,
// type-checked by the same loader — the ledger's own version of injecting a
// fault into the fake forge and requiring the suite to catch it.
//
// WHAT IT STILL DOES NOT COVER, said plainly: deleting a rule outright — its
// entry, its check and its violation together — removes the check and the thing
// that guards it in one hunk, and nothing here notices. That is the same
// exposure as deleting a test, and it is visible in a diff in a way that
// hollowing one was not.

// complaint is one thing the ledger has to say. It carries the rule that said
// it so that a rule proven by the self-test is proven under its own name and
// not under whichever rule happened to fire.
type complaint struct {
	rule string
	at   string
	text string
}

func (c complaint) String() string { return c.at + ": " + c.text }

// The rules, by name. A name is what a failure is reported under and what the
// self-test proves, so they are written once.
const (
	ruleSaysNothing        = "an assertion that says nothing"
	ruleSayingsCollide     = "a saying some other assertion also says"
	ruleStepGoesNowhere    = "a step on its way to nothing"
	ruleEmptyPopulation    = "a population with nothing in it"
	ruleUnknownSaying      = "a proof naming words no assertion says"
	ruleAmbiguousSaying    = "a proof naming words several assertions say"
	ruleSayingNamesAStep   = "a proof naming a step as though it were a claim"
	ruleUnknownStop        = "a proof stopping words no step says"
	ruleAmbiguousStop      = "a proof stopping words several places say"
	ruleStopNamesAClaim    = "a proof stopping a claim"
	ruleStopIsTheComplaint = "a stop in the function the proof is about"
	ruleProofPassed        = "a proof the suite does not fail"
	ruleFailureFromNowhere = "a failure from outside the population"
	ruleUnplaceable        = "a failure no single assertion claims"
	ruleFiredUnnamed       = "an assertion that fires unnamed"
	ruleStopDidNotStop     = "a stop that is not a capability refusal"
	ruleNamedDidNotFire    = "an assertion named and not firing"
	ruleProofProvesNothing = "a proof that complains about nothing"
	ruleOrphan             = "a contract assertion with no fault behind it"
	ruleStrandedFault      = "a fault with no proof in front of it"
)

// ledger is everything the rules are read over: the suite's own source, the
// proofs, and what the suite SAID about each of them. A rule is a function of
// this and nothing else, which is what lets a violating one be handed over.
type ledger struct {
	src    *source
	pop    []assertion
	proofs []proof
	said   map[string]*recorder
}

// rule is a check and the fault behind it.
type rule struct {
	name     string
	check    func(*ledger) []complaint
	violates func(*testing.T) *ledger
}

func ledgerRules() []rule {
	return []rule{
		{ruleSaysNothing, checkSaysNothing, violateSaysNothing},
		{ruleSayingsCollide, checkSayingsCollide, violateSayingsCollide},
		{ruleStepGoesNowhere, checkStepGoesNowhere, violateStepGoesNowhere},
		{ruleEmptyPopulation, checkEmptyPopulation, violateEmptyPopulation},
		{ruleUnknownSaying, checkNames(ruleUnknownSaying), violateUnknownSaying},
		{ruleAmbiguousSaying, checkNames(ruleAmbiguousSaying), violateAmbiguousSaying},
		{ruleSayingNamesAStep, checkNames(ruleSayingNamesAStep), violateSayingNamesAStep},
		{ruleUnknownStop, checkStops(ruleUnknownStop), violateUnknownStop},
		{ruleAmbiguousStop, checkStops(ruleAmbiguousStop), violateAmbiguousStop},
		{ruleStopNamesAClaim, checkStops(ruleStopNamesAClaim), violateStopNamesAClaim},
		{ruleStopIsTheComplaint, checkCollateral, violateStopIsTheComplaint},
		{ruleProofPassed, checkRun(ruleProofPassed), violateProofPassed},
		{ruleFailureFromNowhere, checkRun(ruleFailureFromNowhere), violateFailureFromNowhere},
		{ruleUnplaceable, checkRun(ruleUnplaceable), violateUnplaceable},
		{ruleFiredUnnamed, checkRun(ruleFiredUnnamed), violateFiredUnnamed},
		{ruleStopDidNotStop, checkRun(ruleStopDidNotStop), violateStopDidNotStop},
		{ruleNamedDidNotFire, checkRun(ruleNamedDidNotFire), violateNamedDidNotFire},
		{ruleProofProvesNothing, checkProofProvesNothing, violateProofProvesNothing},
		{ruleOrphan, checkOrphan, violateOrphan},
		{ruleStrandedFault, checkStrandedFault, violateStrandedFault},
	}
}

// TestTheSuiteObeysTheLedger is the ledger read over the suite as it stands.
func TestTheSuiteObeysTheLedger(t *testing.T) {
	l := theLedger(t)
	counts := map[kind]int{}
	for _, a := range l.pop {
		counts[a.kind]++
	}
	t.Logf("%d assertions: %d contract, %d plumbing", len(l.pop), counts[contract], counts[plumbing])
	t.Logf("%d proofs against %d faults the fake forge can be told to have", len(l.proofs), len(l.src.faults))
	for _, r := range ledgerRules() {
		t.Run(r.name, func(t *testing.T) {
			for _, c := range r.check(l) {
				t.Errorf("%s", c)
			}
		})
	}
}

// TestTheLedgerObeysTheRuleItImposes is the other direction, and it is the one
// that was missing. Each rule is handed a ledger that breaks it and must say
// so, under its own name. A rule that has been hollowed says nothing and fails
// here; a rule added with no violation behind it fails here too.
func TestTheLedgerObeysTheRuleItImposes(t *testing.T) {
	for _, r := range ledgerRules() {
		t.Run(r.name, func(t *testing.T) {
			if r.violates == nil {
				t.Fatalf("this rule has no ledger that breaks it, so nothing knows whether it still holds — which is exactly what it accuses an assertion of")
			}
			said := r.check(r.violates(t))
			if len(said) == 0 {
				t.Fatalf("this rule was handed a ledger that breaks it and said nothing: it is decoration, and hollowing it out would go unnoticed")
			}
			for _, c := range said {
				if c.rule != r.name {
					t.Errorf("a ledger that breaks %q was answered under %q instead: %s", r.name, c.rule, c)
				}
			}
		})
	}
}

// theLedger is the suite, its proofs, and what the suite says about each of
// them. It is built once: every rule reads the same run.
var (
	ledgerOnce sync.Once
	theOne     *ledger
)

func theLedger(t *testing.T) *ledger {
	t.Helper()
	ledgerOnce.Do(func() {
		l := &ledger{
			src:    suiteSource(t),
			proofs: allProofs(),
			said:   map[string]*recorder{},
		}
		l.pop = populationOf(l.src)
		for _, p := range l.proofs {
			l.said[p.name] = record(p.subject(t))
		}
		theOne = l
	})
	return theOne
}

// ─────────────────────────────── the rules ───────────────────────────────

// checkSaysNothing. An assertion with no message cannot be named by a proof,
// so nothing can know it is load-bearing.
func checkSaysNothing(l *ledger) []complaint {
	var out []complaint
	for _, a := range l.pop {
		if a.says == "" {
			out = append(out, complaint{ruleSaysNothing, a.site,
				a.call + " says nothing, so no proof can name it and nothing can know it is load-bearing"})
		}
	}
	return out
}

// checkSayingsCollide. Two assertions that say the same thing cannot be told
// apart, which is how a self-test case came to pass on an assertion it was not
// written for.
func checkSayingsCollide(l *ledger) []complaint {
	var out []complaint
	for i, a := range l.pop {
		for j, b := range l.pop {
			if i == j || a.says == "" || b.says == "" {
				continue
			}
			if strings.Contains(b.says, a.says) {
				out = append(out, complaint{ruleSayingsCollide, a.site,
					fmt.Sprintf("%q is contained in what %s says, %q, so a proof naming the first would be answered by the second",
						a.says, b.site, b.says)})
			}
		}
	}
	return out
}

// checkStepGoesNowhere. A step is exempt from needing a fault because it is not
// a claim — it is how the suite GETS to a claim. So there has to be one: a real
// assertion after it in the same function, or a function that hands its caller
// the value a question is asked with. A step that is the last thing a subtest
// does was never a step; it is a claim wearing a name that excuses it from
// proof, which is this file's own defect one level up.
func checkStepGoesNowhere(l *ledger) []complaint {
	var out []complaint
	for _, bad := range stepsGoingNowhere(l.src) {
		out = append(out, complaint{ruleStepGoesNowhere, bad.site,
			fmt.Sprintf("this step is on its way to nothing — no assertion follows it in %s, and %s returns no value a caller could ask a question with", bad.fn, bad.fn)})
	}
	return out
}

// checkEmptyPopulation. Every rule here is computed over the population, so a
// population that came back empty is a ledger that passes everything.
func checkEmptyPopulation(l *ledger) []complaint {
	if len(l.pop) > 0 {
		return nil
	}
	return []complaint{{ruleEmptyPopulation, "the suite",
		"the population is empty, so every rule below holds vacuously and this file is checking nothing at all"}}
}

// checkNames and checkStops report what names() and stops() have to say about
// every proof, filtered to the one rule being read. They are three rules each
// because a proof can be wrong in three ways about a name, and a rule is the
// unit the self-test proves.
func checkNames(only string) func(*ledger) []complaint {
	return func(l *ledger) []complaint {
		var out []complaint
		for _, p := range l.proofs {
			_, said := names(l.pop, p)
			out = append(out, filterBy(only, said)...)
		}
		return out
	}
}

func checkStops(only string) func(*ledger) []complaint {
	return func(l *ledger) []complaint {
		var out []complaint
		for _, p := range l.proofs {
			_, said := stops(l.pop, p)
			out = append(out, filterBy(only, said)...)
		}
		return out
	}
}

// checkCollateral. A stop is COLLATERAL: the proof's own complaint is somewhere
// else, and this step is a place the broken subject kept the suite out of. A
// step in the very function the proof is otherwise about is not collateral — it
// is where the proof's complaint would have been, which is what a demoted claim
// looks like from here.
func checkCollateral(l *ledger) []complaint {
	var out []complaint
	for _, p := range l.proofs {
		named, _ := names(l.pop, p)
		stopped, _ := stops(l.pop, p)
		for site := range stopped {
			for at := range named {
				if at == site || stopped[at] {
					continue
				}
				if fnOf(l.pop, at) == fnOf(l.pop, site) {
					out = append(out, complaint{ruleStopIsTheComplaint, site,
						fmt.Sprintf("proof %q stops this and names %s, and both are in %s: a step in the function a proof is about is not a step it could not reach, it is the complaint itself wearing a step's name",
							p.name, at, fnOf(l.pop, site))})
				}
			}
		}
	}
	return out
}

// checkRun is everything that can only be known by running a proof: what the
// suite actually said about a subject that is wrong in a named way, against
// what the proof claims it says.
func checkRun(only string) func(*ledger) []complaint {
	return func(l *ledger) []complaint {
		var out []complaint
		for _, p := range l.proofs {
			out = append(out, filterBy(only, runProof(l, p))...)
		}
		return out
	}
}

func runProof(l *ledger, p proof) []complaint {
	var out []complaint
	named, _ := names(l.pop, p)
	stopped, _ := stops(l.pop, p)
	expected := map[string]bool{}
	for site := range named {
		expected[site] = true
	}
	for site := range stopped {
		expected[site] = true
	}

	rec := l.said[p.name]
	if rec == nil || !rec.failed {
		return append(out, complaint{ruleProofPassed, p.name,
			"the suite passed a subject that is wrong in a named way, so the fault this proof injects proves nothing"})
	}
	fired, said := attribute(l.pop, rec.msgs)
	out = append(out, said...)

	for site, msgs := range fired {
		if !expected[site] {
			out = append(out, complaint{ruleFiredUnnamed, site,
				fmt.Sprintf("this fired under proof %q and is not named by it: %s\nan assertion that fires unnamed is one whose deletion this proof would not notice",
					p.name, sayingAt(l.pop, site))})
			continue
		}
		if !stopped[site] {
			continue
		}
		for _, msg := range msgs {
			if strings.Contains(msg, refusedCapability) {
				continue
			}
			out = append(out, complaint{ruleStopDidNotStop, site,
				fmt.Sprintf("proof %q lists this in stops and it did not stop: it fired because the subject answered wrongly, not because the session refuses a capability.\n%s\nA step this proof can make fire for any other reason was never plumbing — it is a claim, and it belongs in says with a fault behind it.",
					p.name, msg)})
		}
	}
	for site := range expected {
		if len(fired[site]) == 0 {
			out = append(out, complaint{ruleNamedDidNotFire, site,
				fmt.Sprintf("proof %q names this and it did not fire: %s\n%s", p.name, sayingAt(l.pop, site), rec)})
		}
	}
	return out
}

// checkProofProvesNothing. stops accounts for what a broken subject PREVENTS;
// says is what the suite SAYS about it. A proof that only prevents has proved
// nothing — the fault it injects produced no complaint, and an assertion whose
// only proof is a proof like that has no fault behind it after all. It is also
// the shape a demotion leaves behind when the demoted claim was the whole of
// what a fault was written for.
func checkProofProvesNothing(l *ledger) []complaint {
	var out []complaint
	for _, p := range l.proofs {
		named, _ := names(l.pop, p)
		if len(named) == 0 {
			out = append(out, complaint{ruleProofProvesNothing, p.name,
				fmt.Sprintf("this proof names no claim: it stops %v and complains about nothing, so nothing it touches is load-bearing", p.stops)})
		}
	}
	return out
}

// checkOrphan is the guard itself. A contract assertion no proof names is an
// assertion with no fault behind it: either nothing can reach it, or its
// deletion would go unnoticed, and from out here those are the same thing.
func checkOrphan(l *ledger) []complaint {
	proven := map[string]bool{}
	for _, p := range l.proofs {
		named, _ := names(l.pop, p)
		for site := range named {
			proven[site] = true
		}
	}
	var out []complaint
	for _, a := range l.pop {
		if a.kind != contract || proven[a.site] {
			continue
		}
		out = append(out, complaint{ruleOrphan, a.site,
			fmt.Sprintf("%s %q has no fault behind it: no proof names it, so either nothing can reach it or its deletion would go unnoticed", a.call, a.says)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].at < out[j].at })
	return out
}

// checkStrandedFault is the inverse closure, and it is what makes a demotion
// impossible to hide.
//
// The orphan rule reads one way round: every assertion must have a fault behind
// it. Nothing read the other way, and the other way is where the last defect
// went. Measured: turning `require.NotEmpty(t, h, "an empty handle is a key
// nothing can ever remove")` into a step and deleting the four lines of the
// proof that named it left the tree at exit 0, and then replacing the step's
// condition with a constant left it at exit 0 again — a contract fully dead,
// with fake.FaultDeployKeyHandleIsEmpty still in the fake forge, still
// answering an installation with an empty handle, and nothing at all asking it
// to.
//
// Deleting the proof ALONE was caught, because the assertion orphaned. The
// DEMOTION is what laundered it: a step needs no proof, so the assertion
// stopped needing one, and the fault was left with nobody to notice. A fault
// that no proof injects is either a mistake nothing checks for or a proof
// somebody deleted, and from out here those are the same thing.
func checkStrandedFault(l *ledger) []complaint {
	named := map[string]bool{}
	for _, p := range l.proofs {
		for _, f := range p.faults {
			named[string(f)] = true
		}
	}
	var out []complaint
	for value, ident := range l.src.faults {
		if named[value] {
			continue
		}
		out = append(out, complaint{ruleStrandedFault, "fake." + ident,
			"no proof injects this fault, so the mistake it makes a driver commit is one nothing in this suite is shown to catch; either a proof was deleted, or the assertion it proved was demoted out from under it"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].at < out[j].at })
	return out
}

// filterBy keeps the complaints one rule made, so a rule is proven by its own
// case and not by whatever its neighbour said.
func filterBy(only string, said []complaint) []complaint {
	var out []complaint
	for _, c := range said {
		if c.rule == only {
			out = append(out, c)
		}
	}
	return out
}

// ───────────────────── naming, and the two lists that do it ─────────────────────

// names maps a proof's sayings onto the assertions they name, and refuses a
// saying that names anything but one contract assertion. A proof that can name
// a step is a proof saying that step IS reachable by a broken subject, which is
// the one thing a step claims it is not.
func names(pop []assertion, p proof) (map[string]bool, []complaint) {
	out := make(map[string]bool, len(p.says))
	var said []complaint
	for _, saying := range p.says {
		hits := sayingsMatching(pop, saying)
		switch len(hits) {
		case 1:
			if hits[0].kind != contract {
				said = append(said, complaint{ruleSayingNamesAStep, hits[0].site,
					fmt.Sprintf("proof %q says %q, which is %s — a driver fault can kill it, so it is a claim about the driver and must be spelled as one",
						p.name, saying, hits[0].kind)})
				continue
			}
			out[hits[0].site] = true
		case 0:
			said = append(said, complaint{ruleUnknownSaying, p.name,
				fmt.Sprintf("this proof says %q, which no assertion in the suite says", saying)})
		default:
			said = append(said, complaint{ruleAmbiguousSaying, p.name,
				fmt.Sprintf("this proof says %q, which %d assertions say: %v — a saying must name one assertion or it proves whichever happens to fire",
					saying, len(hits), sitesOf(hits))})
		}
	}
	return out, said
}

// stops maps a proof's stops onto the STEPS this subject prevents the suite
// from taking, and refuses one that names a claim. A step and a claim are told
// apart here and nowhere else, so the two lists cannot quietly become one.
func stops(pop []assertion, p proof) (map[string]bool, []complaint) {
	out := make(map[string]bool, len(p.stops))
	var said []complaint
	for _, saying := range p.stops {
		hits := sayingsMatching(pop, saying)
		switch len(hits) {
		case 1:
			if hits[0].kind == contract {
				said = append(said, complaint{ruleStopNamesAClaim, hits[0].site,
					fmt.Sprintf("proof %q stops %q, which is a claim about the driver — a claim this subject makes the suite make belongs in says, where it counts as proof",
						p.name, saying)})
				continue
			}
			out[hits[0].site] = true
		case 0:
			said = append(said, complaint{ruleUnknownStop, p.name,
				fmt.Sprintf("this proof stops %q, which no step in the suite says", saying)})
		default:
			said = append(said, complaint{ruleAmbiguousStop, p.name,
				fmt.Sprintf("this proof stops %q, which %d places say: %v", saying, len(hits), sitesOf(hits))})
		}
	}
	return out, said
}

func sayingsMatching(pop []assertion, saying string) []assertion {
	var hits []assertion
	for _, a := range pop {
		if a.says != "" && strings.Contains(a.says, saying) {
			hits = append(hits, a)
		}
	}
	return hits
}

func sitesOf(hits []assertion) []string {
	var out []string
	for _, h := range hits {
		out = append(out, h.site)
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

type stray struct{ site, fn string }

// stepsGoingNowhere is every marker call with nothing after it. "After" means,
// in the function it is written in: a real assertion, or a call to something in
// this package that makes one. A function that RETURNS a value is on its way by
// construction — the question is its caller's.
func stepsGoingNowhere(src *source) []stray {
	// The functions and the closures whose body makes a claim of its own. They
	// are held by OBJECT and not by name: two closures called reject in two
	// files are two objects, and a method and a function sharing a name are two
	// objects as well.
	asserting := map[types.Object]bool{}
	for _, file := range src.pkg.Syntax {
		ast.Inspect(file, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.FuncDecl:
				if src.isMarkerDecl(v) {
					return false
				}
				if src.asserts(v.Body) {
					if obj := src.pkg.TypesInfo.Defs[v.Name]; obj != nil {
						asserting[obj] = true
					}
				}
			case *ast.AssignStmt:
				// And the closures a function binds to a name and then calls,
				// which is how this suite spells "every wrong delivery, one
				// line each".
				if len(v.Lhs) != 1 || len(v.Rhs) != 1 {
					return true
				}
				lit, ok := v.Rhs[0].(*ast.FuncLit)
				if !ok {
					return true
				}
				id, ok := v.Lhs[0].(*ast.Ident)
				if !ok || !src.asserts(lit.Body) {
					return true
				}
				for _, obj := range []types.Object{src.pkg.TypesInfo.Defs[id], src.pkg.TypesInfo.Uses[id]} {
					if obj != nil {
						asserting[obj] = true
					}
				}
			}
			return true
		})
	}

	var out []stray
	for _, file := range src.pkg.Syntax {
		ast.Inspect(file, func(n ast.Node) bool {
			var body *ast.BlockStmt
			var results *ast.FieldList
			label := "a function literal"
			switch fn := n.(type) {
			case *ast.FuncDecl:
				if src.isMarkerDecl(fn) {
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
				if f, ok := src.howItFails(call); ok {
					if f.kind == plumbing {
						steps = append(steps, call.Pos())
					} else {
						questions = append(questions, call.Pos())
					}
					continue
				}
				if obj := src.called(call); obj != nil && asserting[obj] {
					questions = append(questions, call.Pos())
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
					out = append(out, stray{site: src.site(at), fn: label})
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
func callsDirectlyIn(body *ast.BlockStmt) []*ast.CallExpr {
	var out []*ast.CallExpr
	ast.Inspect(body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.CallExpr:
			out = append(out, v)
		}
		return true
	})
	return out
}
