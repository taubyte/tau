package drivers

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/pkg/forge"
	publicforges "github.com/taubyte/tau/pkg/forge/public"
	"github.com/taubyte/tau/pkg/git/gittest"
)

func TestForges(t *testing.T) {
	reg, err := Registry(nil)
	require.NoError(t, err)
	require.Equal(t, []string{"github.com", "gitlab.com"}, reg.Hosts(), "the public forges when none are named")

	reg, err = Registry([]Forge{
		{Host: "git.internal:8443", Driver: GitLab, Signing: true},
		{Host: "ghe.example.com", Driver: GitHub},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"ghe.example.com", "git.internal:8443"}, reg.Hosts(),
		"naming any replaces the defaults rather than adding to them")

	// A GitLab that does not sign is served without hooks, not refused.
	reg, err = Registry([]Forge{{Host: "gitlab.example.com", Driver: GitLab}})
	require.NoError(t, err)
	drv, err := reg.Driver("gitlab.example.com")
	require.NoError(t, err)
	_, signs := drv.(forge.Signatures)
	require.False(t, signs, "a gitlab without Signing verifies no deliveries")
	_, err = Registry([]Forge{{Host: "gitlab.example.com", Driver: GitLab}}, "gitlab.example.com")
	require.NoError(t, err, "a gitlab people sign in through")

	reg, err = Registry([]Forge{
		{Host: "codeberg.org", Driver: Gitea},
		{Host: "git.internal:3000", Driver: Gitea},
	})
	require.NoError(t, err, "a Gitea, a Forgejo or Codeberg is the gitea driver bound to its host")
	require.Equal(t, []string{"codeberg.org", "git.internal:3000"}, reg.Hosts())
	drv, err = reg.Driver("codeberg.org")
	require.NoError(t, err)
	require.Equal(t, forge.SchemeHubSHA256, drv.(forge.Signatures).Scheme(),
		"a gitea hook signs like GitHub's, so the strict policy takes it with no opt-in")

	_, err = Registry([]Forge{{Host: "code.example.com", Driver: "fossil"}})
	require.ErrorContains(t, err, `names driver "fossil"`)

	// Every other driver binds, and only the ones whose forge signs verify
	// deliveries: those are the forges whose pushes trigger a build.
	reg, err = Registry([]Forge{
		{Host: "gogs.example.com", Driver: Gogs},
		{Host: "onedev.example.com", Driver: OneDev},
		{Host: "review.example.com", Driver: Gerrit},
		{Host: "git.sr.ht", Driver: Sourcehut},
		{Host: "git.example.org", Driver: Sourcehut},
		{Host: "srht.example.org", Driver: Sourcehut, PayloadKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="},
		{Host: "bitbucket.org", Driver: Bitbucket},
		{Host: "dev.azure.com", Driver: Azure, Organizations: []string{"acme"}},
		{Host: "bitbucket.example.com", Driver: BitbucketServer},
		{Host: "bbs.example.com", Driver: BitbucketServer, SSHPort: 2222},
	})
	require.NoError(t, err)
	// Both of a Bitbucket Server's addresses, on whatever ssh port it was
	// given, are one repository.
	for _, spelling := range []string{
		"https://bitbucket.example.com/scm/PROJ/repo.git", "ssh://git@bitbucket.example.com:7999/PROJ/repo.git",
	} {
		k, err := reg.Key(spelling)
		require.NoError(t, err)
		require.Equal(t, "bitbucket.example.com/proj/repo", k.String(), spelling)
	}
	k, err := reg.Key("ssh://git@bbs.example.com:2222/PROJ/repo.git")
	require.NoError(t, err)
	require.Equal(t, "bbs.example.com/proj/repo", k.String())
	_, err = Registry([]Forge{{Host: "bbs.example.com", Driver: BitbucketServer, SSHPort: -1}})
	require.ErrorContains(t, err, `forge "bbs.example.com"`)
	for host, want := range map[string]bool{
		"gogs.example.com": true, "onedev.example.com": false, "review.example.com": false,
		"git.sr.ht": true, "git.example.org": false, "srht.example.org": true,
		"bitbucket.org": true, "dev.azure.com": false, "bitbucket.example.com": true,
	} {
		d, err := reg.Driver(host)
		require.NoError(t, err)
		_, signs := d.(forge.Signatures)
		require.Equal(t, want, signs, host)
	}

	_, err = Registry([]Forge{{Host: "dev.azure.com", Driver: Azure}})
	require.ErrorContains(t, err, `forge "dev.azure.com"`, "azure names its organizations")
	_, err = Registry([]Forge{{Host: "srht.example.org", Driver: Sourcehut, PayloadKey: "short"}})
	require.ErrorContains(t, err, `forge "srht.example.org"`)

	_, err = Registry([]Forge{{Host: "Codeberg.org", Driver: Gitea}})
	require.ErrorContains(t, err, `forge "Codeberg.org"`)

	_, err = Registry([]Forge{{Host: "GitHub.com", Driver: GitHub}})
	require.ErrorContains(t, err, `forge "GitHub.com"`)

	_, err = Registry([]Forge{{Host: "Gitlab.com", Driver: GitLab}})
	require.ErrorContains(t, err, `forge "Gitlab.com"`)
}

func TestHostKeys(t *testing.T) {
	keys, err := HostKeys(nil)
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
	keys, err = HostKeys([]Forge{{Host: "bitbucket.org", Driver: Bitbucket}})
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

	keys, err = HostKeys([]Forge{
		{Host: "git.internal:8443", Driver: GitLab, Signing: true,
			HostKeys: []string{"git.internal " + knownHostKeys["gitlab.com"][0], knownHostKeys["github.com"][0]}},
		{Host: "ghe.example.com", Driver: GitHub},
	})
	require.NoError(t, err)
	require.Len(t, keys["git.internal:8443"], 2, "a known_hosts line and a bare key both read")
	require.Empty(t, keys["ghe.example.com"], "a forge that is not public and named no keys is cloned from by nothing")

	_, err = HostKeys([]Forge{{Host: "git.internal", Driver: GitHub, HostKeys: []string{"not a key"}}})
	require.ErrorContains(t, err, `forge "git.internal" host key`)
}

func TestAGerritBindingCarriesItsDeploymentCredentials(t *testing.T) {
	priv, pub := gittest.KeyPair(t)
	hostKey := string(gossh.MarshalAuthorizedKey(pub))
	events := &Events{Address: "review.example.com:29418", User: "tau-bot", Key: priv}

	reg, err := Registry([]Forge{{Host: "review.example.com", Driver: Gerrit, Membership: "admin:pw", Events: events, HostKeys: []string{hostKey}}})
	require.NoError(t, err)
	require.Equal(t, []string{"review.example.com"}, reg.EventHosts())
	drv, err := reg.Driver("review.example.com")
	require.NoError(t, err)
	_, members := drv.(forge.Memberships)
	require.True(t, members)

	reg, err = Registry([]Forge{{Host: "review.example.com", Driver: Gerrit}})
	require.NoError(t, err)
	require.Empty(t, reg.EventHosts(), "a gerrit with no Events triggers nothing")

	_, err = Registry([]Forge{{Host: "review.example.com", Driver: Gerrit, Events: events}})
	require.ErrorContains(t, err, "host key pinned", "a stream with no pinned host key is refused at boot")
	_, err = Registry([]Forge{{Host: "review.example.com", Driver: Gerrit, Events: events, HostKeys: []string{"garbage"}}})
	require.Error(t, err)
}

func TestTheCLIsPublicForgesSignAsTheDefaultCloudVerifies(t *testing.T) {
	cloud, err := Registry(nil)
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

func TestAForgeBehindAPrivateCATrustsOnlyIt(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))

	for _, driver := range names {
		f := Forge{Host: "git.example.com", Driver: driver, CA: ca, Organizations: []string{"acme"}}
		_, err := Registry([]Forge{f})
		require.NoError(t, err, "%s takes a private root", driver)
	}

	_, err = Registry([]Forge{{Host: "git.example.com", Driver: GitHub, CA: "not a certificate"}})
	require.ErrorContains(t, err, "holds no certificate")
}
