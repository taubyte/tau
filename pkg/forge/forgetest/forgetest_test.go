package forgetest

import (
	"testing"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
)

func TestEveryShapePasses(t *testing.T) {
	type testCase struct {
		name  string
		shape fake.Shape
	}
	for _, tc := range []testCase{
		{"github-like", fake.GitHubLike},
		{"gitlab-like-nested-standard-webhooks", fake.GitLabLike},
		{"gitea-like", fake.GiteaLike},
		{"bitbucket-like-uuid-handles-no-clone-url", fake.BitbucketLike},
		{"a-host-with-nothing-but-the-three-mandatory-answers", fake.Minimal},
	} {
		Run(t, subjectFor(t, tc.name, tc.shape))
	}
}

func TestAbsenceIsATestedContract(t *testing.T) {
	for _, c := range []forge.Capability{
		forge.CapList, forge.CapCreate, forge.CapDelete, forge.CapDeployKeys,
		forge.CapPushHooks, forge.CapCloneAddr, forge.CapPushProbe,
		forge.CapSignatures, forge.CapMembership,
	} {
		Run(t, subjectFor(t, "without-"+string(c), without(fake.GitHubLike, c)))
	}
}
