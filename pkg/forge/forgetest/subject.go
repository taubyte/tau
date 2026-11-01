// Package forgetest is the conformance suite every forge driver runs against
// itself. It asserts behaviour, not shape: a public repository reads as read, an
// outage is an error and never a definitive no, a key is derived from the
// forge's answer, a hook speaks only for its own repository, a declared
// capability works, and a credential never reaches an error, a URL or a host it
// is not for. It never skips: an undeclared capability must answer
// *forge.UnsupportedError. It reaches no real forge.
//
// The suite is tested against pkg/forge/fake's faults, and every assertion in it
// must have a fault behind it (ledger_test.go; plumbing.go tells a step from a
// claim). The tower of guards ends at five points in guard_test.go: hold's
// verdict condition and its Errorf, the unvisited clause, and
// TestTheLedgerHolds's census loop and its Errorf. A TestMain returning before
// m.Run() is the same exposure.
//
// Measure coverage with -coverpkg, since the fake and the suite are run by other
// packages' tests:
//
//	go test -count=1 -p 4 -coverpkg=./pkg/forge/forgetest/...          ./pkg/forge/...
//	go test -count=1 -p 4 -coverpkg=./pkg/forge/forgetest/hermetic     ./pkg/forge/...
//	go test -count=1 -p 4 -coverpkg=./pkg/forge/fake/...               ./pkg/forge/...
package forgetest

import (
	"net/http"
	"time"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// Request is one request a subject made. A driver's tests fill these in from
// an httptest recorder; the fake fills them in from what it would have sent.
// The fields are the ones the credential canary reads.
type Request struct {
	Method string
	URL    string
	Header http.Header
}

// Subject is one forge driver, the fixtures the suite names, and the probes it
// cannot get through the interface. A missing REQUIRED field is a t.Fatalf, not
// a skip.
type Subject struct {
	// Name labels the run. Empty is fine.
	Name string

	// Registry holds EXACTLY ONE binding, for Host. The suite resolves the
	// driver out of it, so it also verifies through registry-level entry points
	// (VerifyPush, IsActiveMember) the way a consumer does.
	Registry *forge.Registry
	Host     string

	// Expect is the exact capability set a session for Credential must compute:
	// equality catches a face whose signature is one type wrong.
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

	// Moved is a key this forge still answers for and does not answer as: asking
	// about it yields MovedTo's repository, in one hop. It is the only fixture where
	// echoing the argument and deriving the key differ, so it is required and must
	// differ from MovedTo.
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

	// RedirectOnce makes the forge answer its next request with status, pointing at
	// loc, once. REQUIRED. The suite arms it per operation, at a host nobody holds a
	// credential for (nothing may arrive there) and at another repository on Host
	// (the answer must not describe it). The status is the suite's: 301, 302 and 303
	// make Go rewrite a write to a GET, 307 and 308 replay it. A subject speaking no
	// HTTP records the request it would have sent to loc and acts on the repository
	// loc names.
	RedirectOnce func(loc string, status int)

	// Required when Expect has CapDeployKeys. The subject supplies this probe, so a
	// lying one passes: the interface has nothing to inspect.
	LastDeployKeyReadOnly func() bool
	DeployKeyInstalled    func(forge.Handle) bool

	// Required when Expect has CapPushHooks. LastHookWire is the string the
	// forge was ACTUALLY given, which is what proves the driver passed it
	// through instead of re-encoding it into something no delivery will match.
	LastHookWire  func() string
	HookInstalled func(forge.Handle) bool

	// Required when Expect has CapSignatures: real, correctly signed deliveries in
	// this forge's payload shape. DeliveryFor names another repository under the
	// same hook's secret and scheme. DeliveryAt is required only for a scheme that
	// signs a timestamp.
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
