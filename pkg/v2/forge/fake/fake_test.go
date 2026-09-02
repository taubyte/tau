package fake_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/v2/forge"
	"github.com/taubyte/tau/pkg/v2/forge/fake"
)

// The conformance suite is where this forge is really exercised
// (pkg/v2/forge/forgetest). What is left here is the handful of things a
// consumer meets before the suite does: how it refuses a shape that cannot
// exist, and what an anonymous session can and cannot see.

func TestAShapeThatCannotExistIsRefusedLoudly(t *testing.T) {
	for name, sh := range map[string]fake.Shape{
		"no host":                                {Scheme: forge.SchemeHubSHA256},
		"a forge that signs but names no scheme": {Host: "fake.nameless"},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("a shape that cannot be built was built anyway")
				}
			}()
			fake.New(sh)
		})
	}
}

// TestAnOmissionPatternThatIsNotATypeIsAnError: a Go type either implements an
// interface or it does not, so the powerset of optional faces is not
// constructible. The three patterns that ARE types cover both contracts the
// suite runs; anything else is refused where a client is built, rather than
// quietly handing back a different forge than was asked for.
func TestAnOmissionPatternThatIsNotATypeIsAnError(t *testing.T) {
	f := fake.New(fake.Shape{
		Host:   "fake.partial",
		Scheme: forge.SchemeHubSHA256,
		Omit:   []forge.Capability{forge.CapList, forge.CapCreate},
	})
	_, err := f.Driver().Bind(f.Token())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a shape that exists")
}

func TestBindRefusesACredentialNobodySet(t *testing.T) {
	f := fake.New(fake.GitHubLike)
	_, err := f.Driver().Bind(forge.Credential{})
	require.ErrorIs(t, err, forge.ErrNoCredential)

	c, err := f.Driver().Bind(f.Token())
	require.NoError(t, err)
	assert.Equal(t, f.Host(), c.Host())
}

// TestAnAnonymousSessionSeesOnlyWhatIsPublic: the anonymous guard lives in the
// core and stops the authorization questions, but a public repository is still
// a public repository, and a private one is not merely refused — it is not
// there.
func TestAnAnonymousSessionSeesOnlyWhatIsPublic(t *testing.T) {
	f := fake.New(fake.GitHubLike)
	reg, err := f.Registry(forge.Policy{})
	require.NoError(t, err)
	k := f.Keys()

	sess, err := reg.For(k.ReadOnly, forge.Anonymous())
	require.NoError(t, err)
	repo, err := sess.Repository(context.Background(), k.ReadOnly)
	require.NoError(t, err)
	assert.Equal(t, k.ReadOnly, repo.Key)
	assert.False(t, repo.Private)

	sess, err = reg.For(k.Writable, forge.Anonymous())
	require.NoError(t, err)
	_, err = sess.Repository(context.Background(), k.Writable)
	var nf *forge.NotFoundError
	require.ErrorAs(t, err, &nf, "a private repository was described to nobody in particular")
}

// TestAShapeThatSignsNothingBindingIsRefusedByTheZeroPolicy: an instance older
// than GitLab 19.0 offers only a replayed token, and accepting one has to be
// said out loud.
func TestAShapeThatSignsNothingBindingIsRefusedByTheZeroPolicy(t *testing.T) {
	f := fake.New(fake.GitLabOldLike)
	_, err := f.Registry(forge.Policy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AllowUnboundSchemes")

	_, err = f.Registry(forge.Policy{AllowUnboundSchemes: true})
	require.NoError(t, err)
}

// TestDeliveriesNeedAHookThisForgeHolds: the builders sign for a hook the forge
// actually registered, so a test cannot accidentally verify against a hook that
// was never installed.
func TestDeliveriesNeedAHookThisForgeHolds(t *testing.T) {
	f := fake.New(fake.GitHubLike)
	_, err := f.Delivery(forge.Hook{Handle: "never-registered"}, "refs/heads/main", "abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no hook")
}
