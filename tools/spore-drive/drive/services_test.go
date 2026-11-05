package drive

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/taubyte/tau/services/builder"

	"github.com/taubyte/tau/services/plm/ingress"

	"github.com/taubyte/tau/pkg/forge/drivers"

	"github.com/spf13/afero"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"
	"gotest.tools/v3/assert"

	"github.com/taubyte/tau/instance"
	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	"github.com/taubyte/tau/services/auth"
	"github.com/taubyte/tau/services/auth/password"
	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/services/secrets"
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

func TestSignInAndCertificatesReachEveryNodeAndTheirSecretsOnlyTheBundle(t *testing.T) {
	fs, p := fixtures.VirtConfig()
	assert.NilError(t, afero.WriteFile(fs, "/keys/acme.key", []byte("ACCOUNT KEY"), 0o600))
	p = withCloud(t, fs, p, signIn)

	first, err := driveFor(p).nodesFor("host1")
	assert.NilError(t, err)
	for _, inst := range first.instances {
		n := inst.file.Node
		assert.Equal(t, n.Auth.Admin.Name, "root")
		assert.Equal(t, n.Auth.SignUp, auth.SignUpOpen)
		assert.DeepEqual(t, n.Auth.Redirects, []string{"https://console.test.com/callback"})
		assert.DeepEqual(t, n.Auth.Forges, []auth.ForgeApp{{Host: "github.com", ClientID: "gh-id"}})
		assert.DeepEqual(t, n.Auth.OIDC, []auth.OIDCProvider{{Name: "corp", Issuer: "https://id.test.com", ClientID: "corp-id", Scopes: []string{"email"}}})
		assert.DeepEqual(t, n.Forges, []drivers.Forge{{Host: "github.com", Driver: "github"}, {Host: "git.test.com", Driver: "gitea"}})
		acme := n.CM.Certificate.ACME
		assert.Assert(t, acme.Enabled)
		assert.Equal(t, acme.Directory, "https://ca.test.com/dir")
		assert.Equal(t, acme.Email, "ops@test.com")
		assert.Equal(t, acme.EAB.KeyID, "k")
		var built builder.Config
		assert.NilError(t, n.Section(instance.Builder, &built))
		assert.Assert(t, built.Domains.Trusted, "a trusted cloud's builder serves declared zones unproven")
		var public ingress.Config
		assert.NilError(t, n.Section(instance.PLM, &public))
		assert.DeepEqual(t, public.Origins, []string{"https://console.test.com"})

		raw, err := yaml.Marshal(inst.file)
		assert.NilError(t, err)
		for _, secret := range []string{"correct-horse", "$argon2id$", "gh-secret", "corp-secret", "ACCOUNT KEY"} {
			assert.Assert(t, !strings.Contains(string(raw), secret), "%s is in %s's file", secret, inst.name)
		}
		assert.Equal(t, strings.Count(string(raw), "driver:"), 2, "a forge's driver is said once, in node.forges")
	}

	d := driveFor(p)
	sealed, err := d.sealSecrets()
	assert.NilError(t, err)
	var bundle secretsv2.InitBundle
	assert.NilError(t, proto.Unmarshal(sealed, &bundle))
	var refs []string
	opened := map[string]string{}
	for _, s := range bundle.GetSecrets() {
		_, plain, err := secrets.Unsealed(s)
		assert.NilError(t, err)
		opened[s.GetRef().GetName()] = string(plain)
		refs = append(refs, s.GetRef().GetScope().GetCloud().GetService()+"/"+s.GetRef().GetName())
		assert.Assert(t, s.GetCid() != "" && len(s.GetDek()) > 0)
	}
	hash, err := password.Parse(opened[auth.AdminSecret])
	assert.NilError(t, err)
	assert.NilError(t, hash.Verify("correct-horse", nil))
	assert.Equal(t, hash.Pepper, "", "every node verifies the admin, so no node's pepper is in it")
	assert.DeepEqual(t, refs, []string{
		auth.VaultService + "/" + auth.AdminSecret,
		auth.VaultService + "/" + auth.OAuthSecretName("github.com"),
		auth.VaultService + "/" + auth.OIDCSecretName("corp"),
		cm.VaultService + "/" + cm.AccountSecret,
	})
	for _, secret := range []string{"correct-horse", "$argon2id$", "gh-secret", "corp-secret", "ACCOUNT KEY"} {
		assert.Assert(t, !strings.Contains(string(sealed), secret), "%s is sealed in the clear", secret)
	}

	second, err := d.sealSecrets()
	assert.NilError(t, err)
	assert.Assert(t, !bytes.Equal(sealed, second), "every deploy seals afresh")

	assert.Assert(t, first.instances[0].keepsSecrets())
	assert.Equal(t, len(first.instances), 1, "one node per host")
}

// fixedNow is the clock the deploy tests run on.
var fixedNow = time.Date(2026, 10, 22, 9, 30, 0, 0, time.UTC)

func TestTheSecretsReachInitOnStdinAndNowhereElse(t *testing.T) {
	_, p := fixtures.VirtConfig()
	d := driveFor(p)
	fs := afero.NewMemMapFs()
	var got []string
	var stdin []byte
	h := &fsHost{fs: fs, input: func(in []byte, name string, args ...string) error {
		got, stdin = append([]string{name}, args...), in
		return nil
	}}
	inst := unit{name: "shape1"}
	assert.NilError(t, d.initSecrets(t.Context(), h, inst, []byte("sealed")))
	assert.DeepEqual(t, got, []string{"/tb/bin/tau", "init", "--config", "/tb/config/shape1.yaml", "--root", "/tb/storage/shape1", "-"})
	assert.Equal(t, string(stdin), "sealed")
	files, err := afero.ReadDir(fs, "/")
	assert.NilError(t, err)
	assert.Equal(t, len(files), 0, "nothing was written on the host")

	failing := &fsHost{fs: fs, input: func([]byte, string, ...string) error { return errors.New("no init") }}
	assert.ErrorContains(t, d.initSecrets(t.Context(), failing, inst, nil), "initializing tau@shape1")
}

func TestAV1NodesDataIsMovedAside(t *testing.T) {
	_, p := fixtures.VirtConfig()
	d := driveFor(p)
	d.now = func() time.Time { return fixedNow }

	type testCase struct {
		storage, v2, fails, moved bool
	}
	for name, tc := range map[string]testCase{
		"no data":       {},
		"v1's data":     {storage: true, moved: true},
		"a v2 node's":   {storage: true, v2: true},
		"a failed move": {storage: true, fails: true},
	} {
		t.Run(name, func(t *testing.T) {
			var moved []string
			h := &fsHost{fs: afero.NewMemMapFs(), sudo: func(name string, args ...string) error {
				switch name {
				case "test":
					assert.DeepEqual(t, args, []string{"-d", "/tb/storage/shape1"})
					if !tc.storage {
						return errors.New("absent")
					}
				case "grep":
					assert.DeepEqual(t, args, []string{"-qs", "^node:", "/tb/config/shape1.yaml"})
					if !tc.v2 {
						return errors.New("not a v2 node file")
					}
				case "mv":
					moved = args
					if tc.fails {
						return errors.New("busy")
					}
				}
				return nil
			}}
			err := d.moveAsideV1(t.Context(), h, "shape1")
			if tc.fails {
				assert.ErrorContains(t, err, "moving /tb/storage/shape1 aside")
				return
			}
			assert.NilError(t, err)
			if tc.moved {
				assert.DeepEqual(t, moved, []string{"/tb/storage/shape1", "/tb/storage/shape1.v1-20261022T093000"})
			} else {
				assert.Assert(t, moved == nil)
			}
		})
	}
}

func TestNoSignInOrCertificatesSealsNothing(t *testing.T) {
	_, p := fixtures.VirtConfig()
	nodes, err := driveFor(p).nodesFor("host1")
	assert.NilError(t, err)
	sealed, err := driveFor(p).sealSecrets()
	assert.NilError(t, err)
	assert.Assert(t, sealed == nil, "nothing to hand a node")
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
	assert.DeepEqual(t, f.Node.Services, []string{instance.CM, instance.Register, instance.Discovery})
	assert.Equal(t, f.Node.CloudFqdn, "gtest.com")
	assert.Equal(t, f.HTTP, "", "only a gateway serves http")
}

func TestSealingRefusesWhatCannotBeASecret(t *testing.T) {
	for name, extra := range map[string]string{
		"auth that is not a map": "auth: [1]\n",
		"a name with a slash":    "auth:\n    forges:\n        - {host: a/b, clientid: id, clientsecret: s}\n",
	} {
		fs, p := fixtures.VirtConfig()
		p = withCloud(t, fs, p, extra)
		_, err := driveFor(p).sealSecrets()
		assert.Assert(t, err != nil, name)
	}
}

func TestConsoleOriginsAreTheCallbacksOrigins(t *testing.T) {
	assert.DeepEqual(t, consoleOrigins([]string{"https://console.example/cb", "https://console.example/other", "http://127.0.0.1:3000/x", "::"}),
		[]string{"https://console.example", "http://127.0.0.1:3000"})
}

func TestSporeDriveOwnsTheBuildSections(t *testing.T) {
	_, err := withBuild(map[string]any{instance.Builder: map[string]any{}}, &cloudServices{trusted: true})
	assert.ErrorContains(t, err, instance.Builder)
	kept, err := withBuild(map[string]any{"mail": 1}, &cloudServices{})
	assert.NilError(t, err)
	assert.DeepEqual(t, kept, map[string]any{"mail": 1})
}
