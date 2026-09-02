// Package forgetest is the conformance suite every forge driver runs against
// itself.
//
// IT ASSERTS BEHAVIOUR AND NOT SHAPE. That a driver satisfies forge.Client is
// the compiler's job and needs no suite; what needs one is that a public
// repository reads as read and not write, that an outage returns an error and
// never a definitive no, that a repository key is DERIVED from the answer the
// forge gave rather than echoed back from the argument, that a delivery whose
// body moved by one byte is rejected, that a hook can only speak for its own
// repository, that a capability a driver appears to have is one it can actually
// perform, and that a credential never turns up in an error string, in a URL,
// or at a host it is not for. Every one of those is a thing a driver can get
// wrong while compiling perfectly.
//
// TWO OF THOSE NEED A FIXTURE THAT DISAGREES, and for a long time neither had
// one, so neither was checked. A key round-tripping unchanged is what an
// ECHOING driver produces too, and only Moved — a repository the forge answers
// for under one key and answers WITH under another — tells the two apart. A
// credential's destination cannot be read off a fixture at all: a fixture
// records what ARRIVED at it, so the one request it can never see is the
// request that went somewhere else. RedirectOnce and the decoy the suite starts
// against it are where that request becomes visible.
//
// AND A REDIRECT HAS A SECOND EDGE THE DECOY IS BLIND TO. A hop to another host
// is refused by forge.PinnedTransport under every driver; a hop to another
// REPOSITORY ON THE SAME HOST is permitted by it, correctly, because a per-hop
// host check is the whole of what a pin can decide. Every request stays on the
// host the credential is for, every recorded URL passes the canary, and the
// forge has just chosen which repository the caller's question was about. The
// suite aims RedirectOnce at both, and the same-host probe reads the ANSWER an
// operation produced rather than the destination — because there is nothing
// wrong with the destination.
//
// THE SUITE NEVER SKIPS. For each optional capability it runs one of two
// contracts — the capability's own, or the contract that absence is
// *forge.UnsupportedError and that Require refuses — so a driver cannot hide a
// broken capability by not declaring one. Nothing here is conditional on a
// forge being reachable, because nothing here reaches one: a Subject is
// supplied by the driver's own tests, over httptest or over memory.
//
// The suite is itself tested, and then the test is tested. pkg/v2/forge/fake can
// be told to be wrong in named ways, and this package's tests assert that each
// one fails the suite — which is the only way to know that an assertion asserts.
//
// AND THE OTHER WAY ROUND, which is the direction the defects kept arriving
// from: every assertion here must have a FAULT BEHIND IT. An assertion nothing
// can make fire is decoration, it reads exactly like a check, and three
// separate rounds added one or deleted a live one with every test still green.
// So the suite's own assertions are a population the ledger walks, and each one
// is a claim needing a fault, a STEP that is on its way to a claim, or a value
// this package does not produce at all — with the test in pkg/v2/forge that
// holds it named on the line. See ledger_test.go, and plumbing.go for the
// spelling that tells a step from a claim.
//
// MEASURE IT WITH -coverpkg. Plain -cover counts only the statements a
// package's OWN tests reach, and the two packages a conformance suite is made
// of are run by everybody else: pkg/v2/forge/fake is exercised by this
// package's tests and by consumers', so plain -cover calls it 28.0% where the
// pinned measurement says 94.6%. This package reads the same either way
// (97.6%) only because its tests run the whole suite themselves. One command
// is true for both, and it names the package being measured:
//
//	go test -count=1 -p 4 -coverpkg=./pkg/v2/forge/forgetest/... ./pkg/v2/forge/...
//	go test -count=1 -p 4 -coverpkg=./pkg/v2/forge/fake/...      ./pkg/v2/forge/...
package forgetest

import (
	"net/http"
	"time"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/v2/forge"
)

// Request is one request a subject made. A driver's tests fill these in from
// an httptest recorder; the fake fills them in from what it would have sent.
// The fields are the ones the credential canary reads.
type Request struct {
	Method string
	URL    string
	Header http.Header
}

// Subject is one forge driver, plus the fixtures on it the suite names and the
// probes it cannot get through the interface.
//
// Everything marked REQUIRED is a t.Fatalf when missing: a Subject that leaves
// one out has not been checked, it has been skipped, and the difference is
// invisible in a green test run.
type Subject struct {
	// Name labels the run. Empty is fine.
	Name string

	// Registry holds EXACTLY ONE binding, for Host. The suite resolves the
	// driver out of it, so it also verifies through registry-level entry points
	// (VerifyPush, IsActiveMember) the way a consumer does.
	Registry *forge.Registry
	Host     string

	// Expect is the EXACT capability set a session for Credential must compute.
	// The suite asserts EQUALITY and not superset, because that is the only
	// thing that catches a face whose method signature is one type wrong: it
	// compiles, the type assertion silently fails, and the capability
	// disappears without a word.
	Expect forge.Set

	// Credential is spent on every call. BadCredential is one the forge
	// refuses. Token and BadToken are their raw values, so the suite can look
	// for them where they must never appear.
	Credential    forge.Credential
	BadCredential forge.Credential
	Token         string
	BadToken      string

	// The fixtures. All must be on Host.
	//
	//	Writable    at least AccessWrite for Credential
	//	ReadOnly    public or otherwise readable with NO grant: EXACTLY AccessRead
	//	NoAccess    exists, Credential holds nothing on it
	//	Missing     does not exist
	//	Admin       AccessAdmin; hooks, deploy keys and the push probe run here
	//	Deletable   AccessAdmin, and nothing else in the suite uses it
	//	Creatable   a legal key that does not exist yet
	//	Uncreatable a legal key in a namespace Credential may not create in
	//	Deep        Owner contains "/"; AccessAdmin when DeepNests
	Writable    repokey.Key
	ReadOnly    repokey.Key
	NoAccess    repokey.Key
	Missing     repokey.Key
	Admin       repokey.Key
	Deletable   repokey.Key
	Creatable   repokey.Key
	Uncreatable repokey.Key
	Deep        repokey.Key

	// Moved is a key this forge STILL ANSWERS FOR and does not answer AS:
	// asking about it yields the repository MovedTo names, readable by
	// Credential. A rename is the usual way a forge comes to have one, and
	// there are others — a transfer, a case fold, an instance behind a path
	// rewrite — so HOW the fixture arranges the disagreement is its own
	// business. A driver that follows no redirect (which is a defensible
	// policy, and one driver here holds it) needs the answer to arrive in one
	// hop, so a fixture serves the moved repository rather than pointing at it.
	//
	// IT IS THE ONLY FIXTURE THAT CAN ASSERT DERIVATION. forge.Client's own
	// contract says Repository's Key must be derived from the clone URL the
	// forge returned and never echoed back from the argument, and on every
	// other fixture in this Subject the two are the same string — so the
	// forbidden echo and the required derivation produce identical answers and
	// the contract is unassertable. The suite therefore REQUIRES them, and
	// requires them to differ: Moved == MovedTo is a Subject that has quietly
	// opted out of the assertion.
	Moved   repokey.Key
	MovedTo repokey.Key

	// DeepNests says whether this forge addresses a nested owner. When false the
	// suite requires *forge.UnaddressableError AND that no request was made —
	// a depth is refused before the wire, never flattened into a name no
	// repository has. When true, the deep key runs the round trip too.
	DeepNests bool

	// Requests reports every request the subject made since the last call, and
	// resets. REQUIRED: it is what proves a refusal happened before the wire,
	// and it is what the credential canary reads.
	Requests func() []Request

	// Break makes the next call fail at the transport; Unbreak restores. Both
	// REQUIRED. They are the only way to assert the invariant that matters
	// most: an outage is an error and never (AccessNone, nil).
	Break   func()
	Unbreak func()

	// RedirectOnce makes the forge answer the NEXT request it is given with
	// status, pointing at loc, once. REQUIRED.
	//
	// IT IS ARMED ONCE PER OPERATION, not once per run, and every operation
	// this subject declares gets a turn — the deployment's own credential
	// included, which rides a client no session can reach. A driver is not one
	// method, and a redirect policy written at one call site is not a policy the
	// next call site has.
	//
	// THE SUITE ARMS IT WITH TWO KINDS OF loc, AND THEY PROVE DIFFERENT THINGS.
	//
	// A HOST NOBODY HERE HOLDS A CREDENTIAL FOR. This is the only way the suite
	// can see where a request went: Requests reports what a fixture observed,
	// and a fixture observes arrivals, so a request that went to another host
	// arrives nowhere it can report and "every recorded request went to Host" is
	// a sentence that cannot fail for a driver that sent one elsewhere. A forge
	// answering 302 to somewhere else is the whole threat forge.PinnedTransport
	// exists for — every SDK runs on an http.Client and an http.Client follows
	// redirects — so the suite starts a server of its own at loc and reads what
	// showed up. A driver must send NOTHING there.
	//
	// AN ADDRESS ON Host NAMING ANOTHER REPOSITORY, one this subject itself
	// reported, so the suite invents no URL shapes. The pin permits that hop and
	// must: a per-hop host check is the whole of what a pin decides, and this is
	// the host the credential is for. Nothing under a driver refuses it, nothing
	// over a driver can see it, and what comes back is another repository's
	// permissions, another repository's hook, another repository's deletion. The
	// suite reads the ANSWER the operation produced rather than the destination,
	// because there is nothing wrong with the destination.
	//
	// THE STATUS IS THE SUITE'S CHOICE AND IT DECIDES WHICH HALF IS REACHED,
	// which is why it is a parameter and not a fixture's constant. A 301, 302
	// or 303 makes Go rewrite the request to a GET: a write following one lands
	// nowhere, and the driver reads somebody else's 200 and reports success
	// having done nothing. A 307 or 308 replays method and body, so the write
	// lands on the repository the redirect named. A fixture that only ever
	// answered 302 left the second half unreached — and worse, it made a
	// replayed POST arrive at an endpoint that answers only GET, so the call
	// failed on ENDPOINT SHAPE and the probe passed without ever reaching the
	// question it asks.
	//
	// A subject that speaks no HTTP implements it by recording the request it
	// WOULD have sent to loc, which the same subtest reads out of Requests, and
	// by acting on the repository loc names — under the same rule about the
	// status, since that rule is the stdlib's and not a fixture's. Either way
	// what happened is observed rather than assumed.
	RedirectOnce func(loc string, status int)

	// Required when Expect has CapDeployKeys.
	//
	// LastDeployKeyReadOnly is a probe the SUBJECT supplies, so a driver author
	// who supplies a lying one passes. It is required and it is not verifiable
	// from outside — the interface has no parameter to inspect — and saying so
	// is more honest than a test that pretends otherwise.
	LastDeployKeyReadOnly func() bool
	DeployKeyInstalled    func(forge.Handle) bool

	// Required when Expect has CapPushHooks. LastHookWire is the string the
	// forge was ACTUALLY given, which is what proves the driver passed it
	// through instead of re-encoding it into something no delivery will match.
	LastHookWire  func() string
	HookInstalled func(forge.Handle) bool

	// Required when Expect has CapSignatures. Each builds a REAL, correctly
	// signed delivery in this forge's own payload shape — real HMAC over real
	// bytes — so that what is being tested is verification and not a stub.
	//
	// DeliveryFor names a DIFFERENT repository under the same hook's secret and
	// scheme: the confused-deputy case, a delivery that is perfectly signed and
	// speaks for somebody else's repository.
	//
	// DeliveryAt is required only when the driver's scheme signs a timestamp
	// (forge.SchemeStandardWebhooks today); it is what the replay-window cases
	// need and nothing else calls it.
	Delivery        func(h forge.Hook, ref, commit string) (forge.Delivery, error)
	DeliveryFor     func(h forge.Hook, other repokey.Key, ref, commit string) (forge.Delivery, error)
	NonPushDelivery func(h forge.Hook) (forge.Delivery, error)
	DeliveryAt      func(h forge.Hook, ref, commit string, at time.Time) (forge.Delivery, error)

	// Required when Expect has CapMembership. Namespace/Member/NonMember are
	// answered by the DEPLOYMENT's credential, not by Credential.
	Namespace string
	Member    string
	NonMember string

	// Required when Expect has CapPushProbe: a git credential that may push to
	// Admin, and one that may not.
	PushKey   []byte
	NoPushKey []byte
}
