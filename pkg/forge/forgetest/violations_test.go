package forgetest

import (
	"go/ast"
	"go/types"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taubyte/tau/pkg/forge/fake"
)

// The faults behind the ledger's own rules: each ledger breaks one rule, which
// the self-test in guard_test.go requires to say so. Source violations are real
// source type-checked through with; proof violations perturb the real ledger,
// or are built by hand where no perturbation reaches the case.

// but is this ledger with a different set of proofs, and whatever the suite is
// recorded as having said about them.
func (l *ledger) but(proofs []proof, said map[string]*recorder) *ledger {
	out := &ledger{src: l.src, pop: l.pop, proofs: proofs,
		said:    map[string]*recorder{},
		without: map[string]map[fake.Fault]*recorder{}}
	for k, v := range l.said {
		out.said[k] = v
	}
	for k, v := range said {
		out.said[k] = v
	}
	for k, v := range l.without {
		out.without[k] = v
	}
	return out
}

func violateSaysNothing(t *testing.T) *ledger {
	src := suiteSource(t).with(t, "ledger_fault_says_nothing.go", `package forgetest

import "github.com/stretchr/testify/assert"

func (r *runner) ledgerFaultSaysNothing(t tb) {
	assert.NotEmpty(t, r.s.Host)
}
`)
	return &ledger{src: src, pop: populationOf(src), proofs: allProofs()}
}

func violateSayingsCollide(t *testing.T) *ledger {
	src := suiteSource(t).with(t, "ledger_fault_collide.go", `package forgetest

import "github.com/stretchr/testify/assert"

func (r *runner) ledgerFaultCollide(t tb) {
	assert.NotEmpty(t, r.s.Host, "the subject names a host")
	assert.NotEmpty(t, r.s.Token, "the subject names a host and a credential")
}
`)
	return &ledger{src: src, pop: populationOf(src), proofs: allProofs()}
}

func violateStepGoesNowhere(t *testing.T) *ledger {
	src := suiteSource(t).with(t, "ledger_fault_step_goes_nowhere.go", `package forgetest

func (r *runner) ledgerFaultStepGoesNowhere(t tb) {
	mustHold(t, r.s.Host != "", "a question there is nothing after this to ask")
}
`)
	return &ledger{src: src, pop: populationOf(src), proofs: allProofs()}
}

// violateOrphan is the evasion that started this round, kept as the fault
// behind the rule that now catches it: a real assertion, in real source, that
// no proof names.
func violateOrphan(t *testing.T) *ledger {
	src := suiteSource(t).with(t, "ledger_fault_orphan.go", `package forgetest

import "github.com/stretchr/testify/assert"

func (r *runner) ledgerFaultOrphan(t tb) {
	assert.NotEmpty(t, r.s.Host, "a claim about the subject that no fault in this suite can kill")
}
`)
	return &ledger{src: src, pop: populationOf(src), proofs: allProofs()}
}

// The shapes the walk once went round, each real source and an orphan.

// violateOrphanInATopLevelLiteral: a function literal is a function literal
// wherever it is bound, and the walk knew two of the places a name can be bound.
func violateOrphanInATopLevelLiteral(t *testing.T) *ledger {
	src := suiteSource(t).with(t, "ledger_fault_orphan_in_a_literal.go", `package forgetest

import "github.com/stretchr/testify/assert"

var ledgerFaultOrphanInALiteral = func(t tb, r *runner) {
	assert.NotEmpty(t, r.s.Host, "a claim in a top-level function literal that no fault in this suite can kill")
}
`)
	return &ledger{src: src, pop: populationOf(src), proofs: allProofs()}
}

// violateOrphanInAMarker: a marker is any function that asks for a step, so a
// step parameter used to exempt everything written beside it.
func violateOrphanInAMarker(t *testing.T) *ledger {
	src := suiteSource(t).with(t, "ledger_fault_orphan_in_a_marker.go", `package forgetest

import "github.com/stretchr/testify/assert"

func ledgerFaultOrphanInAMarker(t tb, r *runner, question step) {
	mustHold(t, r.s.Host != "", question)
	assert.NotEmpty(t, r.s.Token, "a claim written inside a marker that no fault in this suite can kill")
}
`)
	return &ledger{src: src, pop: populationOf(src), proofs: allProofs()}
}

// violateOrphanThroughAMethodValue hands over the method that fails, not the
// reporter. It must live outside this package, since a body here is walked;
// the helper is forgetest/outside.
func violateOrphanThroughAMethodValue(t *testing.T) *ledger {
	src := suiteSource(t).with(t, "ledger_fault_orphan_through_a_method_value.go", `package forgetest

import "github.com/taubyte/tau/pkg/forge/forgetest/outside"

func (r *runner) ledgerFaultOrphanThroughAMethodValue(t tb) {
	outside.Check(t.Errorf, r.s.Host != "", "a claim reached through a method value that no fault in this suite can kill")
}
`)
	return &ledger{src: src, pop: populationOf(src), proofs: allProofs()}
}

// violateOrphanThroughAName: the exemption moved from an import alias to a
// one-line var, and got cheaper to type than the alias had been.
func violateOrphanThroughAName(t *testing.T) *ledger {
	src := suiteSource(t).with(t, "ledger_fault_orphan_through_a_name.go", `package forgetest

import "github.com/stretchr/testify/assert"

var ledgerFaultTruthy = assert.NotEmpty

func (r *runner) ledgerFaultOrphanThroughAName(t tb) {
	ledgerFaultTruthy(t, r.s.Host, "a claim reached through a func-typed name that no fault in this suite can kill")
}
`)
	return &ledger{src: src, pop: populationOf(src), proofs: allProofs()}
}

// violateUnwalkedBody is the real coverage computation with one root taken away:
// no source this package can be handed produces an unwalked body.
func violateUnwalkedBody(t *testing.T) *ledger {
	l := theLedger(t)
	full := l.src.walk()
	if len(full.order) == 0 {
		t.Fatalf("the suite holds no body at all, so the walk cannot be shown one it misses")
	}
	short := &walk{byObj: map[types.Object]*callable{}, byLit: map[*ast.FuncLit]*callable{}}
	for _, c := range full.order[1:] {
		short.order = append(short.order, c)
		if c.obj != nil {
			short.byObj[c.obj] = c
		}
		if c.lit != nil {
			short.byLit[c.lit] = c
		}
	}
	broken := l.but(l.proofs, nil)
	broken.unwalked = l.src.unwalkedBodies(short)
	if len(broken.unwalked) == 0 {
		t.Fatalf("a walk with one of its roots taken away still reaches every body in the suite, so nothing here would notice losing one")
	}
	return broken
}

// violateEmptyPopulation is by hand: no source this package can be handed
// produces an empty population while this file is in it, and an empty
// population is precisely the state in which every other rule holds vacuously.
func violateEmptyPopulation(t *testing.T) *ledger {
	return &ledger{src: suiteSource(t), pop: nil, proofs: allProofs()}
}

func violateUnknownSaying(t *testing.T) *ledger {
	l := theLedger(t)
	return l.but([]proof{{name: "a-proof-naming-words-nothing-says",
		says: []string{"a sentence this suite has never contained"}}}, nil)
}

func violateAmbiguousSaying(t *testing.T) *ledger {
	l := theLedger(t)
	frag := sharedFragment(t, l.pop)
	return l.but([]proof{{name: "a-proof-naming-words-two-assertions-say", says: []string{frag}}}, nil)
}

func violateSayingNamesAStep(t *testing.T) *ledger {
	l := theLedger(t)
	s := aPlumbing(t, l.pop)
	return l.but([]proof{{name: "a-proof-naming-a-step", says: []string{s.says}}}, nil)
}

func violateUnknownStop(t *testing.T) *ledger {
	l := theLedger(t)
	return l.but([]proof{{name: "a-proof-stopping-words-nothing-says",
		stops: []string{"a step this suite has never taken"}}}, nil)
}

func violateAmbiguousStop(t *testing.T) *ledger {
	l := theLedger(t)
	frag := sharedFragment(t, l.pop)
	return l.but([]proof{{name: "a-proof-stopping-words-two-places-say", stops: []string{frag}}}, nil)
}

func violateStopNamesAClaim(t *testing.T) *ledger {
	l := theLedger(t)
	a := aContract(t, l.pop)
	return l.but([]proof{{name: "a-proof-stopping-a-claim", stops: []string{a.says}}}, nil)
}

// violateStopIsTheComplaint is a proof whose complaint and whose "could not
// reach" are the same function, which is what a demoted claim looks like from
// out here.
func violateStopIsTheComplaint(t *testing.T) *ledger {
	l := theLedger(t)
	claim, step := sameFunction(t, l.pop)
	return l.but([]proof{{name: "a-proof-stopping-where-it-complains",
		says: []string{claim.says}, stops: []string{step.says}}}, nil)
}

func violateProofProvesNothing(t *testing.T) *ledger {
	l := theLedger(t)
	s := aPlumbing(t, l.pop)
	return l.but([]proof{{name: "a-proof-that-only-prevents", stops: []string{s.says}}}, nil)
}

// violateProofPassed is by hand: every proof in the suite fails, which is what
// TestTheSuiteObeysTheLedger keeps true, so the one state this rule is about
// cannot be reached by perturbing a proof's lists.
func violateProofPassed(t *testing.T) *ledger {
	l := theLedger(t)
	p := l.proofs[0]
	return l.but([]proof{p}, map[string]*recorder{p.name: {}})
}

// violateFiredUnnamed takes a real proof's sayings away. Everything its fault
// makes the suite complain about then fires under a proof that names none of
// it, which is the shape an assertion has when its deletion would go unnoticed.
func violateFiredUnnamed(t *testing.T) *ledger {
	l := theLedger(t)
	p := l.proofs[0]
	return l.but([]proof{{name: p.name, subject: p.subject}}, nil)
}

// violateNamedDidNotFire gives a real proof one more saying, naming a real
// assertion its fault does not reach.
func violateNamedDidNotFire(t *testing.T) *ledger {
	l := theLedger(t)
	p := l.proofs[0]
	fired, _ := attribute(l.pop, l.said[p.name].msgs)
	for _, a := range l.pop {
		if a.kind != contract || len(fired[a.site]) > 0 {
			continue
		}
		says := append(append([]string{}, p.says...), a.says)
		return l.but([]proof{{name: p.name, subject: p.subject, says: says, stops: p.stops}}, nil)
	}
	t.Fatalf("every contract assertion in the suite fires under %q, so there is nothing this proof could name and not reach", p.name)
	return nil
}

// violateStopDidNotStop is by hand. The rule is about a step that fired for a
// reason that is NOT a capability refusal, and the suite has no subject that
// produces one — which is the point of the rule: the day somebody writes one,
// this is what refuses it.
func violateStopDidNotStop(t *testing.T) *ledger {
	l := theLedger(t)
	s := aPlumbing(t, l.pop)
	name := "a-proof-whose-stop-did-not-stop"
	return l.but([]proof{{name: name, stops: []string{s.says}, says: []string{aContract(t, l.pop).says}}},
		map[string]*recorder{name: {failed: true, msgs: []string{
			"forgetest: the suite never got as far as " + s.says + ": the forge answered wrongly",
			aContract(t, l.pop).says,
		}}})
}

// violateFailureFromNowhere is by hand: a testify trace pointing at a line no
// assertion in the population is written on.
func violateFailureFromNowhere(t *testing.T) *ledger {
	l := theLedger(t)
	name := "a-proof-whose-failure-came-from-nowhere"
	return l.but([]proof{{name: name}},
		map[string]*recorder{name: {failed: true, msgs: []string{
			"\n\tError Trace:\t/nowhere/nothing.go:1\n\tError: something failed",
		}}})
}

// violateUnplaceable is by hand: a failure with no trace whose words no
// assertion says, which is a failure this file cannot reason about.
func violateUnplaceable(t *testing.T) *ledger {
	l := theLedger(t)
	name := "a-proof-whose-failure-no-assertion-claims"
	return l.but([]proof{{name: name}},
		map[string]*recorder{name: {failed: true, msgs: []string{
			"a failure whose words this suite has never contained anywhere at all",
		}}})
}

// violateStrandedFault takes away the one proof that injects some fault, which
// is what deleting a proof — or demoting the assertion it proved — leaves
// behind.
func violateStrandedFault(t *testing.T) *ledger {
	l := theLedger(t)
	count := map[string]int{}
	for _, p := range l.proofs {
		for _, f := range p.faults {
			count[string(f)]++
		}
	}
	for i, p := range l.proofs {
		for _, f := range p.faults {
			if count[string(f)] != 1 {
				continue
			}
			kept := append(append([]proof{}, l.proofs[:i]...), l.proofs[i+1:]...)
			return l.but(kept, nil)
		}
	}
	t.Fatalf("every fault in the fake forge is injected by more than one proof, so no single deletion strands one")
	return nil
}

// violateLooseLoad is a real load site in a real package that nothing has
// pinned: the fake forge, whose own test binary links neither the loader nor
// the suite that pins the process. It is read by the same walk over the same
// tree as the honest code.
func violateLooseLoad(t *testing.T) *ledger {
	dir, err := filepath.Abs("../fake")
	if err != nil {
		t.Fatalf("finding the fake forge's directory, to show the ledger a source load that breaks its rule: %v", err)
	}
	return &ledger{src: suiteSource(t), loads: looseLoads(t, map[string][]byte{
		filepath.Join(dir, "ledger_fault_loose_load.go"): []byte(`package fake

import "golang.org/x/tools/go/packages"

func ledgerFaultLooseLoad() ([]*packages.Package, error) {
	return packages.Load(&packages.Config{Mode: packages.NeedName}, ".")
}
`),
	})}
}

// violateLooseLoadThroughAValue puts one name between the call and the loader;
// the site named is the line the value was bound on.
func violateLooseLoadThroughAValue(t *testing.T) *ledger {
	dir, err := filepath.Abs("../fake")
	if err != nil {
		t.Fatalf("finding the fake forge's directory, to show the ledger a source load that breaks its rule: %v", err)
	}
	return &ledger{src: suiteSource(t), loads: looseLoads(t, map[string][]byte{
		filepath.Join(dir, "ledger_fault_loose_load_value.go"): []byte(`package fake

import "golang.org/x/tools/go/packages"

var ledgerFaultLoadThroughAValue = packages.Load

func ledgerFaultLooseLoadThroughAValue() ([]*packages.Package, error) {
	return ledgerFaultLoadThroughAValue(&packages.Config{Mode: packages.NeedName}, ".")
}
`),
	})}
}

// violateStrandedFaultDeclaredWithoutItsType: a fault declared without the type
// on its line and typed where it is used, which is where an untyped constant
// gets its type.
func violateStrandedFaultDeclaredWithoutItsType(t *testing.T) *ledger {
	src := suiteSource(t).withIn(t, "../fake", "ledger_fault_untyped_fault.go", `package fake

const faultDeclaredWithoutItsType = "declared-without-its-type"

var _ Fault = faultDeclaredWithoutItsType
`)
	return &ledger{src: src, pop: populationOf(src), proofs: allProofs()}
}

// violateFaultDoesNothing gives a proof a second fault borrowed from another
// proof, and records both runs: every complaint the proof names is still made,
// and the borrowed fault is behind none of them.
func violateFaultDoesNothing(t *testing.T) *ledger {
	l := theLedger(t)
	for _, p := range l.proofs {
		if len(p.faults) == 0 || l.said[p.name] == nil {
			continue
		}
		named, _ := names(l.pop, p)
		if len(named) == 0 {
			continue
		}
		for _, q := range l.proofs {
			borrowed, ok := aFaultNotIn(q.faults, p.faults)
			if !ok {
				continue
			}
			more := append(append([]fake.Fault{}, p.faults...), borrowed)
			said := record(p.subject(t, more))
			fired, _ := attribute(l.pop, said.msgs)
			still := false
			for id := range named {
				if len(fired[id]) > 0 {
					still = true
				}
			}
			if !still {
				continue // the extra fault took this proof's own complaint away
			}
			broken := l.but([]proof{{name: p.name, subject: p.subject, says: p.says,
				stops: p.stops, faults: more}}, map[string]*recorder{p.name: said})
			runs := map[fake.Fault]*recorder{}
			for _, f := range more {
				runs[f] = record(p.subject(t, dropping(more, f)))
			}
			broken.without = map[string]map[fake.Fault]*recorder{p.name: runs}
			return broken
		}
	}
	t.Fatalf("no proof in the suite survives being given a second fault, so the ledger cannot be shown a fault that does nothing")
	return nil
}

// aFaultNotIn is a fault one proof injects that another does not, which is what
// a fault moved into a neighbouring list is.
func aFaultNotIn(faults, exclude []fake.Fault) (fake.Fault, bool) {
	for _, f := range faults {
		found := false
		for _, e := range exclude {
			if e == f {
				found = true
			}
		}
		if !found {
			return f, true
		}
	}
	return "", false
}

func aContract(t *testing.T, pop []assertion) assertion {
	t.Helper()
	for _, a := range pop {
		if a.kind == contract && a.says != "" && !strings.Contains(a.says, "%") {
			return a
		}
	}
	t.Fatalf("the suite has no contract assertion with plain words, so the ledger's own rules cannot be shown one")
	return assertion{}
}

func aPlumbing(t *testing.T, pop []assertion) assertion {
	t.Helper()
	for _, a := range pop {
		if a.kind == plumbing && a.says != "" && !strings.Contains(a.says, "%") {
			return a
		}
	}
	t.Fatalf("the suite has no step with plain words, so the ledger's own rules cannot be shown one")
	return assertion{}
}

// sameFunction is a claim and a step written in the same function, which is
// what makes a stop the complaint itself rather than collateral.
func sameFunction(t *testing.T, pop []assertion) (claim, step assertion) {
	t.Helper()
	for _, a := range pop {
		if a.kind != contract || a.says == "" || strings.Contains(a.says, "%") {
			continue
		}
		for _, b := range pop {
			if b.kind == plumbing && b.in == a.in && b.says != "" && !strings.Contains(b.says, "%") {
				return a, b
			}
		}
	}
	t.Fatalf("no function in the suite holds both a claim and a step, so the collateral rule cannot be shown a violation")
	return assertion{}, assertion{}
}

// sharedFragment is words that more than one assertion says, which is what a
// proof must never name.
func sharedFragment(t *testing.T, pop []assertion) string {
	t.Helper()
	for _, a := range pop {
		for _, b := range pop {
			if a.site == b.site || a.says == "" || b.says == "" {
				continue
			}
			if frag := commonRun(a.says, b.says); frag != "" {
				return frag
			}
		}
	}
	t.Fatalf("no two assertions in the suite share a run of words, so the ambiguity rules cannot be shown a violation")
	return ""
}

// commonRun is the longest leading run of whole words two sayings share, or ""
// when they share nothing worth naming.
func commonRun(a, b string) string {
	wa, wb := strings.Fields(a), strings.Fields(b)
	n := 0
	for n < len(wa) && n < len(wb) && wa[n] == wb[n] {
		n++
	}
	if n == 0 {
		return ""
	}
	run := strings.Join(wa[:n], " ")
	if len(run) < 8 {
		return ""
	}
	return run
}

// violateFailurePutDown: a way to fail is left in a struct field, where the
// walk cannot follow it and the call site's type says only "callback". Both
// halves are here — a testify function, which has a message and asserts, and a
// tb's FailNow, which is shaped like every callback in the language.
func violateFailurePutDown(t *testing.T) *ledger {
	src := suiteSource(t).with(t, "ledger_fault_failure_put_down.go", `package forgetest

import "github.com/stretchr/testify/assert"

type ledgerFaultBox struct {
	check func(assert.TestingT, any, ...any) bool
	stop  func()
}

func (r *runner) ledgerFaultThroughAField(t tb) {
	box := ledgerFaultBox{check: assert.NotEmpty, stop: t.FailNow}
	box.check(t, r.s.Host, "a claim reached through a struct field, which no fault in this suite can kill")
	if r.s.Host == "" {
		box.stop()
	}
}
`)
	return &ledger{src: src, pop: populationOf(src), proofs: allProofs()}
}
