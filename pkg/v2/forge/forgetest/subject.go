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
// perform, and that a credential never turns up in an error string or a URL.
// Every one of those is a thing a driver can get wrong while compiling
// perfectly.
//
// ONE OF THEM NEEDS A FIXTURE THAT DISAGREES, and for a long time there was
// none, so it went unchecked: a key round-tripping unchanged is what an ECHOING
// driver produces too, and only Moved — a repository the forge answers for
// under one key and answers WITH under another — tells the two apart.
//
// THE SUITE NEVER SKIPS. For each optional capability it runs one of two
// contracts — the capability's own, or the contract that absence is
// *forge.UnsupportedError and that Require refuses — so a driver cannot hide a
// broken capability by not declaring one. Nothing here is conditional on a
// forge being reachable, because nothing here reaches one: a Subject is
// supplied by the driver's own tests, over httptest or over memory.
//
// The suite is itself tested. pkg/v2/forge/fake can be told to be wrong in
// named ways, and this package's tests assert that each one fails the suite —
// which is the only way to know that an assertion asserts.
//
// MEASURE IT WITH -coverpkg. Almost every statement in this package runs from
// the tests of ANOTHER package — the fake's, github's, gitlab's — and plain
// -cover counts only the ones a package's own tests reach, which reports 77.4%
// here and 27.5% for the fake and is not what either of them is. The honest
// command names the package being measured:
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
