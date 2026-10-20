package forgetest

import (
	"testing"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
)

// TestEveryShapePasses runs the suite against an in-memory forge wearing each
// of the shapes the four real forges have between them. It is the positive half
// of proving the suite is runnable; the negative half — that it CATCHES a
// driver that is wrong — is proofs_test.go, and matters more.
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

// THE NEGATIVE HALF IS NOT HERE ANY MORE, AND THAT IS THE POINT.
//
// It used to be a table beside this one: a fault, a shape, and a fragment the
// suite had to say somewhere. Two tables of the same subjects is how the last
// defect hid — a case passed on a fragment that was a SUBSTRING of a message
// some other assertion emits, so it was green on an assertion it was not
// written for, while the assertion it named never ran at all.
//
// So there is one table, in proofs_test.go, and the ledger holds it to
// something stricter than "said this somewhere": the COMPLETE set of complaints
// the suite makes about each broken subject must be exactly the set that proof
// names. Everything the old table asserted is asserted there, and the fragments
// are checked to name one assertion each.
