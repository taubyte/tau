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
// Twenty-two rules and their faults, and a rule may have more than one: a rule
// evaded in four shapes needs four, or the three nobody showed it are three it
// does not hold against. Hollowed one at a time, all of them fail here.
//
// AND THE THINGS THAT RUN THE RULES ARE HELD THE SAME WAY, because each of them
// could disable every rule at once and no rule can see it happen from inside
// the registry. Round six closed the rules and left these open; measured, each
// could be hollowed on its own with the whole forge tree at exit 0. So each is
// a HARNESS with a registry that breaks it, in the shape a rule has:
//
//	the REPORT. obey is the one path a complaint travels, and a loop that
//	hears a rule and drops what it says is hollowing one level further out.
//	Handed a rule that complains about every ledger there is, it must complain.
//
//	the SELF-TEST. Handed a rule that is decoration — one that says nothing
//	about the ledger it is given — it must say so, which is the whole of what
//	it exists for.
//
//	the REGISTRY. A rule not in it does not run, so an emptied registry is
//	every rule turned off and nothing left to notice. It is asked from outside
//	it, over the rule names read out of this file's own source with go/types —
//	the same closure the faults get — and handed a registry one entry short, it
//	must name the entry that went.
//
//	the LIST OF HARNESSES, closed the way the registry is: the names are read
//	out of source, so dropping one, or emptying the list, is visible from
//	outside it.
//
//	the LOADER. If the source cannot be type-checked the ledger fails loudly
//	rather than computing its rules over a smaller population.
//
// WHAT IT STILL DOES NOT COVER, said plainly. Deleting a rule OUTRIGHT — its
// name, its entry, its check and its violation, in one hunk — leaves nothing
// declared for the registry check to miss. That is the same exposure as
// deleting a test, and it is four coordinated deletions rather than the one
// character that used to do it. And the regress stops at hold and
// TestTheLedgerHolds below; the package doc in subject.go names the twelve
// lines, says why that set cannot be smaller, and says what the equivalent
// exposure is elsewhere.

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
	ruleFailurePutDown     ruleName = "a way to fail put where nothing follows it"
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
		{ruleFailurePutDown, checkFailurePutDown, breaks(violateFailurePutDown)},
	}
}

// obey reads the rules over a ledger and reports what they have to say. It is
// the ONE path a failure travels, and the self-test below travels it too: a
// rule that complains into a loop which drops what it says is a rule nobody
// hears, and that is hollowing one level further out than hollowing the rule.
func obey(t reporter, l *ledger, rules []rule) int {
	said := 0
	for _, r := range rules {
		for _, c := range r.check(l) {
			t.Errorf("%s: %s", c.rule, c)
			said++
		}
	}
	return said
}

// ──────────── the three things that run the rules, and their faults ────────────
//
// EVERY RULE HAD A FAULT BEHIND IT AND THE LOOPS THAT EXECUTE THEM HAD NONE.
//
// Round six made a rule a value with a violating ledger, and left the three
// things that run those rules exactly as exposed as the rules had been.
// Measured, one at a time: the report's loop emptied to `_ = r`, the
// self-test's condition switched off with `false &&`, and the registry check
// reading `unreadRules(src, nil)` — three separate hollowings, three times
// GO_TEST_EXIT=0 over the whole forge tree. A rule proven by a loop that does
// not run it is proven by nothing, and no rule can see that from inside the
// registry.
//
// So each of the three is a HARNESS now, in the same shape a rule is: a name,
// a run that is a function of the rules it is given, and a registry that breaks
// it.
//
// AND THERE IS ONE CALL THAT RUNS ONE, because two of them was the last
// off-switch. Round seven ran every harness twice from two places: the real one
// passed nil and the rules as they stand, the self-test passed a recorder and a
// registry that breaks it. The self-test proved the path IT used and the suite
// hung off the other one, so `nil` written as `&recorder{}` — one token, gofmt
// clean, vet clean — sent every complaint from all twenty-two rules over the
// real suite into a recorder nobody reads. Measured with the
// LastDeployKeyReadOnly contract deleted from optional.go: GO_TEST_EXIT=0 over
// the whole forge tree, where that deletion alone is GO_TEST_EXIT=1, the census
// still printing "183 cases", all twenty-two named subtests still green.
// `ledgerHarnesses()[1:]` did the same, and nothing noticed either: the checks
// that existed asked whether every declared name was CARRIED BY the list, which
// stays true when the loop goes round a subset of it.
//
// So there is ONE call now — hold — and it is the only place a harness is ever
// run and the only place what a harness said is read. It takes no reporter: the
// listener is made inside it and is always compared against what the registry
// it was handed must come to. Both directions go through it, so the call the
// suite is held by IS the call the self-test exercises. And what actually ran
// is written down INSIDE that call and read back against the harness names in
// source, so a loop round a subset — or round nothing — is named rather than
// silent.

// harnessName is what one of the three is called, and it is a TYPE for the same
// reason ruleName is: a list is a thing somebody can shorten.
type harnessName string

const (
	harnessTheSuiteObeysTheLedger    harnessName = "the suite obeys the ledger"
	harnessTheLedgerObeysItsOwnRules harnessName = "the ledger obeys the rule it imposes"
	harnessEveryRuleDeclaredIsRead   harnessName = "every rule the ledger declares is read"
)

// harness is one of the three, and the registry that breaks it.
type harness struct {
	name harnessName
	// run reads a set of rules and says what they have to say to the listener
	// it is handed — never to t, which is why the type is the recorder and not
	// an interface with a nil in it. A harness that reported straight to t
	// could only ever be run in the direction that must be silent, and that is
	// what made two call sites necessary in the first place.
	run func(t *testing.T, heard *recorder, rules []rule)
	// breaks is a registry this harness must complain about — its fault, in
	// the same sense a rule's violating ledger is one.
	breaks func(t *testing.T) []rule
}

func ledgerHarnesses() []harness {
	return []harness{
		{harnessTheSuiteObeysTheLedger, readTheLedgerOverTheSuite, aRuleWithSomethingToSayAboutEverything},
		{harnessTheLedgerObeysItsOwnRules, proveEveryRule, aRuleThatIsDecoration},
		{harnessEveryRuleDeclaredIsRead, readEveryRuleDeclared, aRegistryOneShort},
	}
}

// ───────────────────── the one call a harness is run by ─────────────────────

// registry is a set of rules AND what running a harness over them must come to.
// There are two answers and there is no third, and which one applies is a
// property of the MATERIAL and not of the plumbing: over the rules as they
// stand a harness must be silent, over a registry that breaks it a harness must
// complain.
//
// Neither answer can be written the other way round without a named failure,
// because each is the other's control. The real rules under "must complain" say
// nothing and fail; a registry that breaks a harness under "must be silent"
// complains and fails. That is what makes it safe for the direction to be data.
type registry struct {
	rules     []rule
	complains bool
}

// theRulesAsTheyStand is the registry the whole suite is actually held by.
//
// Hollowing it to nothing does not pass: readEveryRuleDeclared is one of the
// harnesses, and over an empty set of rules it names every rule this file
// declares, which is a complaint in the direction that must be silent.
func theRulesAsTheyStand() registry {
	return registry{ledgerRules(), false}
}

// whatBreaksIt is the harness's own fault, in the shape a rule's violating
// ledger is one.
func whatBreaksIt(t *testing.T, h harness) registry {
	return registry{h.breaks(t), true}
}

// hold runs one harness over one registry. IT IS THE ONLY PLACE A HARNESS IS
// EVER RUN, and the only place what a harness said is read.
//
// It takes no reporter. The listener is made here and is compared, here,
// against what the registry it was handed must come to — so "discard" is not
// something this file can express: there is no argument to write it as, and the
// only listener there is gets read two lines after it is made. The call the
// suite is held by is this one; the call the self-test exercises is this one;
// proving either proves both.
func hold(t *testing.T, ran *census, h harness, over registry) {
	ran.write(heldRun{h.name, over.complains})
	heard := &recorder{}
	h.run(t, heard, over.rules)
	if heard.failed != over.complains {
		t.Errorf("%s", over.wrong(h, heard))
	}
}

// wrong is what a harness that came out the other way is reported as.
//
// It is deliberately NOT load-bearing and it is a separate function for exactly
// that reason: Errorf fails the test whatever string it is handed, so a
// hollowed message is still a red test, and the lines that must be correct stay
// the five statements above and no more.
func (over registry) wrong(h harness, heard *recorder) string {
	if over.complains {
		return fmt.Sprintf("%q was run %s and said nothing: it is a loop that does not run what it is given, and hollowing it turns every rule off at once with no rule able to see it happen",
			h.name, theDirection(over.complains))
	}
	return fmt.Sprintf("%q %s said:\n%s", h.name, theDirection(over.complains), heard)
}

// theDirection names one of the two ways a harness is run. It is written once
// and read by both the failure and the census, so a run named in a complaint
// and the same run named as never having happened are spelled the same.
func theDirection(complains bool) string {
	if complains {
		return "over a registry that breaks it"
	}
	return "over the rules as they stand"
}

// heldRun is one harness run one way. It is the unit the census counts, because
// a harness held in one direction only is a harness whose other direction is
// off — and dropping one of the two calls in TestTheLedgerHolds is exactly as
// cheap as dropping an entry from the list used to be.
type heldRun struct {
	name      harnessName
	complains bool
}

// census is what actually ran, written down INSIDE hold and not by the loop
// that calls it.
//
// MEMBERSHIP WAS THE HOLE. unreadHarnesses asked whether every declared name
// was carried by an entry in the list — which stays true when the loop goes
// round `ledgerHarnesses()[1:]`, and stays true when a call reaches past hold
// to h.run with a listener nobody reads. This asks the other question: did this
// run actually hold it, both ways.
//
// IT COUNTS RATHER THAN TICKING, because a set can be filled in advance.
// Measured: four lines writing every declared harness into the census before
// the loop, plus `ledgerHarnesses()[1:]`, hid the deleted LastDeployKeyReadOnly
// contract at GO_TEST_EXIT=0. hold writes exactly once per call, so a run
// written down twice is a run somebody recorded without running — and the same
// count catches a list carrying one harness twice and another not at all, which
// a membership check calls complete.
type census struct {
	mu   sync.Mutex
	held map[heldRun]int
}

func (c *census) write(r heldRun) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.held == nil {
		c.held = map[heldRun]int{}
	}
	c.held[r]++
}

// unaccounted is every run this file declares that hold did not make exactly
// once, by the identifier the harness is declared under and the direction.
func (c *census) unaccounted(src *source) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for value, ident := range src.harnesses {
		for _, complains := range []bool{false, true} {
			switch n := c.held[heldRun{harnessName(value), complains}]; {
			case n == 0:
				out = append(out, fmt.Sprintf("%s %s never happened, so every rule that run would have read is off and nothing above was computed over anything it would have caught",
					ident, theDirection(complains)))
			case n > 1:
				out = append(out, fmt.Sprintf("%s %s was written down %d times and hold writes once per call, so a run was recorded that nothing ran — either the list carries this harness twice, or something wrote into the census by hand",
					ident, theDirection(complains), n))
			}
		}
	}
	sort.Strings(out)
	return out
}

// TestTheLedgerHolds is the whole invocation: every harness, both ways, through
// the one call.
//
// There is no second call site. The direction that must be silent and the
// direction that must complain are two calls to the same function, one line
// apart, so a listener nobody reads is not expressible — and the census below
// names a loop that went round a subset of the harnesses instead of letting it
// pass.
func TestTheLedgerHolds(t *testing.T) {
	ran := &census{}
	for _, h := range ledgerHarnesses() {
		t.Run(string(h.name), func(t *testing.T) {
			hold(t, ran, h, theRulesAsTheyStand())
			hold(t, ran, h, whatBreaksIt(t, h))
		})
	}
	for _, wrong := range ran.unaccounted(suiteSource(t)) {
		t.Errorf("%s", wrong)
	}
}

// TestTheCensusIsNotSatisfiedByAnythingButOneRunEach is the census's own fault,
// and it is why the loop above cannot be shortened or written into. A census
// that answers "nothing to report" whatever it is shown is the membership check
// all over again, so it is shown four things: a run of every declared harness
// both ways, which it must call complete; the same with one single run taken
// away, which it must name; the same with one run written down twice, which it
// must name, because that is what filling the census in advance looks like from
// here; and a source declaring a harness nothing ever ran, which it must name
// too.
func TestTheCensusIsNotSatisfiedByAnythingButOneRunEach(t *testing.T) {
	src := suiteSource(t)
	everyRun := func() *census {
		c := &census{}
		for value := range src.harnesses {
			for _, complains := range []bool{false, true} {
				c.write(heldRun{harnessName(value), complains})
			}
		}
		return c
	}
	if got := everyRun().unaccounted(src); len(got) > 0 {
		t.Fatalf("a census with every declared harness held both ways exactly once reported %v, so it names runs that did happen and nothing it says can be read", got)
	}
	for value, ident := range src.harnesses {
		for _, complains := range []bool{false, true} {
			odd := heldRun{harnessName(value), complains}

			short := &census{}
			for v := range src.harnesses {
				for _, c := range []bool{false, true} {
					if r := (heldRun{harnessName(v), c}); r != odd {
						short.write(r)
					}
				}
			}
			if len(short.unaccounted(src)) == 0 {
				t.Errorf("%s %s never ran and the census did not notice, so a loop round a subset of the harnesses — or round none of them, or one of the two calls deleted — would pass", ident, theDirection(complains))
			}

			twice := everyRun()
			twice.write(odd)
			if len(twice.unaccounted(src)) == 0 {
				t.Errorf("%s %s was written down twice and the census did not notice, so writing every run into it before the loop — and then going round a subset — would pass", ident, theDirection(complains))
			}
		}
	}
	broken := src.with(t, "ledger_fault_unheld_harness_test.go", `package forgetest

const harnessNobodyRuns harnessName = "a way of running the rules nothing ever runs"
`)
	if len(everyRun().unaccounted(broken)) == 0 {
		t.Fatalf("a harness declared and never run went unnoticed, so declaring one and leaving it out of the list would too")
	}
}

// readTheLedgerOverTheSuite is the ledger read over the suite as it stands.
func readTheLedgerOverTheSuite(t *testing.T, heard *recorder, rules []rule) {
	l := theLedger(t)
	counts := map[kind]int{}
	for _, a := range l.pop {
		counts[a.kind]++
	}
	t.Logf("%d cases: %d contract, %d plumbing", len(l.pop), counts[contract], counts[plumbing])
	t.Logf("%d proofs against %d faults the fake forge can be told to have", len(l.proofs), len(l.src.faults))
	for _, r := range rules {
		t.Run(string(r.name), func(t *testing.T) { obey(heard, l, []rule{r}) })
	}
}

// aRuleWithSomethingToSayAboutEverything is the report's fault: a registry of
// one rule that complains about any ledger at all. A report that runs no rule,
// or hears one and drops what it says, stays quiet here.
func aRuleWithSomethingToSayAboutEverything(*testing.T) []rule {
	name := ruleName("a rule with something to say about every ledger there is")
	return []rule{{name: name, check: func(*ledger) []complaint {
		return []complaint{{name, "the suite", "this rule complains about every ledger, and the report has to carry what it says"}}
	}}}
}

// aRuleThatIsDecoration is the self-test's fault: a rule that stays quiet about
// a ledger it was handed. Catching exactly this is what the self-test is for,
// so a self-test that does not run its rules passes it.
func aRuleThatIsDecoration(*testing.T) []rule {
	return []rule{{
		name:     ruleName("a rule that says nothing about the ledger that breaks it"),
		check:    func(*ledger) []complaint { return nil },
		violates: breaks(func(t *testing.T) *ledger { return theLedger(t) }),
	}}
}

// aRegistryOneShort is the registry check's fault: the real registry with an
// entry taken away, which is what shortening it — or emptying it — looks like
// from outside.
func aRegistryOneShort(t *testing.T) []rule {
	rules := ledgerRules()
	if len(rules) < 2 {
		t.Fatalf("the ledger declares fewer than two rules, so a registry one short of itself is not a registry")
	}
	return rules[1:]
}

// proveEveryRule hands each rule every ledger that breaks it and requires the
// rule to say so, under its own name, THROUGH the loop that reports it.
//
// It is a function of the rules it is given so that the thing RUNNING them can
// itself be handed a rule that breaks it — see the harnesses below. A rule that
// has been hollowed says nothing and fails here; a rule declared with no
// violation behind it fails here too; and so does a reporting loop that hears a
// rule and drops what it says.
func proveEveryRule(t *testing.T, heard *recorder, rules []rule) {
	for _, r := range rules {
		t.Run(string(r.name), func(t *testing.T) {
			if len(r.violates) == 0 {
				heard.Errorf("%s has no ledger that breaks it, so nothing knows whether it still holds — which is exactly what it accuses an assertion of", r.name)
				return
			}
			for _, violate := range r.violates {
				t.Run(theShapeOf(violate), func(t *testing.T) {
					broken := violate(t)
					for _, c := range r.check(broken) {
						if c.rule != r.name {
							heard.Errorf("%s: a ledger that breaks %q was answered under %q instead: %s", theShapeOf(violate), r.name, c.rule, c)
						}
					}
					said := &recorder{}
					if n := obey(said, broken, []rule{r}); n == 0 || !said.failed {
						heard.Errorf("%s was handed %s, a ledger that breaks it, and %d complaints reached the report (failed=%v): it is decoration, and hollowing it — or the loop that reports it — would go unnoticed",
							r.name, theShapeOf(violate), n, said.failed)
					}
				})
			}
		})
	}
}

// reporter is where a complaint goes: obey takes one so that a rule can be read
// over a ledger that breaks it and what it said can be looked at rather than
// ending the test.
//
// It used to be what a HARNESS took as well, with nil meaning "report to t" —
// which is how one token came to silence the whole suite. A harness now always
// says what it has to say to the recorder hold made for it, and hold always
// reads it; there is no nil and no choice of listener anywhere.
type reporter interface{ Errorf(string, ...any) }

// readEveryRuleDeclared closes the registry, which is the last list in this file
// that a hand maintains. A rule constant no entry carries is a rule nothing
// runs, and an empty registry is every rule at once — neither is visible from
// inside the registry, because a rule that is not in it does not run. So this is
// asked from outside it, over the rule names read out of this file's own source,
// and it is asked of the registry it is HANDED so that a registry one entry
// short is a thing it can be shown.
func readEveryRuleDeclared(t *testing.T, heard *recorder, rules []rule) {
	src := suiteSource(t)
	t.Run("as it stands", func(t *testing.T) {
		for _, name := range unreadRules(src, rules) {
			heard.Errorf("%s is declared and no registry entry reads it, so nothing computes it over anything", name)
		}
	})
	t.Run("one more rule declared than the registry reads", func(t *testing.T) {
		broken := src.with(t, "ledger_fault_unread_rule_test.go", `package forgetest

const ruleNobodyReads ruleName = "a rule the registry never looks at"
`)
		if len(unreadRules(broken, rules)) == 0 {
			heard.Errorf("a rule declared and left out of the registry went unnoticed, so shortening the registry — or emptying it — would too")
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
// checkFailurePutDown. A way to fail that is stored rather than handed over is
// one the walk cannot follow, and at the call site its type says only
// "callback" — so an assertion made through it is in no population, needs no
// message, no proof and no fault. Refusing it where it is put down is the whole
// of what keeps "a value does not stop being testify by being given a shorter
// name" true of values that are given no name at all.
func checkFailurePutDown(l *ledger) []complaint {
	var out []complaint
	for _, p := range l.src.waysToFailPutDown() {
		out = append(out, complaint{ruleFailurePutDown, p.site,
			"a way to fail this test is put in " + p.where + ", where nothing follows it: called from there its type is func(...any) or func(), which is the shape of every callback in the language, so the claim made through it would be in no population and would need no fault. Hand it over, do not store it"})
	}
	return out
}

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
