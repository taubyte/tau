package forgetest

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/taubyte/tau/pkg/v2/forge/fake"
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
// Twenty rules, twenty faults. Hollowed one at a time, all twenty fail here.
//
// THREE THINGS OUTSIDE THE RULES ARE HELD TOO, because each of them could
// disable every rule at once and no rule can see it happen from inside the
// registry:
//
//	the REPORT. obey is the one path a complaint travels, and the self-test
//	travels it: a loop that hears a rule and drops what it says is hollowing,
//	one level further out.
//
//	the REGISTRY. A rule not in it does not run, so an emptied registry is
//	every rule turned off and nothing left to notice.
//	TestEveryRuleTheLedgerDeclaresIsRead asks from outside it, over the rule
//	names read out of this file's own source with go/types — the same closure
//	the faults get. Emptying the registry, or dropping one entry, fails there.
//
//	the LOADER. If the source cannot be type-checked the ledger fails loudly
//	rather than computing its rules over a smaller population.
//
// WHAT IT STILL DOES NOT COVER, said plainly: deleting a rule OUTRIGHT — its
// name, its entry, its check and its violation, in one hunk — leaves nothing
// declared for the registry check to miss. That is the same exposure as
// deleting a test, and it is four coordinated deletions rather than the one
// character that used to do it.

// complaint is one thing the ledger has to say. It carries the rule that said
// it so that a rule proven by the self-test is proven under its own name and
// not under whichever rule happened to fire.
type complaint struct {
	rule ruleName
	at   string
	text string
}

// ruleName is what a rule is called. It is a TYPE so that the ledger can read
// the rules it declares out of its own source, the way it reads the faults out
// of the fake forge's — a registry is a list, and a list is a thing somebody
// can shorten without anything noticing.
type ruleName string

func (c complaint) String() string { return c.at + ": " + c.text }

// The rules, by name. A name is what a failure is reported under and what the
// self-test proves, so they are written once.
const (
	ruleSaysNothing        ruleName = "an assertion that says nothing"
	ruleSayingsCollide     ruleName = "a saying some other assertion also says"
	ruleStepGoesNowhere    ruleName = "a step on its way to nothing"
	ruleEmptyPopulation    ruleName = "a population with nothing in it"
	ruleUnknownSaying      ruleName = "a proof naming words no assertion says"
	ruleAmbiguousSaying    ruleName = "a proof naming words several assertions say"
	ruleSayingNamesAStep   ruleName = "a proof naming a step as though it were a claim"
	ruleUnknownStop        ruleName = "a proof stopping words no step says"
	ruleAmbiguousStop      ruleName = "a proof stopping words several places say"
	ruleStopNamesAClaim    ruleName = "a proof stopping a claim"
	ruleStopIsTheComplaint ruleName = "a stop in the function the proof is about"
	ruleProofPassed        ruleName = "a proof the suite does not fail"
	ruleFailureFromNowhere ruleName = "a failure from outside the population"
	ruleUnplaceable        ruleName = "a failure no single assertion claims"
	ruleFiredUnnamed       ruleName = "an assertion that fires unnamed"
	ruleStopDidNotStop     ruleName = "a stop that is not a capability refusal"
	ruleNamedDidNotFire    ruleName = "an assertion named and not firing"
	ruleProofProvesNothing ruleName = "a proof that complains about nothing"
	ruleOrphan             ruleName = "a contract assertion with no fault behind it"
	ruleStrandedFault      ruleName = "a fault with no proof in front of it"
	ruleFaultDoesNothing   ruleName = "a fault the proof does not need"
	ruleUnwalkedBody       ruleName = "a body nothing walks"
)

// ledger is everything the rules are read over: the suite's own source, the
// proofs, and what the suite SAID about each of them. A rule is a function of
// this and nothing else, which is what lets a violating one be handed over.
type ledger struct {
	src    *source
	pop    []assertion
	proofs []proof
	said   map[string]*recorder

	// unwalked is every body in the suite that the walk does not enter, by the
	// site it is written at. It is empty by construction, and it is computed
	// and carried anyway: "by construction" is a claim about code somebody will
	// edit, and an assertion in a body nothing walks is in no population and no
	// rule below is computed over it.
	unwalked []string

	// without is what the suite says about a proof's subject when ONE of its
	// faults is taken away and everything else is left alone. It is the
	// counterfactual the causal rule is read over, and it is recorded here
	// rather than computed inside a rule because a rule is a function of the
	// ledger and of nothing else.
	without map[string]map[fake.Fault]*recorder
}

// rule is a check and the faults behind it.
//
// violates is a LIST because a rule can be evaded in more than one shape, and a
// shape nothing is shown is a shape nothing holds against. The walk was
// measured going round four of them at once — a literal bound by a var, a claim
// written inside a marker, a failure handed over without its reporter, and
// testify given a shorter name — and each is a case of the orphan rule, not a
// paraphrase of the others.
type rule struct {
	name     ruleName
	check    func(*ledger) []complaint
	violates []func(*testing.T) *ledger
}

// breaks is the ledgers that break a rule, written where the rule is declared.
func breaks(fs ...func(*testing.T) *ledger) []func(*testing.T) *ledger { return fs }

// theShapeOf is what a violation is reported under: its own identifier, read
// off the function value. A violation added to a rule names itself in the
// output, so nothing has to keep a label beside it in step.
func theShapeOf(v func(*testing.T) *ledger) string {
	name := runtime.FuncForPC(reflect.ValueOf(v).Pointer()).Name()
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	return strings.TrimSuffix(strings.TrimPrefix(name, "violate"), "-fm")
}

func ledgerRules() []rule {
	return []rule{
		{ruleSaysNothing, checkSaysNothing, breaks(violateSaysNothing)},
		{ruleSayingsCollide, checkSayingsCollide, breaks(violateSayingsCollide)},
		{ruleStepGoesNowhere, checkStepGoesNowhere, breaks(violateStepGoesNowhere)},
		{ruleEmptyPopulation, checkEmptyPopulation, breaks(violateEmptyPopulation)},
		{ruleUnknownSaying, checkNames(ruleUnknownSaying), breaks(violateUnknownSaying)},
		{ruleAmbiguousSaying, checkNames(ruleAmbiguousSaying), breaks(violateAmbiguousSaying)},
		{ruleSayingNamesAStep, checkNames(ruleSayingNamesAStep), breaks(violateSayingNamesAStep)},
		{ruleUnknownStop, checkStops(ruleUnknownStop), breaks(violateUnknownStop)},
		{ruleAmbiguousStop, checkStops(ruleAmbiguousStop), breaks(violateAmbiguousStop)},
		{ruleStopNamesAClaim, checkStops(ruleStopNamesAClaim), breaks(violateStopNamesAClaim)},
		{ruleStopIsTheComplaint, checkCollateral, breaks(violateStopIsTheComplaint)},
		{ruleProofPassed, checkRun(ruleProofPassed), breaks(violateProofPassed)},
		{ruleFailureFromNowhere, checkRun(ruleFailureFromNowhere), breaks(violateFailureFromNowhere)},
		{ruleUnplaceable, checkRun(ruleUnplaceable), breaks(violateUnplaceable)},
		{ruleFiredUnnamed, checkRun(ruleFiredUnnamed), breaks(violateFiredUnnamed)},
		{ruleStopDidNotStop, checkRun(ruleStopDidNotStop), breaks(violateStopDidNotStop)},
		{ruleNamedDidNotFire, checkRun(ruleNamedDidNotFire), breaks(violateNamedDidNotFire)},
		{ruleProofProvesNothing, checkProofProvesNothing, breaks(violateProofProvesNothing)},
		{ruleOrphan, checkOrphan, breaks(violateOrphan,
			violateOrphanInATopLevelLiteral, violateOrphanInAMarker,
			violateOrphanThroughAMethodValue, violateOrphanThroughAName)},
		{ruleStrandedFault, checkStrandedFault, breaks(violateStrandedFault)},
		{ruleFaultDoesNothing, checkFaultDoesNothing, breaks(violateFaultDoesNothing)},
		{ruleUnwalkedBody, checkUnwalkedBody, breaks(violateUnwalkedBody)},
	}
}

// obey reads the rules over a ledger and reports what they have to say. It is
// the ONE path a failure travels, and the self-test below travels it too: a
// rule that complains into a loop which drops what it says is a rule nobody
// hears, and that is hollowing one level further out than hollowing the rule.
func obey(t interface{ Errorf(string, ...any) }, l *ledger, rules []rule) int {
	said := 0
	for _, r := range rules {
		for _, c := range r.check(l) {
			t.Errorf("%s: %s", c.rule, c)
			said++
		}
	}
	return said
}

// TestTheSuiteObeysTheLedger is the ledger read over the suite as it stands.
func TestTheSuiteObeysTheLedger(t *testing.T) {
	l := theLedger(t)
	counts := map[kind]int{}
	for _, a := range l.pop {
		counts[a.kind]++
	}
	t.Logf("%d cases: %d contract, %d plumbing", len(l.pop), counts[contract], counts[plumbing])
	t.Logf("%d proofs against %d faults the fake forge can be told to have", len(l.proofs), len(l.src.faults))
	for _, r := range ledgerRules() {
		t.Run(string(r.name), func(t *testing.T) { obey(t, l, []rule{r}) })
	}
}

// TestTheLedgerObeysTheRuleItImposes is the other direction, and it is the one
// that was missing. Each rule is handed a ledger that breaks it and must say
// so, under its own name, THROUGH the loop that reports it. A rule that has
// been hollowed says nothing and fails here; a rule added with no violation
// behind it fails here too; and so does a reporting loop that hears a rule and
// drops it.
func TestTheLedgerObeysTheRuleItImposes(t *testing.T) {
	proveEveryRule(t, nil, ledgerRules())
}

// proveEveryRule hands each rule every ledger that breaks it and requires the
// rule to say so, under its own name, THROUGH the loop that reports it.
//
// It is a function of the rules it is given so that the thing RUNNING them can
// itself be handed a rule that breaks it — see the harnesses below. A rule that
// has been hollowed says nothing and fails here; a rule declared with no
// violation behind it fails here too; and so does a reporting loop that hears a
// rule and drops what it says.
func proveEveryRule(t *testing.T, report reporter, rules []rule) {
	for _, r := range rules {
		t.Run(string(r.name), func(t *testing.T) {
			to := reportTo(report, t)
			if len(r.violates) == 0 {
				to.Errorf("%s has no ledger that breaks it, so nothing knows whether it still holds — which is exactly what it accuses an assertion of", r.name)
				return
			}
			for _, violate := range r.violates {
				t.Run(theShapeOf(violate), func(t *testing.T) {
					to := reportTo(report, t)
					broken := violate(t)
					for _, c := range r.check(broken) {
						if c.rule != r.name {
							to.Errorf("a ledger that breaks %q was answered under %q instead: %s", r.name, c.rule, c)
						}
					}
					heard := &recorder{}
					if n := obey(heard, broken, []rule{r}); n == 0 || !heard.failed {
						to.Errorf("%s was handed a ledger that breaks it and %d complaints reached the report (failed=%v): it is decoration, and hollowing it — or the loop that reports it — would go unnoticed",
							r.name, n, heard.failed)
					}
				})
			}
		})
	}
}

// reporter is where a complaint goes. The three things that run the rules take
// one so that each can be run against a recorder over material that breaks it —
// a *testing.T when they are held for real, a recorder when they are the thing
// being held.
type reporter interface{ Errorf(string, ...any) }

// reportTo is the subtest's own t unless somebody is listening instead.
func reportTo(report reporter, t *testing.T) reporter {
	if report == nil {
		return t
	}
	return report
}

// TestEveryRuleTheLedgerDeclaresIsRead closes the registry, which is the last
// list in this file that a hand maintains. A rule constant no entry carries is
// a rule nothing runs, and an empty registry is every rule at once — neither is
// visible from inside the registry, because a rule that is not in it does not
// run. So this is asked from outside it, over the rule names read out of this
// file's own source.
func TestEveryRuleTheLedgerDeclaresIsRead(t *testing.T) {
	src := suiteSource(t)
	t.Run("as it stands", func(t *testing.T) {
		for _, name := range unreadRules(src, ledgerRules()) {
			t.Errorf("%s is declared and no registry entry reads it, so nothing computes it over anything", name)
		}
	})
	t.Run("one more rule declared than the registry reads", func(t *testing.T) {
		broken := src.with(t, "ledger_fault_unread_rule_test.go", `package forgetest

const ruleNobodyReads ruleName = "a rule the registry never looks at"
`)
		if len(unreadRules(broken, ledgerRules())) == 0 {
			t.Fatalf("a rule declared and left out of the registry went unnoticed, so shortening the registry — or emptying it — would too")
		}
	})
}

// unreadRules is every rule name this file declares that no registry entry
// carries, by the identifier it is declared under.
func unreadRules(src *source, rules []rule) []string {
	read := map[string]bool{}
	for _, r := range rules {
		read[string(r.name)] = true
	}
	var out []string
	for value, ident := range src.rules {
		if !read[value] {
			out = append(out, ident)
		}
	}
	sort.Strings(out)
	return out
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
			src:     suiteSource(t),
			proofs:  allProofs(),
			said:    map[string]*recorder{},
			without: map[string]map[fake.Fault]*recorder{},
		}
		l.pop = populationOf(l.src)
		l.unwalked = l.src.unwalkedBodies(l.src.walk())
		for _, p := range l.proofs {
			l.said[p.name] = record(p.subject(t, p.faults))
			if len(p.faults) == 0 {
				continue
			}
			// And the same subject once more for each fault, without it. This
			// is the whole of the causal rule's material: what the suite says
			// when one named mistake is put back.
			short := map[fake.Fault]*recorder{}
			for _, f := range p.faults {
				short[f] = record(p.subject(t, dropping(p.faults, f)))
			}
			l.without[p.name] = short
		}
		theOne = l
	})
	if theOne == nil {
		t.Fatalf("the ledger could not be built, and the first test to try said why")
	}
	return theOne
}

// ─────────────────────────────── the rules ───────────────────────────────

// checkSaysNothing. An assertion with no message cannot be named by a proof,
// so nothing can know it is load-bearing.
func checkSaysNothing(l *ledger) []complaint {
	var out []complaint
	for _, a := range l.pop {
		if a.says == "" {
			out = append(out, complaint{ruleSaysNothing, a.id,
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
				out = append(out, complaint{ruleSayingsCollide, a.id,
					fmt.Sprintf("%q is contained in what %s says, %q, so a proof naming the first would be answered by the second and neither can be named alone",
						a.says, b.id, b.says)})
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
func checkNames(only ruleName) func(*ledger) []complaint {
	return func(l *ledger) []complaint {
		var out []complaint
		for _, p := range l.proofs {
			_, said := names(l.pop, p)
			out = append(out, filterBy(only, said)...)
		}
		return out
	}
}

func checkStops(only ruleName) func(*ledger) []complaint {
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
func checkRun(only ruleName) func(*ledger) []complaint {
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
		for id := range named {
			proven[id] = true
		}
	}
	var out []complaint
	for _, a := range l.pop {
		if a.kind != contract || proven[a.id] {
			continue
		}
		out = append(out, complaint{ruleOrphan, a.id,
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

// checkUnwalkedBody. Go holds a body in exactly two places — a declaration and
// a literal — and every one of them has to be inside something this file walks,
// or the assertions written in it are invisible: not exempt, not counted, not
// anywhere, and no rule below computed over them. Four shapes were measured
// doing exactly that, and the fix for each of them was to state where a body
// can be rather than to add one more place to a list. This is what says the
// statement still holds.
func checkUnwalkedBody(l *ledger) []complaint {
	var out []complaint
	for _, site := range l.unwalked {
		out = append(out, complaint{ruleUnwalkedBody, site,
			"nothing walks this body, so an assertion written in it is in no population, needs no message, no proof and no fault, and nothing anywhere says so"})
	}
	return out
}

// checkFaultDoesNothing is the fault closure made CAUSAL, and causation is the
// half a list could never be.
//
// The stranded-fault rule reads the population of faults and requires each one
// to appear in SOME proof's faults. Appearing in a list is not evidence of
// anything, and the last defect went straight through the middle of it.
// Measured, three edits: delete the assertion that the installed deploy key is
// read-only, delete the two-line proof that named it, and append its fault to
// the neighbouring proof's list. Every fault still named, every rule still
// quiet, gofmt clean, vet clean, GO_TEST_EXIT=0 — a driver that installs a
// WRITABLE deploy key passing the whole conformance suite, with the only trace
// an unasserted census line moving 183 to 182.
//
// So the question is not whether a fault is NAMED but whether it is WHY. Build
// the same subject without it, run the suite again, and require at least one of
// the complaints this proof names to stop being made. A fault whose absence the
// suite cannot tell proves nothing about anything the proof names, and the
// assertion it was written for has nothing behind it any more.
//
// It is asked of each fault ON ITS OWN, over a subject that still has the
// others, so a proof that honestly needs two faults — a redirect that has to be
// followed before the record it answers with can be wrong — holds: take either
// away and a complaint the proof names goes with it.
func checkFaultDoesNothing(l *ledger) []complaint {
	var out []complaint
	for _, p := range l.proofs {
		if len(p.faults) == 0 {
			continue
		}
		named, _ := names(l.pop, p)
		with := map[string][]string{}
		if rec := l.said[p.name]; rec != nil {
			with, _ = attribute(l.pop, rec.msgs)
		}
		for _, f := range distinctFaults(p.faults) {
			rec, ran := l.without[p.name][f]
			if !ran || rec == nil {
				out = append(out, complaint{ruleFaultDoesNothing, p.name,
					fmt.Sprintf("nothing ran this proof's subject without %s, so whether that fault is why the suite complains was never asked: a fault nothing has been shown to cause is a fault in a list", f)})
				continue
			}
			without, _ := attribute(l.pop, rec.msgs)
			needed := false
			for id := range named {
				if len(with[id]) > 0 && len(without[id]) == 0 {
					needed = true
				}
			}
			if !needed {
				out = append(out, complaint{ruleFaultDoesNothing, p.name,
					fmt.Sprintf("taking %s away leaves every complaint this proof names still being made, so nothing this proof proves is that fault's doing: the fault is in a list and behind no assertion, and whatever it was written for can be deleted with nothing noticing", f)})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].text < out[j].text })
	return out
}

// dropping is a fault list with one fault taken out of it, which is the only
// difference between the two runs the rule above compares. It removes the fault
// by VALUE and not by position: a list naming the same fault twice names one
// mistake, and a subject built without it must not still have it.
func dropping(faults []fake.Fault, drop fake.Fault) []fake.Fault {
	var out []fake.Fault
	for _, f := range faults {
		if f != drop {
			out = append(out, f)
		}
	}
	return out
}

// distinctFaults is the mistakes a proof injects, each asked about once.
func distinctFaults(faults []fake.Fault) []fake.Fault {
	seen := map[fake.Fault]bool{}
	var out []fake.Fault
	for _, f := range faults {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// filterBy keeps the complaints one rule made, so a rule is proven by its own
// case and not by whatever its neighbour said.
func filterBy(only ruleName, said []complaint) []complaint {
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
				said = append(said, complaint{ruleSayingNamesAStep, hits[0].id,
					fmt.Sprintf("proof %q says %q, which is %s — a driver fault can kill it, so it is a claim about the driver and must be spelled as one",
						p.name, saying, hits[0].kind)})
				continue
			}
			out[hits[0].id] = true
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
				said = append(said, complaint{ruleStopNamesAClaim, hits[0].id,
					fmt.Sprintf("proof %q stops %q, which is a claim about the driver — a claim this subject makes the suite make belongs in says, where it counts as proof",
						p.name, saying)})
				continue
			}
			out[hits[0].id] = true
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
		out = append(out, h.id)
	}
	return out
}

// fnOf is the function an assertion is written in, which is how a stop that is
// collateral is told from one that is the proof's own complaint.
func fnOf(pop []assertion, id string) string {
	for _, a := range pop {
		if a.id == id {
			return a.in
		}
	}
	return "?"
}

func sayingAt(pop []assertion, id string) string {
	for _, a := range pop {
		if a.id == id {
			return a.kind.String() + " " + a.call + " " + strconv.Quote(a.says)
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
