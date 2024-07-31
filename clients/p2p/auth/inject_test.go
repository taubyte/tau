package auth_test

import (
	"bytes"
	"encoding/pem"
	"testing"

	"github.com/taubyte/http/helpers"
	commonIface "github.com/taubyte/tau/core/common"
	authIface "github.com/taubyte/tau/core/services/auth"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/services/auth/acme/store"
	"gotest.tools/v3/assert"
)

func injectCert(t *testing.T, client authIface.Client) []byte {
	cert, key, err := helpers.GenerateCert("*.pass.com")
	assert.NilError(t, err)

	var p bytes.Buffer
	err = pem.Encode(&p, &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: key,
	})
	assert.NilError(t, err)

	err = pem.Encode(&p, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert,
	})
	assert.NilError(t, err)

	err = client.InjectStaticCertificate("*.pass.com", []byte(cert))
	assert.NilError(t, err)

	return cert
}

func TestInject(t *testing.T) {
	testDir := t.TempDir()

	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"auth": {},
			"tns":  {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					Auth: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	auth, err := simple.Auth()
	assert.NilError(t, err)

	cert := injectCert(t, auth)

	newStore, err := store.New(u.Context(), simple.PeerNode(), testDir, err)
	assert.NilError(t, err)

	// Shoud Fail
	_, err = newStore.Get(u.Context(), "test.fail.com")
	if err == nil {
		t.Error("Expected error")
		return
	}

	// Should Pass
	data, err := newStore.Get(u.Context(), "test.pass.com")
	assert.NilError(t, err)

	if !bytes.Equal(data, cert) {
		t.Error("Expected key to match")
		return
	}
}
