package forgeauth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/services/build/frontend"
)

// EVERY SHAPE, because the point of this adapter is that it names none: the
// same Identity answers a GitHub, a GitLab, a Gitea and a Bitbucket the same
// way, and a shape it answered differently would be a provider it knows.
func TestEveryForgeAnswersTheSameQuestionTheSameWay(t *testing.T) {
	for _, shape := range []fake.Shape{fake.GitHubLike, fake.GitLabLike, fake.GiteaLike, fake.BitbucketLike} {
		t.Run(shape.Host, func(t *testing.T) {
			f := fake.New(shape)
			reg, err := f.Registry(forge.Policy{})
			require.NoError(t, err)
			id, err := New(reg)
			require.NoError(t, err)
			keys, token := f.Keys(), f.TokenString()
			ctx := t.Context()

			for _, c := range []struct {
				name  string
				token string
				key   repokey.Key
				want  frontend.Access
			}{
				{"a writer", token, keys.Writable, frontend.AccessGranted},
				{"an admin", token, keys.Admin, frontend.AccessGranted},
				{"read is not write", token, keys.ReadOnly, frontend.AccessDenied},
				{"no grant", token, keys.NoAccess, frontend.AccessDenied},
				{"no such repository reads as a denial", token, keys.Missing, frontend.AccessDenied},
				{"a refused credential is not a denial", f.BadTokenString(), keys.Writable, frontend.AccessUnauthenticated},
				{"no credential at all", "", keys.Writable, frontend.AccessUnauthenticated},
			} {
				got, err := id.WriteAccess(ctx, c.token, c.key)
				require.NoError(t, err, c.name)
				require.Equal(t, c.want, got, c.name)
			}

			// AN OUTAGE IS NOT A DENIAL.
			f.Break()
			_, err = id.WriteAccess(ctx, token, keys.Writable)
			require.Error(t, err, "a forge that could not answer answered")
			f.Unbreak()
		})
	}
}

// A HOST THIS CLOUD SERVES NO FORGE FOR IS AN ERROR, never a denial: answering
// "denied" for a repository nobody here can ask about would be a decision
// nobody made.
func TestAnUnservedHostIsNotADenial(t *testing.T) {
	reg, err := fake.New(fake.GitHubLike).Registry(forge.Policy{})
	require.NoError(t, err)
	id, err := New(reg)
	require.NoError(t, err)

	key, err := repokey.Parse("https://codeberg.org/acme/pay")
	require.NoError(t, err)
	_, err = id.WriteAccess(t.Context(), "tok", key)
	var none *forge.NoDriverError
	require.ErrorAs(t, err, &none)

	_, err = New(nil)
	require.Error(t, err)
}

func TestTheClaimantIsTheForgesStableSubject(t *testing.T) {
	f := fake.New(fake.GitHubLike)
	reg, err := f.Registry(forge.Policy{})
	require.NoError(t, err)
	forges, err := New(reg)
	require.NoError(t, err)

	who, err := forges.Claimant(t.Context(), f.TokenString(), f.Host())
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(who, f.Host()+"/"), who)
	require.Greater(t, len(who), len(f.Host())+1, "a subject follows the host")

	_, err = forges.Claimant(t.Context(), f.BadTokenString(), f.Host())
	require.ErrorIs(t, err, frontend.ErrBadCredential)
	_, err = forges.Claimant(t.Context(), "", f.Host())
	require.ErrorIs(t, err, frontend.ErrBadCredential)
	_, err = forges.Claimant(t.Context(), f.TokenString(), "codeberg.org")
	require.ErrorIs(t, err, frontend.ErrUnknownForge, "a token is never sent to a forge this cloud does not serve")
}
