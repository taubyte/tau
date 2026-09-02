package forgetest

import (
	"strings"
	"testing"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/v2/forge"
	"github.com/taubyte/tau/pkg/v2/forge/fake"
)

// TestEveryShapePasses runs the suite against an in-memory forge wearing each
// of the shapes the four real forges have between them. It is the positive half
// of proving the suite is runnable; the negative half — that it CATCHES a
// driver that is wrong — is below and matters more.
//
// The shapes are not variations on a theme. Nested is GitLab's subgroups,
// HandlesUUID is Bitbucket's hook id, a payload with no clone URL is
// Bitbucket's push event, and the four schemes are four real signature
// conventions including one that binds nothing.
func TestEveryShapePasses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		shape fake.Shape
	}{
		{"github-like", fake.GitHubLike},
		{"gitlab-like-nested-standard-webhooks", fake.GitLabLike},
		{"gitlab-18-like-shared-token", fake.GitLabOldLike},
		{"gitea-like", fake.GiteaLike},
		{"bitbucket-like-uuid-handles-no-clone-url", fake.BitbucketLike},
		{"a-host-with-nothing-but-the-three-mandatory-answers", fake.Minimal},
	} {
		Run(t, subjectFor(t, tc.name, tc.shape))
	}
}

// TestAbsenceIsATestedContract runs a forge missing exactly one capability, for
// every optional capability there is. The suite never skips, so each of these
// runs the OTHER contract: the Session method is *UnsupportedError naming that
// capability, and Require refuses it.
func TestAbsenceIsATestedContract(t *testing.T) {
	for _, c := range []forge.Capability{
		forge.CapList, forge.CapCreate, forge.CapDelete, forge.CapDeployKeys,
		forge.CapPushHooks, forge.CapCloneAddr, forge.CapPushProbe,
		forge.CapSignatures, forge.CapMembership,
	} {
		Run(t, subjectFor(t, "without-"+string(c), without(fake.GitHubLike, c)))
	}
}

// TestTheSuiteCatchesABrokenDriver is the assertion that makes every other
// assertion in this package worth something. Each fault is a mistake a driver
// author can make that compiles; the suite must fail, and must fail SAYING WHY,
// because a failure that does not name the defect sends the next person looking
// in the wrong place.
func TestTheSuiteCatchesABrokenDriver(t *testing.T) {
	for _, tc := range []struct {
		fault fake.Fault
		// plus is a SECOND fault, for the cases where one alone is permitted.
		// Following a same-host redirect and deriving the key from what came
		// back is how a rename resolves; following it and echoing the argument
		// is the record that names one repository and describes another. Only
		// the pair is wrong, so only the pair can prove the assertion.
		plus   fake.Fault
		shape  fake.Shape
		saying string
	}{
		{
			fault:  fake.FaultFlattensDeepKey,
			shape:  fake.GitHubLike,
			saying: "must refuse",
		},
		{
			fault:  fake.FaultVisibilityIsPermission,
			shape:  fake.GitHubLike,
			saying: "a readable repository with no grant read as write",
		},
		{
			fault:  fake.FaultOutageIsRefusal,
			shape:  fake.GitHubLike,
			saying: "an unreachable forge answered the authorization question",
		},
		{
			// The echo the contract forbids, on the one fixture where it is
			// visible: a repository the forge answers for under a name it does
			// not answer WITH.
			fault:  fake.FaultEchoesKey,
			shape:  fake.GitHubLike,
			saying: "must be derived from the clone address the forge returned",
		},
		{
			// The OTHER half of the record: a key that is right and clone
			// addresses that name a different repository. Nothing else in the
			// suite refuses this driver, which is what makes it the case that
			// proves the record's address assertion is doing work rather than
			// standing beside one that would have failed anyway.
			fault:  fake.FaultRepositoryAddressNamesAnother,
			shape:  fake.GitHubLike,
			saying: "clone address in the record",
		},
		{
			// And the same mistake on the surface a builder is actually handed.
			// It is a separate fault because it is separate code: one assertion
			// each, and neither can pass on the other's account.
			fault:  fake.FaultCloneAddressNamesAnother,
			shape:  fake.GitHubLike,
			saying: "clone address handed to a builder",
		},
		{
			// A credential that followed a redirect off its own host. No
			// fixture can see this one — the request arrives somewhere else —
			// so the suite reads it where it lands.
			fault:  fake.FaultFollowsRedirect,
			shape:  fake.GitHubLike,
			saying: "which is not the host this credential is for",
		},
		{
			// The hop a credential pin permits by design: same host, another
			// repository. The decoy proves nothing about it — the request never
			// leaves the host it is for — so the only way to see it is to watch
			// what the operation ANSWERED after being pointed somewhere else.
			fault:  fake.FaultFollowsSameHostRedirect,
			shape:  fake.GitHubLike,
			saying: "followed a redirect to",
		},
		{
			// The same hop, on the credential no session holds. Nothing a
			// session does goes near it, so the only thing that finds this is a
			// probe armed at every operation rather than at one.
			fault:  fake.FaultFollowsRedirectOnTheDeploymentCredential,
			shape:  fake.GitHubLike,
			saying: "which is not the host this credential is for",
		},
		{
			fault:  fake.FaultFollowsSameHostRedirect,
			plus:   fake.FaultEchoesKey,
			shape:  fake.GitHubLike,
			saying: "answered with the key it was ASKED about",
		},
		{
			fault:  fake.FaultLeaksTokenInError,
			shape:  fake.GitHubLike,
			saying: "the credential is in an error string",
		},
		{
			fault:  fake.FaultLeaksTokenInURL,
			shape:  fake.GitHubLike,
			saying: "the credential is in a request URL",
		},
		{
			fault:  fake.FaultManglesWire,
			shape:  fake.GitHubLike,
			saying: "wire secret the verifier will not key its MAC with",
		},
		{
			fault:  fake.FaultWritableDeployKey,
			shape:  fake.GitHubLike,
			saying: "the installed deploy key can push",
		},
		{
			fault:  fake.FaultForgetsRemoval,
			shape:  fake.GitHubLike,
			saying: "still installed after a successful removal",
		},
		{
			fault:  fake.FaultMissingIsNone,
			shape:  fake.GitHubLike,
			saying: "must not answer an authorization question",
		},
		{
			fault:  fake.FaultSignsWithAnotherScheme,
			shape:  fake.GitHubLike,
			saying: "a correctly signed delivery was rejected",
		},
		{
			// The same one under Standard Webhooks, because that scheme's
			// failure path is a different one: no signature MATCHED rather than
			// no signature header.
			fault:  fake.FaultManglesWire,
			shape:  fake.GitLabLike,
			saying: "wire secret the verifier will not key its MAC with",
		},
	} {
		name := string(tc.fault)
		shape := faulty(tc.shape, tc.fault)
		if tc.plus != "" {
			name += "+" + string(tc.plus)
			shape = faulty(shape, tc.plus)
		}
		t.Run(name+"-on-"+tc.shape.Host, func(t *testing.T) {
			rec := record(subjectFor(t, "broken", shape))
			if !rec.failed {
				t.Fatalf("the suite passed a driver with fault %q", name)
			}
			if !strings.Contains(rec.String(), tc.saying) {
				t.Fatalf("the suite failed a driver with fault %q, but never said %q:\n%s",
					name, tc.saying, rec.String())
			}
		})
	}
}

// TestTheSuiteCatchesAnOverclaimedCapability is the failure that motivated
// comparing the capability set for EQUALITY rather than containment: a face
// whose method signature is one type wrong compiles, its type assertion fails
// in silence, and the capability disappears with nothing said. Here the same
// discrepancy is produced from the other side — a Subject declaring a
// capability its forge does not have — and the suite must name it.
func TestTheSuiteCatchesAnOverclaimedCapability(t *testing.T) {
	s := subjectFor(t, "overclaims", without(fake.GitHubLike, forge.CapPushHooks))
	s.Expect[forge.CapPushHooks] = struct{}{}

	rec := record(s)
	if !rec.failed {
		t.Fatal("the suite accepted a capability set that does not match the driver")
	}
	if !strings.Contains(rec.String(), "capabilities =") {
		t.Fatalf("the suite did not name the capability mismatch:\n%s", rec.String())
	}
}

// TestASubjectThatCannotBeRunIsFatalAndNotSkipped: every missing probe is a
// Fatalf, because a suite that quietly runs less than it says it does is worse
// than no suite.
func TestASubjectThatCannotBeRunIsFatalAndNotSkipped(t *testing.T) {
	for _, tc := range []struct {
		name   string
		break_ func(*Subject)
		saying string
	}{
		{"no-requests-probe", func(s *Subject) { s.Requests = nil }, "refused before the wire"},
		{"no-redirect-probe", func(s *Subject) { s.RedirectOnce = nil }, "where a request WENT is unobservable"},
		{"a-moved-fixture-that-did-not-move", func(s *Subject) {
			s.Moved = s.MovedTo
		}, "answer for one key and answer WITH another"},
		{"no-moved-fixture", func(s *Subject) { s.Moved = repokey.Key{} }, "fixture Moved is not set"},
		{"no-break-probe", func(s *Subject) { s.Break = nil }, "an outage is not a refusal"},
		{"no-expect", func(s *Subject) { s.Expect = nil }, "exact capability set"},
		{"no-registry", func(s *Subject) { s.Registry = nil }, "needs a Registry and a Host"},
		{"no-token", func(s *Subject) { s.Token = "" }, "raw credential the canary looks for"},
		{"fixture-on-another-host", func(s *Subject) {
			s.Writable.Host = "elsewhere.invalid"
		}, "is on host"},
		{"deep-fixture-is-not-deep", func(s *Subject) {
			s.Deep.Owner = "acme"
		}, "must have a nested owner"},
		{"no-hook-probe", func(s *Subject) { s.LastHookWire = nil }, "CapPushHooks needs LastHookWire"},
		{"no-deploy-key-probe", func(s *Subject) { s.DeployKeyInstalled = nil }, "CapDeployKeys needs"},
		{"no-delivery-builder", func(s *Subject) { s.Delivery = nil }, "CapSignatures needs"},
		{"no-membership-fixtures", func(s *Subject) { s.Member = "" }, "CapMembership needs"},
		{"no-push-probe-keys", func(s *Subject) { s.PushKey = nil }, "CapPushProbe needs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := subjectFor(t, tc.name, fake.GitHubLike)
			tc.break_(&s)
			rec := record(s)
			if !rec.failed || !strings.Contains(rec.String(), tc.saying) {
				t.Fatalf("a Subject missing %s did not stop the suite saying %q: failed=%v\n%s",
					tc.name, tc.saying, rec.failed, rec.String())
			}
		})
	}
}

// TestAtimestampedSchemeNeedsItsDatingBuilder is the one conditional
// requirement in the preflight, so it gets its own case: a scheme that signs a
// timestamp cannot have its replay window asserted without one.
func TestATimestampedSchemeNeedsItsDatingBuilder(t *testing.T) {
	s := subjectFor(t, "no-delivery-at", fake.GitLabLike)
	s.DeliveryAt = nil
	rec := record(s)
	if !rec.failed || !strings.Contains(rec.String(), "signs a timestamp") {
		t.Fatalf("a timestamped scheme with no DeliveryAt was accepted:\n%s", rec.String())
	}
}
