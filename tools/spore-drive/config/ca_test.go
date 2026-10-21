package config

import (
	"testing"
	"time"

	"github.com/spf13/afero"
	"gotest.tools/v3/assert"

	"github.com/taubyte/tau/p2p/sectls"
)

func TestTheCloudsCA(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := New(fs, "/")
	assert.NilError(t, err)
	assert.NilError(t, p.Cloud().Domain().SetRoot("test.com"))
	ca := p.Cloud().P2P().CA()

	_, err = ca.Load()
	assert.ErrorContains(t, err, "ca.crt", "there is none yet")
	_, err = ca.OpenCertificate()
	assert.Assert(t, err != nil)
	_, err = ca.OpenKey()
	assert.Assert(t, err != nil)

	assert.NilError(t, ca.Generate())
	loaded, err := ca.Load()
	assert.NilError(t, err)
	assert.Equal(t, loaded.Root.Subject.CommonName, "test.com")

	// A user's own CA sits at whatever paths they name.
	own, err := sectls.NewCA("mine", time.Hour)
	assert.NilError(t, err)
	keyPEM, err := own.KeyPEM()
	assert.NilError(t, err)
	assert.NilError(t, afero.WriteFile(fs, "/mine/root.pem", own.CertificatePEM(), 0o600))
	assert.NilError(t, afero.WriteFile(fs, "/mine/root.key", keyPEM, 0o600))
	assert.NilError(t, ca.SetCertificate("mine/root.pem"))
	assert.NilError(t, ca.SetKey("mine/root.key"))
	loaded, err = ca.Load()
	assert.NilError(t, err)
	assert.Equal(t, loaded.Root.Subject.CommonName, "mine")

	// Half of one is refused, not completed.
	assert.NilError(t, ca.SetKey("mine/missing.key"))
	_, err = ca.Load()
	assert.ErrorContains(t, err, "missing.key")

	assert.NilError(t, afero.WriteFile(fs, "/mine/other.key", func() []byte {
		other, err := sectls.NewCA("other", time.Hour)
		assert.NilError(t, err)
		k, err := other.KeyPEM()
		assert.NilError(t, err)
		return k
	}(), 0o600))
	assert.NilError(t, ca.SetKey("mine/other.key"))
	_, err = ca.Load()
	assert.ErrorContains(t, err, "does not match")
}
