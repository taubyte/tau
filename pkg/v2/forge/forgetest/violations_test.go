package forgetest

import (
	"strings"
	"testing"

	"github.com/taubyte/tau/pkg/v2/forge/fake"
)

// THE FAULTS BEHIND THE LEDGER'S OWN RULES.
//
// Each of these is a ledger that breaks exactly one rule, and the self-test in
// guard_test.go requires that rule to say so. They are the ledger's version of
// fake.Fault: the fault is injected, the thing that is supposed to notice is
// run, and a rule that stays quiet is decoration.
//
// The ones about the SOURCE are real source. `with` type-checks this very
// package with one more file in it, so the rule reads a violation through the
// same loader, the same go/types resolution and the same walk as the honest
// code — there is no second implementation to drift.
//
// The ones about a PROOF perturb the real ledger where they can: an existing
// proof with one saying taken away, or one added, over the real population and
// the real recorded run. Where a real perturbation cannot reach the case — a
// step firing for a reason no subject in the suite produces, a suite that did
// not fail at all — the ledger is built by hand over the real population, and
// that is said here rather than implied.

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

// ─────────────────────────── the source ones ───────────────────────────

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

// violateEmptyPopulation is by hand: no source this package can be handed
// produces an empty population while this file is in it, and an empty
// population is precisely the state in which every other rule holds vacuously.
func violateEmptyPopulation(t *testing.T) *ledger {
	return &ledger{src: suiteSource(t), pop: nil, proofs: allProofs()}
}

// ─────────────────────────── the proof ones ───────────────────────────

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

// ─────────────────────────── the run ones ───────────────────────────

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

// violateFaultDoesNothing is the laundering that started this round, run for
// real: a proof carrying one more fault than anything it says depends on.
//
// It is built out of the suite's own material rather than by hand. A proof that
// injects a fault is given a SECOND one, taken from another proof, and both
// runs the rule compares are really recorded — the subject with both, and the
// subject with the borrowed one taken away. The second is the proof's own run,
// so every complaint it names is still made, and the borrowed fault is behind
// none of them. That is a fault moved into a neighbouring proof's list, which
// is exactly the edit that laundered a live contract out of the suite.
//
// The pair is chosen by what it does and not by what it is called: any proof
// whose complaints survive being given the extra fault will do, and one that
// does not survive is skipped rather than argued with.
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

// ─────────────────────── finding real material to break ───────────────────────

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
