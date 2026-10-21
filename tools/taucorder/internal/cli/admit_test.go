package cli

import (
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"

	"github.com/taubyte/tau/p2p/sectls"
)

// admission writes what a client of cloud needs to join it, from ca, and
// returns the flags naming the files.
func admission(t *testing.T, ca *sectls.CA, cloud string) []string {
	t.Helper()
	private, public, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := crypto.MarshalPrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ca.Issue(public, cloud, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := map[string][]byte{
		"private-key": []byte(base64.StdEncoding.EncodeToString(raw)),
		"ca":          ca.CertificatePEM(),
		"certificate": cert,
	}
	var flags []string
	for flag, content := range files {
		path := filepath.Join(dir, flag)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		flags = append(flags, "--"+flag, path)
	}
	return flags
}
