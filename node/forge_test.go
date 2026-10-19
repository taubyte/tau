package node

import (
	"testing"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
	publicforges "github.com/taubyte/tau/pkg/forge/public"

	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
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

// EVERY FORGE A NODE SERVES IS CLONED FROM ONLY BY ITS PINNED HOST KEYS: the
// public forges by the keys they publish, any other by the keys its operator
// named, and a forge with none by nothing at all.
func TestHostKeys(t *testing.T) {
	keys, err := hostKeys(nil)
	require.NoError(t, err)
	require.Len(t, keys["github.com"], 3)
	require.Len(t, keys["gitlab.com"], 3)
	for host, want := range map[string]string{
		"github.com": "SHA256:+DiY3wvvV6TuJJhbpZisF/zLDA0zPMSvHdkr4UvCOqU",
		"gitlab.com": "SHA256:eUXGGm1YGsMAS7vkcx6JOJdOGHPem5gQp4taiCfCLB8",
	} {
		var got []string
		for _, k := range keys[host] {
			got = append(got, gossh.FingerprintSHA256(k))
		}
		require.Contains(t, got, want, "%s's published ed25519 fingerprint", host)
	}

	keys, err = hostKeys([]Forge{
		{Host: "git.internal:8443", Driver: ForgeGitLab, Signing: true,
			HostKeys: []string{"git.internal " + knownHostKeys["gitlab.com"][0], knownHostKeys["github.com"][0]}},
		{Host: "ghe.example.com", Driver: ForgeGitHub},
	})
	require.NoError(t, err)
	require.Len(t, keys["git.internal:8443"], 2, "a known_hosts line and a bare key both read")
	require.Empty(t, keys["ghe.example.com"], "a forge that is not public and named no keys is cloned from by nothing")

	_, err = hostKeys([]Forge{{Host: "git.internal", Driver: ForgeGitHub, HostKeys: []string{"not a key"}}})
	require.ErrorContains(t, err, `forge "git.internal" host key`)
}

func TestAnInjectedForgeRegistryWins(t *testing.T) {
	var o options
	require.Error(t, WithForges(nil)(&o))

	reg, err := fake.New(fake.GitHubLike).Registry(forge.Policy{})
	require.NoError(t, err)
	require.NoError(t, WithForges(reg)(&o))

	got, err := (&Node{opts: o}).forgeRegistry()
	require.NoError(t, err)
	require.Same(t, reg, got)

	got, err = (&Node{}).forgeRegistry()
	require.NoError(t, err)
	require.NotSame(t, reg, got)
}

// THE CLI SIGNS WHAT THE CLOUD VERIFIES. `tau link` installs a hook through
// pkg/forge/public, deriving the wire secret under each driver's scheme; a
// cloud with no forges declared verifies under DefaultForges. The two must
// agree on every host, or every delivery a developer's link installs fails.
func TestTheCLIsPublicForgesSignAsTheDefaultCloudVerifies(t *testing.T) {
	cloud, err := forges(nil)
	require.NoError(t, err)
	cli, err := publicforges.Registry(forge.Policy{})
	require.NoError(t, err)
	require.Equal(t, cloud.Hosts(), cli.Hosts())
	for _, host := range cloud.Hosts() {
		want, err := cloud.Driver(host)
		require.NoError(t, err)
		got, err := cli.Driver(host)
		require.NoError(t, err)
		require.Equal(t, want.(forge.Signatures).Scheme(), got.(forge.Signatures).Scheme(), host)
	}
}
