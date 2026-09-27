package node

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestForges(t *testing.T) {
	reg, err := forges(nil)
	require.NoError(t, err)
	require.Equal(t, []string{"github.com", "gitlab.com"}, reg.Hosts(), "the public forges when none are named")

	reg, err = forges([]Forge{
		{Host: "git.internal:8443", Driver: ForgeGitLab, Signing: true},
		{Host: "ghe.example.com", Driver: ForgeGitHub},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"ghe.example.com", "git.internal:8443"}, reg.Hosts(),
		"naming any replaces the defaults rather than adding to them")

	_, err = forges([]Forge{{Host: "gitlab.example.com", Driver: ForgeGitLab}})
	require.ErrorContains(t, err, "does not bind the body",
		"a replayed shared token is refused at startup unless signing is on")

	_, err = forges([]Forge{{Host: "code.example.com", Driver: "gitea"}})
	require.ErrorContains(t, err, `names driver "gitea"`)

	_, err = forges([]Forge{{Host: "GitHub.com", Driver: ForgeGitHub}})
	require.ErrorContains(t, err, `forge "GitHub.com"`)

	_, err = forges([]Forge{{Host: "Gitlab.com", Driver: ForgeGitLab}})
	require.ErrorContains(t, err, `forge "Gitlab.com"`)
}
