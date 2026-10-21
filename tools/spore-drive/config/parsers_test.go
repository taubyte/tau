package config

import (
	"encoding/base64"
	"io"
	"testing"

	"github.com/spf13/afero"
	"gotest.tools/v3/assert"
)

func parserWith(t *testing.T, files map[string]string) (afero.Fs, Parser) {
	t.Helper()
	fs := afero.NewMemMapFs()
	for name, content := range files {
		assert.NilError(t, afero.WriteFile(fs, "/"+name, []byte(content), 0o600))
	}
	p, err := New(fs, "/")
	assert.NilError(t, err)
	return fs, p
}

func TestAddedAndTenancyAreRead(t *testing.T) {
	_, p := parserWith(t, map[string]string{
		"added.yaml":   "example:\n  greeting: hello\n",
		"tenancy.yaml": "namespaces: [gitea.example.com/team]\nprovider: github\nowner: acme\napp:\n  id: 7\n",
	})
	assert.DeepEqual(t, p.Added(), map[string]any{"example": map[string]any{"greeting": "hello"}})
	assert.DeepEqual(t, p.Tenancy(), []string{"gitea.example.com/team", "github.com/acme"})
	assert.Assert(t, p.TenancyApp())

	_, p = parserWith(t, map[string]string{"tenancy.yaml": "provider: forge.example.com\nowner: acme\n"})
	assert.DeepEqual(t, p.Tenancy(), []string{"forge.example.com/acme"}) // a forge host names itself
	assert.Assert(t, !p.TenancyApp())

	_, p = parserWith(t, nil)
	assert.Assert(t, p.Added() == nil)
	assert.Assert(t, p.Tenancy() == nil)
}

func TestASignerKeyIsCreatedUnderItsDirectory(t *testing.T) {
	fs, p := parserWith(t, nil)
	_, err := p.Auth().Add("main").Create()
	assert.ErrorContains(t, err, "no key found")

	assert.NilError(t, p.Auth().Add("main").SetKey("ssh/deploy.pem"))
	w, err := p.Auth().Get("main").Create()
	assert.NilError(t, err)
	_, err = io.WriteString(w, "key")
	assert.NilError(t, err)
	assert.NilError(t, w.Close())
	got, err := afero.ReadFile(fs, "/ssh/deploy.pem")
	assert.NilError(t, err)
	assert.Equal(t, string(got), "key")
}

func TestBootstrapListsAndDeletes(t *testing.T) {
	_, p := parserWith(t, nil)
	b := p.Cloud().P2P().Bootstrap()
	assert.NilError(t, b.Shape("shape1").Append("host1"))
	assert.DeepEqual(t, b.List(), []string{"shape1"})
	assert.NilError(t, b.Delete("shape1"))
	assert.Equal(t, len(b.List()), 0)
}

func TestSSHAddresses(t *testing.T) {
	_, p := parserWith(t, nil)
	ssh := p.Hosts().Host("h").SSH()
	assert.Equal(t, ssh.Port(), uint16(22), "the default port")
	assert.Assert(t, ssh.SetFullAddress("no-port") != nil)
	assert.Assert(t, ssh.SetFullAddress("1.2.3.4:99999") != nil)
	assert.NilError(t, ssh.SetFullAddress("1.2.3.4:2222"))
	assert.Equal(t, ssh.Port(), uint16(2222))
}

func TestAnInstanceKeyAndItsID(t *testing.T) {
	_, p := parserWith(t, nil)
	inst := p.Hosts().Host("h").Shapes().Instance("s")
	assert.Equal(t, inst.Id(), "", "no key, no id")

	assert.Assert(t, inst.SetKey("not base64!") != nil)
	assert.Assert(t, inst.SetKey(base64.StdEncoding.EncodeToString([]byte("not a key"))) != nil)

	id, key, err := generateNodeKeyAndID("")
	assert.NilError(t, err)
	assert.NilError(t, inst.SetKey(key))
	assert.Equal(t, inst.Key(), key)
	assert.Equal(t, inst.Id(), id)

	// An id is derived from a key written without one.
	other := p.Hosts().Host("h2").Shapes().Instance("s")
	assert.NilError(t, other.(*instShape).Query.Get("key").Set(key).Commit())
	assert.Equal(t, other.Id(), id)
}

func TestDomainValidationKeysFromAPrivateOne(t *testing.T) {
	priv, pub, err := generateDVKeys(nil, nil)
	assert.NilError(t, err)

	p2, pub2, err := generateDVKeys(priv, pub)
	assert.NilError(t, err)
	assert.DeepEqual(t, p2, priv)
	assert.DeepEqual(t, pub2, pub)

	_, derived, err := generateDVKeys(priv, nil)
	assert.NilError(t, err)
	assert.DeepEqual(t, derived, pub)

	_, _, err = generateDVKeys([]byte("not pem"), nil)
	assert.ErrorContains(t, err, "open ecdsa key")
}

func TestValidationKeysDefaultTheirPaths(t *testing.T) {
	_, p := parserWith(t, nil)
	v := p.Cloud().Domain().Validation()
	_, err := v.OpenPrivateKey()
	assert.Assert(t, err != nil)
	_, err = v.OpenPublicKey()
	assert.Assert(t, err != nil)
	priv, pub := v.Keys()
	assert.Equal(t, priv, "keys/dv_private.key")
	assert.Equal(t, pub, "keys/dv_public.key")
}

func TestACAIsGeneratedAtItsDefaultPaths(t *testing.T) {
	_, p := parserWith(t, nil)
	ca := p.Cloud().P2P().CA()
	assert.NilError(t, ca.Generate())
	for _, open := range []func() (io.ReadCloser, error){ca.OpenCertificate, ca.OpenKey} {
		r, err := open()
		assert.NilError(t, err)
		assert.NilError(t, r.Close())
	}
}

func TestKeysCannotBeWrittenToAReadOnlyConfig(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/cloud.yaml", []byte("p2p:\n  ca:\n    certificate: keys/ca.crt\n    key: keys/ca.key\ndomain:\n  validation:\n    key:\n      private: keys/a\n      public: keys/b\n"), 0o600))
	p, err := New(afero.NewReadOnlyFs(fs), "/")
	assert.NilError(t, err)
	assert.Assert(t, p.Cloud().P2P().CA().Generate() != nil)
	assert.Assert(t, p.Cloud().Domain().Validation().Generate() != nil)
}
