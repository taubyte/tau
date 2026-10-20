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

	// A GitLab that does not sign is served without hooks, not refused.
	reg, err = forges([]Forge{{Host: "gitlab.example.com", Driver: ForgeGitLab}})
	require.NoError(t, err)
	drv, err := reg.Driver("gitlab.example.com")
	require.NoError(t, err)
	_, signs := drv.(forge.Signatures)
	require.False(t, signs, "a gitlab without Signing verifies no deliveries")

	reg, err = forges([]Forge{
		{Host: "codeberg.org", Driver: ForgeGitea},
		{Host: "git.internal:3000", Driver: ForgeGitea},
	})
	require.NoError(t, err, "a Gitea, a Forgejo or Codeberg is the gitea driver bound to its host")
	require.Equal(t, []string{"codeberg.org", "git.internal:3000"}, reg.Hosts())
	drv, err = reg.Driver("codeberg.org")
	require.NoError(t, err)
	require.Equal(t, forge.SchemeHubSHA256, drv.(forge.Signatures).Scheme(),
		"a gitea hook signs like GitHub's, so the strict policy takes it with no opt-in")

	_, err = forges([]Forge{{Host: "code.example.com", Driver: "fossil"}})
	require.ErrorContains(t, err, `names driver "fossil"`)

	// Every other driver binds, and only the ones whose forge signs verify
	// deliveries: those are the forges whose pushes trigger a build.
	reg, err = forges([]Forge{
		{Host: "gogs.example.com", Driver: ForgeGogs},
		{Host: "onedev.example.com", Driver: ForgeOneDev},
		{Host: "review.example.com", Driver: ForgeGerrit},
		{Host: "git.sr.ht", Driver: ForgeSourcehut},
		{Host: "git.example.org", Driver: ForgeSourcehut},
		{Host: "srht.example.org", Driver: ForgeSourcehut, PayloadKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="},
		{Host: "bitbucket.org", Driver: ForgeBitbucket},
		{Host: "dev.azure.com", Driver: ForgeAzure, Organizations: []string{"acme"}},
	})
	require.NoError(t, err)
	for host, want := range map[string]bool{
		"gogs.example.com": true, "onedev.example.com": false, "review.example.com": false,
		"git.sr.ht": true, "git.example.org": false, "srht.example.org": true,
		"bitbucket.org": true, "dev.azure.com": false,
	} {
		d, err := reg.Driver(host)
		require.NoError(t, err)
		_, signs := d.(forge.Signatures)
		require.Equal(t, want, signs, host)
	}

	_, err = forges([]Forge{{Host: "dev.azure.com", Driver: ForgeAzure}})
	require.ErrorContains(t, err, `forge "dev.azure.com"`, "azure names its organizations")
	_, err = forges([]Forge{{Host: "srht.example.org", Driver: ForgeSourcehut, PayloadKey: "short"}})
	require.ErrorContains(t, err, `forge "srht.example.org"`)

	_, err = forges([]Forge{{Host: "Codeberg.org", Driver: ForgeGitea}})
	require.ErrorContains(t, err, `forge "Codeberg.org"`)

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

	// bitbucket.org is not a default forge, but a node that names it with no
	// keys of its own gets the three it publishes, each matched to the
	// fingerprint Atlassian publishes separately.
	keys, err = hostKeys([]Forge{{Host: "bitbucket.org", Driver: ForgeBitbucket}})
	require.NoError(t, err)
	var got []string
	for _, k := range keys["bitbucket.org"] {
		got = append(got, gossh.FingerprintSHA256(k))
	}
	require.Equal(t, []string{
		"SHA256:ybgmFkzwOSotHTHLJgHO0QN8L0xErw6vd0VhFA9m3SM",
		"SHA256:FC73VB6C4OQLSCrjEayhMp9UMxS97caD/Yyi2bhW/J0",
		"SHA256:46OSHA1Rmj8E8ERTC6xkNcmGOw9oFxYr0WF6zWW8l1E",
	}, got)

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
