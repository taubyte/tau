package fake_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
)

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

func TestDeliveriesNeedAHookAndNotAHandle(t *testing.T) {
	f := fake.New(fake.GitHubLike)
	_, err := f.Delivery(forge.Hook{Handle: "never-registered"}, "refs/heads/main", "abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no secret or no scheme")

	secret, err := forge.NewSecret()
	require.NoError(t, err)
	_, err = f.Delivery(forge.Hook{
		Handle: "a-handle-this-forge-never-assigned",
		Key:    f.Keys().Admin,
		Scheme: forge.SchemeHubSHA256,
		Secret: secret,
	}, "refs/heads/main", "1f0c9d2b3a4e5f60718293a4b5c6d7e8f9012345")
	require.NoError(t, err, "a hook with a secret and a scheme is signable wherever its handle came from")
}
