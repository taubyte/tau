package drive

import (
	"encoding/base64"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"
	"gotest.tools/v3/assert"

	"github.com/taubyte/tau/node"
	"github.com/taubyte/tau/node/boot"
	"github.com/taubyte/tau/services/auth"
	"github.com/taubyte/tau/services/auth/password"
	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/tools/spore-drive/config"
	"github.com/taubyte/tau/tools/spore-drive/config/fixtures"
)

const signIn = `
auth:
    admin: {name: root, password: correct-horse}
    signup: open
    redirects: [https://console.test.com/callback]
    forges:
        - {host: github.com, driver: github, clientid: gh-id, clientsecret: gh-secret}
        - {host: git.test.com, driver: gitea}
    oidc:
        - {name: corp, issuer: https://id.test.com, clientid: corp-id, clientsecret: corp-secret, scopes: [email]}
cm:
    acme: {directory: https://ca.test.com/dir, email: ops@test.com, eab: {kid: k, hmac: aG1hYw}, accountkey: keys/acme.key}
    domains: {trusted: true}
`

func withCloud(t *testing.T, fs afero.Fs, p config.Parser, extra string) config.Parser {
	t.Helper()
	assert.NilError(t, p.Sync())
	f, err := fs.OpenFile("/cloud.yaml", os.O_APPEND|os.O_WRONLY, 0)
	assert.NilError(t, err)
	_, err = f.WriteString(extra)
	assert.NilError(t, err)
	assert.NilError(t, f.Close())
	p, err = config.New(fs, "/")
	assert.NilError(t, err)
	return p
}

func TestSignInAndCertificatesReachEveryNodeAndTheirSecretsOnlyTheSeed(t *testing.T) {
	fs, p := fixtures.VirtConfig()
	assert.NilError(t, afero.WriteFile(fs, "/keys/acme.key", []byte("ACCOUNT KEY"), 0o600))
	p = withCloud(t, fs, p, signIn)

	first, err := driveFor(p).nodesFor("host1")
	assert.NilError(t, err)
	for _, inst := range first.instances {
		n := inst.file.Node
		assert.Equal(t, n.Auth.Admin.Name, "root")
		hash, err := password.Parse(n.Auth.Admin.PasswordHash)
		assert.NilError(t, err)
		assert.NilError(t, hash.Verify("correct-horse", nil))
		assert.Equal(t, n.Auth.SignUp, auth.SignUpOpen)
		assert.DeepEqual(t, n.Auth.Redirects, []string{"https://console.test.com/callback"})
		assert.DeepEqual(t, n.Auth.Forges, []auth.ForgeApp{{Host: "github.com", ClientID: "gh-id"}})
		assert.DeepEqual(t, n.Auth.OIDC, []auth.OIDCProvider{{Name: "corp", Issuer: "https://id.test.com", ClientID: "corp-id", Scopes: []string{"email"}}})
		assert.DeepEqual(t, n.Forges, []node.Forge{{Host: "github.com", Driver: "github"}, {Host: "git.test.com", Driver: "gitea"}})
		acme := n.CM.Certificate.ACME
		assert.Assert(t, acme.Enabled)
		assert.Equal(t, acme.Directory, "https://ca.test.com/dir")
		assert.Equal(t, acme.Email, "ops@test.com")
		assert.Equal(t, acme.EAB.KeyID, "k")
		assert.Assert(t, n.CM.Domains.Trusted)

		raw, err := yaml.Marshal(inst.file)
		assert.NilError(t, err)
		for _, secret := range []string{"correct-horse", "gh-secret", "corp-secret", "ACCOUNT KEY"} {
			assert.Assert(t, !strings.Contains(string(raw), secret), "%s is in %s's file", secret, inst.name)
		}
	}

	secrets := []boot.Secret{
		{Project: auth.VaultProject, Environment: auth.VaultEnvironment, Name: auth.OAuthSecretName("github.com"), Value: "gh-secret"},
		{Project: auth.VaultProject, Environment: auth.VaultEnvironment, Name: auth.OIDCSecretName("corp"), Value: "corp-secret"},
		{Project: cm.VaultProject, Environment: cm.VaultEnvironment, Name: cm.AccountSecret, Value: "ACCOUNT KEY"},
	}
	second, err := driveFor(p).nodesFor("host2")
	assert.NilError(t, err)
	holders := []string{first.instances[0].file.PrivateKey, second.instances[0].file.PrivateKey}
	for i, id := range holders {
		holders[i] = peerID(t, id)
	}
	slices.Sort(holders)

	// EVERY HOST'S NODE HOLDS THE SEED, so a writer that dies is replaced.
	for _, main := range []instance{first.instances[0], second.instances[0]} {
		assert.Equal(t, main.file.Seed, "/tb/config/node.seed.yaml")
		assert.DeepEqual(t, *main.seed, boot.SeedFile{Holders: holders, Secrets: secrets})
	}
	assert.Assert(t, first.instances[1].seed == nil, "a split-off frontend is not a holder")
}

func peerID(t *testing.T, key string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(key)
	assert.NilError(t, err)
	priv, err := crypto.UnmarshalPrivateKey(raw)
	assert.NilError(t, err)
	id, err := peer.IDFromPrivateKey(priv)
	assert.NilError(t, err)
	return id.String()
}

func TestASeedIsInstalledAndNoCopyStaysInTmp(t *testing.T) {
	_, p := fixtures.VirtConfig()
	inst := instance{name: "node", seed: &boot.SeedFile{Holders: []string{"a"}, Secrets: []boot.Secret{{Project: "p", Environment: "e", Name: "n", Value: "v"}}}}
	fs := afero.NewMemMapFs()
	h := &fsHost{fs: fs, sudo: func(name string, args ...string) error {
		assert.DeepEqual(t, append([]string{name}, args...), []string{"cp", "-f", "/tmp/node.seed.yaml", "/tb/config/"})
		data, err := afero.ReadFile(fs, args[1])
		if err != nil {
			return err
		}
		return afero.WriteFile(fs, "/tb/config/node.seed.yaml", data, 0o600)
	}}
	assert.NilError(t, driveFor(p).installSeed(t.Context(), h, inst))
	data, err := afero.ReadFile(fs, "/tb/config/node.seed.yaml")
	assert.NilError(t, err)
	var back boot.SeedFile
	assert.NilError(t, yaml.Unmarshal(data, &back))
	assert.DeepEqual(t, back, *inst.seed)
	_, err = fs.Stat("/tmp/node.seed.yaml")
	assert.Assert(t, os.IsNotExist(err), "no copy stays in /tmp")

	failing := &fsHost{fs: afero.NewMemMapFs(), sudo: func(string, ...string) error { return errors.New("no sudo") }}
	assert.ErrorContains(t, driveFor(p).installSeed(t.Context(), failing, inst), "failed to copy")
	_, err = failing.fs.Stat("/tmp/node.seed.yaml")
	assert.Assert(t, os.IsNotExist(err), "nor after a failed copy")

	ro := &fsHost{fs: afero.NewReadOnlyFs(afero.NewMemMapFs())}
	assert.ErrorContains(t, driveFor(p).installSeed(t.Context(), ro, inst), "failed to create")
}

func TestNoSignInOrCertificatesSeedsNothing(t *testing.T) {
	_, p := fixtures.VirtConfig()
	nodes, err := driveFor(p).nodesFor("host1")
	assert.NilError(t, err)
	assert.Assert(t, nodes.instances[0].seed == nil && nodes.instances[0].file.Seed == "")
	assert.Assert(t, !nodes.instances[0].file.Node.CM.Certificate.ACME.Enabled)
	assert.Assert(t, nodes.instances[0].file.Node.Forges == nil)
}

func TestWhatSignInAndCertificatesRefuse(t *testing.T) {
	for name, extra := range map[string]string{
		"an admin without a password": "auth:\n    admin: {name: root}\n",
		"an account key not there":    "cm:\n    acme: {accountkey: keys/none.key}\n",
		"auth that is not a map":      "auth: [1]\n",
		"cm that is not a map":        "cm: [1]\n",
	} {
		fs, p := fixtures.VirtConfig()
		p = withCloud(t, fs, p, extra)
		_, err := driveFor(p).nodesFor("host1")
		assert.Assert(t, err != nil, name)
	}
}

func TestACertificateNodeKnowsTheZone(t *testing.T) {
	_, p := fixtures.VirtConfig()
	assert.NilError(t, p.Shapes().Shape("shape1").Services().Set("cm"))
	assert.NilError(t, p.Shapes().Shape("shape2").Services().Set("tns"))
	nodes, err := driveFor(p).nodesFor("host1")
	assert.NilError(t, err)
	f := nodes.instances[0].file
	assert.DeepEqual(t, f.Node.Services, []string{node.CM, node.Register, node.Discovery})
	assert.Equal(t, f.Node.Gateway.CloudFqdn, "gtest.com")
	assert.Equal(t, f.HTTP, "", "only a gateway serves http")
}
