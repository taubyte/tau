//go:build dreaming

package jobs

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taubyte/tau/core/services/patrick"
	"gotest.tools/v3/assert"

	"github.com/taubyte/tau/clients/p2p/patrick/mock"

	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
)

func generatePrivateKey(t *testing.T) string {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	assert.NilError(t, err, "Failed to generate private key")

	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	return string(privateKeyPEM)
}

func TestRunDelay_Dreaming(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	logFile, err := os.Create(logPath)
	assert.NilError(t, err)
	defer logFile.Close()

	u, cleanup, err := startDream(t)
	assert.NilError(t, err)
	defer cleanup()

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	hoarderClient, err := simple.Hoarder()
	assert.NilError(t, err)

	c := &Context{
		Job: &patrick.Job{
			Delay: &patrick.DelayConfig{
				Time: 300,
			},
			Logs: map[string]string{},
			Meta: patrick.Meta{
				Repository: patrick.Repository{
					ID:       1,
					SSHURL:   "git@github.com:testuser/testrepo.git",
					Provider: "github",
					Branch:   "main",
				},
				HeadCommit: patrick.HeadCommit{
					ID: "testcommit123",
				},
			},
		},
		LogFile:   logFile,
		Node:      simple,
		Monkey:    &mockMonkey{hoarder: hoarderClient},
		Patrick:   &mock.Starfish{Jobs: make(map[string]*patrick.Job, 0)},
		DeployKey: generatePrivateKey(t),
	}
	c.Context(u.Context())

	ctx, ctxC := context.WithTimeout(context.Background(), 1*time.Second)
	defer ctxC()
	err = c.Run(ctx)
	assert.Equal(t, err, ErrorContextCanceled)
}
