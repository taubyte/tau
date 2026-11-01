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

	"github.com/taubyte/tau/pkg/forge/fake"
)

// The ledger's rules are values: a name, a check, and violations it must
// complain about, so a hollowed rule fails by name. What runs the rules (the
// report, the self-test, the registry, the list of harnesses, the loader) is
// held the same way. Deleting a rule outright, in one hunk, is not covered:
// the same exposure as deleting a test.

// complaint is one thing the ledger has to say. It carries the rule that said
// it so that a rule proven by the self-test is proven under its own name and
// not under whichever rule happened to fire.
type complaint struct {
	rule ruleName
	at   string
	text string
}

// ruleName is a type so the ledger can read the rules it declares out of its
// own source.
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
	ruleLooseLoad          ruleName = "a source load under an environment nobody chose"
)

// ledger is everything the rules are read over: the suite's own source, the
// proofs, and what the suite SAID about each of them. A rule is a function of
// this and nothing else, which is what lets a violating one be handed over.
type ledger struct {
	src    *source
	pop    []assertion
	proofs []proof
	said   map[string]*recorder

	// unwalked is every suite body the walk does not enter: empty by construction,
	// and carried anyway, since an assertion in a body nothing walks is in no
	// rule's population.
	unwalked []string

	// loads is every call to packages.Load written in the forge tree that is
	// not the one inside hermetic.Load. Three tests here read source rather
	// than trusting a comment about it, and a test that reads source is only as
	// trustworthy as the source it was handed.
	loads []string

	// without is what the suite says about a proof's subject when ONE of its
	// faults is taken away and everything else is left alone. It is the
	// counterfactual the causal rule is read over, and it is recorded here
	// rather than computed inside a rule because a rule is a function of the
	// ledger and of nothing else.
	without map[string]map[fake.Fault]*recorder
}

// violates is a list: a rule evaded in more than one shape needs each shape
// shown.
type rule struct {
	name     ruleName
	check    func(*ledger) []complaint
	violates []func(*testing.T) *ledger
}

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
		{ruleStrandedFault, checkStrandedFault, breaks(violateStrandedFault,
			violateStrandedFaultDeclaredWithoutItsType)},
		{ruleFaultDoesNothing, checkFaultDoesNothing, breaks(violateFaultDoesNothing)},
		{ruleUnwalkedBody, checkUnwalkedBody, breaks(violateUnwalkedBody)},
		{ruleFailurePutDown, checkFailurePutDown, breaks(violateFailurePutDown)},
		{ruleLooseLoad, checkLooseLoad, breaks(violateLooseLoad, violateLooseLoadThroughAValue)},
	}
}

// obey reads one rule over a ledger and reports what it says: the one path a
// failure travels, the self-test included.
func obey(t reporter, l *ledger, r rule) int {
	said := 0
	for _, c := range r.check(l) {
		t.Errorf("%s: %s", c.rule, c)
		said++
	}
	return said
}

// Each of the three things that run the rules is a harness with a registry that
// breaks it, and hold is the one call that runs one, so the call the suite is
// held by is the call the self-test exercises. What ran is counted inside hold.

// harnessName is a type for ruleName's reason.
type harnessName string

const (
	harnessTheSuiteObeysTheLedger    harnessName = "the suite obeys the ledger"
	harnessTheLedgerObeysItsOwnRules harnessName = "the ledger obeys the rule it imposes"
	harnessEveryRuleDeclaredIsRead   harnessName = "every rule the ledger declares is read"
)

// harness has no loop: hold goes round the rules and asks a harness about one at
// a time, since a range expression is a thing one token shortens.
type harness struct {
	name harnessName
	// run is asked about one rule and reports to the listener it is handed. It
	// answers with the rule it was asked about, which hold tallies, so skipping a
	// rule is named.
	run func(t *testing.T, heard *recorder, r rule) ruleName
	// after is asked once, about the set hold built while going round, so a
	// shortened loop shortens it too.
	after func(t *testing.T, heard *recorder, read map[string]bool)
	// breaks is a registry this harness must complain about — its fault, in
	// the same sense a rule's violating ledger is one.
	breaks func(t *testing.T) []rule
}

func ledgerHarnesses() []harness {
	return []harness{
		{harnessTheSuiteObeysTheLedger, readOneRuleOverTheSuite, nil, aRuleWithSomethingToSayAboutEverything},
		{harnessTheLedgerObeysItsOwnRules, proveOneRule, nil, aRuleThatIsDecoration},
		{harnessEveryRuleDeclaredIsRead, theRuleItself, readEveryRuleDeclared, aRegistryOneShort},
	}
}

// registry is a set of rules and what running a harness over them must come to:
// silent over the rules as they stand, complaining over a registry that breaks
// it. Each is the other's control.
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

// unvisited is every rule handed in that going round did not reach the same
// number of times. The base is the list hold was handed, read again here, so a
// shortened range is named.
func unvisited(rules []rule, went map[ruleName]int) []string {
	want := map[ruleName]int{}
	for _, r := range rules {
		want[r.name]++
	}
	var out []string
	for name, n := range want {
		switch got := went[name]; {
		case got < n:
			out = append(out, fmt.Sprintf("%s was handed to this harness %d time(s) and reached it %d: the loop that runs them stopped short of it — or never started — so it was read over nothing",
				name, n, got))
		case got > n:
			out = append(out, fmt.Sprintf("%s was handed to this harness %d time(s) and reached it %d: going round reaches each rule once, so something wrote into the tally rather than going round",
				name, n, got))
		}
	}
	for name, got := range went {
		if want[name] == 0 {
			out = append(out, fmt.Sprintf("%s reached this harness %d time(s) and was never handed in, so the rules it read are not the rules it was given",
				name, got))
		}
	}
	sort.Strings(out)
	return out
}

// hold runs one harness over one registry: the only place a harness runs and
// what it said is read. It takes no reporter, so discarding is inexpressible.
func hold(t *testing.T, ran *census, h harness, over registry) {
	ran.write(heldRun{h.name, over.complains})
	heard := &recorder{}
	went := map[ruleName]int{}
	read := map[string]bool{}
	for _, r := range over.rules {
		read[string(r.name)] = true
		went[h.run(t, heard, r)]++
	}
	if h.after != nil {
		h.after(t, heard, read)
	}
	if heard.failed != over.complains || len(unvisited(over.rules, went)) > 0 {
		t.Errorf("%s", over.wrong(h, heard, unvisited(over.rules, went)))
	}
}

// wrong is deliberately not load-bearing: Errorf fails the test whatever it is
// handed.
func (over registry) wrong(h harness, heard *recorder, missed []string) string {
	if len(missed) > 0 {
		return fmt.Sprintf("%q was run %s and was not asked about every rule it was handed:\n\t%s\nso whatever it did say was said over a subset, and the rules it was never asked about were read over nothing. It also said:\n%s",
			h.name, theDirection(over.complains), strings.Join(missed, "\n\t"), heard)
	}
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

// census is what ran, written inside hold. It counts rather than ticks, since a
// set can be filled in advance: a run written twice was recorded without
// running.
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

func TestTheLedgerHolds(t *testing.T) {
	l := theLedger(t)
	counts := map[kind]int{}
	for _, a := range l.pop {
		counts[a.kind]++
	}
	t.Logf("%d cases: %d contract, %d plumbing", len(l.pop), counts[contract], counts[plumbing])
	t.Logf("%d proofs against %d faults the fake forge can be told to have", len(l.proofs), len(l.src.faults))

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

	// And one declared without the type on its line.
	stripped := src.with(t, "ledger_fault_unheld_untyped_harness_test.go", `package forgetest

const harnessNobodyRunsEither = "a way of running the rules nothing ever runs, declared without the type on the line"

var _ = harness{name: harnessNobodyRunsEither}
`)
	if len(everyRun().unaccounted(stripped)) == 0 {
		t.Fatalf("a harness declared without the type on its line, and never run, went unnoticed: the census is a closure over a spelling and not over the harnesses this file declares")
	}
}

func TestTheRulesReachedAreCountedAgainstTheRulesHandedIn(t *testing.T) {
	rules := ledgerRules()
	if len(rules) < 2 {
		t.Fatalf("the ledger declares fewer than two rules, so a tally short by one is not a tally")
	}
	full := func() map[ruleName]int {
		out := map[ruleName]int{}
		for _, r := range rules {
			out[r.name]++
		}
		return out
	}

	if got := unvisited(rules, full()); len(got) > 0 {
		t.Fatalf("a complete tally reported %v, so it names rules that were reached and nothing it says can be read", got)
	}

	for i, r := range rules {
		short := full()
		short[r.name]--
		if len(unvisited(rules, short)) == 0 {
			t.Errorf("%s was handed over and never reached, and the tally did not notice: a loop that stopped short of position %d — which is what `over.rules[:%d]` in hold is — would pass", r.name, i, i)
		}
	}

	if got := unvisited(rules, map[ruleName]int{}); len(got) != len(rules) {
		t.Errorf("a tally nothing was ever written into named %d rules and not %d, so a loop that never ran — or one whose body does not tally — would pass", len(got), len(rules))
	}

	written := full()
	written[rules[0].name]++
	if len(unvisited(rules, written)) == 0 {
		t.Errorf("%s was written into the tally twice and it did not notice, so filling the tally in and then going round a subset would pass", rules[0].name)
	}

	stranger := full()
	stranger[ruleName("a rule this harness was never given")]++
	if len(unvisited(rules, stranger)) == 0 {
		t.Errorf("a rule that was never handed in was reached anyway and the tally did not notice, so a loop over some other registry would pass")
	}
}

func readOneRuleOverTheSuite(t *testing.T, heard *recorder, r rule) ruleName {
	l := theLedger(t)
	t.Run(string(r.name), func(t *testing.T) { obey(heard, l, r) })
	return r.name
}

// theRuleItself is the run of a harness whose whole question is about the SET.
// It exists so that there is no nil to guard, and so the tally is written the
// same way for all three: by the answer a harness gives about the rule it was
// asked about.
func theRuleItself(_ *testing.T, _ *recorder, r rule) ruleName { return r.name }

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

// proveOneRule hands a rule every ledger that breaks it and requires the rule to
// say so, under its own name, through the loop that reports it.
func proveOneRule(t *testing.T, heard *recorder, r rule) ruleName {
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
				if n := obey(said, broken, r); n == 0 || !said.failed {
					heard.Errorf("%s was handed %s, a ledger that breaks it, and %d complaints reached the report (failed=%v): it is decoration, and hollowing it — or the loop that reports it — would go unnoticed",
						r.name, theShapeOf(violate), n, said.failed)
				}
			})
		}
	})
	return r.name
}

// reporter is where obey sends a complaint, so a rule read over a breaking
// ledger can be looked at rather than ending the test.
type reporter interface{ Errorf(string, ...any) }

// readEveryRuleDeclared closes the registry from outside it: a rule constant no
// entry carries is a rule nothing runs.
func readEveryRuleDeclared(t *testing.T, heard *recorder, read map[string]bool) {
	src := suiteSource(t)
	t.Run("as it stands", func(t *testing.T) {
		for _, name := range unreadRules(src, read) {
			heard.Errorf("%s is declared and no registry entry reads it, so nothing computes it over anything", name)
		}
	})
	t.Run("one more rule declared than the registry reads", func(t *testing.T) {
		broken := src.with(t, "ledger_fault_unread_rule_test.go", `package forgetest

const ruleNobodyReads ruleName = "a rule the registry never looks at"
`)
		if len(unreadRules(broken, read)) == 0 {
			heard.Errorf("a rule declared and left out of the registry went unnoticed, so shortening the registry — or emptying it — would too")
		}
	})
	t.Run("one more rule declared without the type on the line", func(t *testing.T) {
		// A constant has a type where it is used, which is what is read, so this fault
		// is untyped.
		broken := src.with(t, "ledger_fault_unread_untyped_rule_test.go", `package forgetest

const ruleNobodyReadsEither = "a rule the registry never looks at, declared without the type on the line"

var _ = complaint{rule: ruleNobodyReadsEither}
`)
		if len(unreadRules(broken, read)) == 0 {
			heard.Errorf("a rule declared without the type on its line, and left out of the registry, went unnoticed: the registry check is a closure over a spelling and not over the rules this file declares")
		}
	})
}

// unreadRules is every rule name this file declares that no registry entry
// carries, by the identifier it is declared under.
func unreadRules(src *source, read map[string]bool) []string {
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
		l.loads = looseLoads(t, nil)
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

// checkStepGoesNowhere: a step is exempt from needing a fault because it leads
// to a claim, so it must: a real assertion after it in the same function, or a
// function returning the value a question is asked with.
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

// checkProofProvesNothing: a proof whose fault produced no complaint, only
// prevented steps, has proved nothing.
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

// checkStrandedFault is the inverse closure: a fault no proof injects is a
// mistake nothing checks for or a proof somebody deleted. It stops a demoted
// assertion from hiding.
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

// checkFailurePutDown: a way to fail that is stored rather than handed over is
// one the walk cannot follow, so it is refused where it is put down.
func checkFailurePutDown(l *ledger) []complaint {
	var out []complaint
	for _, p := range l.src.waysToFailPutDown() {
		out = append(out, complaint{ruleFailurePutDown, p.site,
			"a way to fail this test is put in " + p.where + ", where nothing follows it: called from there its type is func(...any) or func(), which is the shape of every callback in the language, so the claim made through it would be in no population and would need no fault. Hand it over, do not store it"})
	}
	return out
}

// checkLooseLoad: a load site in a package whose binary links neither hermetic's
// loader nor its pin runs under the machine's environment, and is named here.
func checkLooseLoad(l *ledger) []complaint {
	var out []complaint
	for _, site := range l.loads {
		out = append(out, complaint{ruleLooseLoad, site,
			"this reaches the loader that type-checks source, in a binary nothing has pinned, so the environment it reads under is whatever the machine running the test happens to have: a workspace can decide which tree it is shown and a missing module can be fetched over the network from inside a test. Whatever it then proves, it proves about source nobody chose"})
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

// checkFaultDoesNothing makes the fault closure causal: build the subject
// without each fault on its own, rerun the suite, and require a complaint the
// proof names to stop. Naming a fault is not evidence.
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
