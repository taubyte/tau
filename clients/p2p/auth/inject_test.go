package auth_test

import (
	"bytes"
	"encoding/pem"
	"os"
	"testing"

	commonIface "github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/http/helpers"
	dreamlandCommon "github.com/taubyte/tau/libdream/common"
	dreamland "github.com/taubyte/tau/libdream/services"
	"github.com/taubyte/tau/protocols/auth/acme/store"
)

var testDir = "testdir"

func TestInject(t *testing.T) {
	defer os.Remove(testDir)

	u := dreamland.Multiverse("testInject")
	defer u.Stop()

	err := u.StartWithConfig(&dreamlandCommon.Config{
		Services: map[string]commonIface.ServiceConfig{
			"auth": {},
			"tns":  {},
		},
		Simples: map[string]dreamlandCommon.SimpleConfig{
			"client": {
				Clients: dreamlandCommon.SimpleConfigClients{
					Auth: &commonIface.ClientConfig{},
				},
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	simple, err := u.Simple("client")
	if err != nil {
		t.Error(err)
		return
	}

	cert, key, err := helpers.GenerateCert("*.pass.com")
	if err != nil {
		t.Error(err)
		return
	}

	var p bytes.Buffer
	err = pem.Encode(&p, &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: key,
	})
	if err != nil {
		t.Error(err)
		return
	}

	err = pem.Encode(&p, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert,
	})
	if err != nil {
		t.Error(err)
		return
	}

	err = simple.Auth().InjectStaticCertificate("*.pass.com", []byte(cert))
	if err != nil {
		t.Error(err)
		return
	}

	newStore, err := store.New(u.Context(), simple.PeerNode(), testDir, err)
	if err != nil {
		t.Error(err)
		return
	}

	// Shoud Fail
	_, err = newStore.Get(u.Context(), "test.fail.com")
	if err == nil {
		t.Error("Expected error")
		return
	}

	// Should Pass
	data, err := newStore.Get(u.Context(), "test.pass.com")
	if err != nil {
		t.Error(err)
		return
	}

	if !bytes.Equal(data, cert) {
		t.Error("Expected key to match")
		return
	}
}
