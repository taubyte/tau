package app

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/taubyte/tau/pkg/raft"
	"gotest.tools/v3/assert"
)

type fakeAuth struct {
	certs   map[string][]byte
	deleted []string
	listErr error
}

func (f *fakeAuth) ListCertificates() ([]string, error) {
	names := []string{}
	for n := range f.certs {
		names = append(names, n)
	}
	return names, f.listErr
}
func (f *fakeAuth) GetRawCertificate(d string) ([]byte, error) { return f.certs[d], nil }
func (f *fakeAuth) DeleteCertificate(d string) error {
	f.deleted = append(f.deleted, d)
	delete(f.certs, d)
	return nil
}

var notAfter = time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC)

func testPEM(t *testing.T, cn string) []byte {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	assert.NilError(t, err)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Fake R3"}, DNSNames: []string{cn}, NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	assert.NilError(t, err)
	kb, _ := x509.MarshalECPrivateKey(k)
	return append(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
}

// operatorRoot is a tau root with shape "test" and a local acme copy of two
// certificates, and ops faked: no p2p, no journal, no ports.
func operatorRoot(t *testing.T) (string, *fakeAuth) {
	root := t.TempDir()
	for _, d := range []string{"storage/test", "config/keys"} {
		assert.NilError(t, os.MkdirAll(filepath.Join(root, d), 0750))
	}
	assert.NilError(t, newApp().RunContext(context.Background(), []string{"tau", "--root", root, "cnf", "gen", "-s", "test", "--services", "auth,seer,patrick", "--swarm-key", "--dv-keys"}))

	local := filepath.Join(root, "storage", "test_client", "local", "acme")
	assert.NilError(t, os.MkdirAll(local, 0700))
	for _, n := range []string{"*.gen.example.com", "x.gen.example.com"} {
		assert.NilError(t, os.WriteFile(filepath.Join(local, n), testPEM(t, n), 0600))
	}
	assert.NilError(t, os.WriteFile(filepath.Join(local, "acme_account+key"), []byte("k"), 0600))

	fa := &fakeAuth{certs: map[string][]byte{"*.gen.example.com": testPEM(t, "*.gen.example.com")}}
	old := ops
	t.Cleanup(func() { ops = old })
	ops = operator{
		auth: func(context.Context, shapeConf) (certAuth, func(), error) { return fa, func() {}, nil },
		raft: func(_ context.Context, sc shapeConf, ns string) (*raft.ClusterInfoResponse, error) {
			return &raft.ClusterInfoResponse{LeaderID: sc.pid, MemberCount: 3}, nil
		},
		journal: func(string) ([]string, error) {
			lines := []string{"INFO ok"}
			for i := range 7 {
				lines = append(lines, "ERROR boom "+string(rune('a'+i)))
			}
			return lines, nil
		},
		portUp: func(port int) bool { return port%2 == 0 },
	}
	return root, fa
}

func runApp(t *testing.T, args ...string) (string, error) {
	var buf bytes.Buffer
	app := newApp()
	app.Writer = &buf
	err := app.RunContext(context.Background(), append([]string{"tau"}, args...))
	return buf.String(), err
}

func TestCertsList(t *testing.T) {
	root, _ := operatorRoot(t)
	out, err := runApp(t, "certs", "list", "--root", root)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(out, "*.gen.example.com                        Fake R3"), out)
	assert.Equal(t, strings.Count(out, "\n"), 3, out) // auth, two local
	assert.Assert(t, strings.Contains(out, "x.gen.example.com"), out)
	assert.Assert(t, strings.Contains(out, "Fake R3"), out)
	assert.Assert(t, strings.Contains(out, "2027-02-01T00:00:00Z"), out)
	assert.Assert(t, !strings.Contains(out, "acme_account+key"), out)

	out, err = runApp(t, "certs", "list", "--root", root, "-s", "test", "--json")
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(out, `"where": "auth"`), out)
}

func TestCertsListOldAuth(t *testing.T) {
	root, fa := operatorRoot(t)
	fa.listErr = errors.New("Acme action `list` not recognized")
	out, err := runApp(t, "certs", "list", "--root", root)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(out, "auth: Acme action `list` not recognized"), out)
	assert.Assert(t, strings.Contains(out, "x.gen.example.com"), out) // local copies still listed
}

func TestCertsDeleteExactName(t *testing.T) {
	root, fa := operatorRoot(t)
	out, err := runApp(t, "certs", "delete", "--root", root, "*.gen.example.com")
	assert.NilError(t, err)
	assert.DeepEqual(t, fa.deleted, []string{"*.gen.example.com"})

	local := filepath.Join(root, "storage", "test_client", "local", "acme")
	_, err = os.Stat(filepath.Join(local, "*.gen.example.com"))
	assert.Assert(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(local, "x.gen.example.com")) // a glob would have matched it
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(out, "local/acme/[*].gen.example.com"), out)
	assert.Assert(t, strings.Contains(out, "other nodes"), out)
}

func TestCertsDeleteRefusesNonNames(t *testing.T) {
	root, fa := operatorRoot(t)
	for _, n := range []string{"localhost", "*", "../../config/test.yaml", "a/b.com"} {
		_, err := runApp(t, "certs", "delete", "--root", root, n)
		assert.Assert(t, err != nil, n)
	}
	assert.Equal(t, len(fa.deleted), 0)
}

func TestNodeHealth(t *testing.T) {
	root, _ := operatorRoot(t)
	out, err := runApp(t, "node", "health", "--root", root)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(out, "shape test: services"), out)
	assert.Assert(t, strings.Contains(out, "raft leader, 3 members"), out)
	assert.Assert(t, strings.Contains(out, "DOWN") || strings.Contains(out, " up"), out)
	assert.Equal(t, strings.Count(out, "error: ERROR"), 5, out)
	assert.Assert(t, strings.Contains(out, "ERROR boom g"), out)
	assert.Assert(t, !strings.Contains(out, "ERROR boom b"), out)

	out, err = runApp(t, "node", "health", "--root", root, "--json")
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(out, `"raft": "leader"`), out)
}

func TestNodeHealthLogUnreachable(t *testing.T) {
	root, _ := operatorRoot(t)
	ops.journal = func(string) ([]string, error) { return nil, errors.New("journalctl not found") }
	out, err := runApp(t, "node", "health", "--root", root)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(out, "log not reachable: journalctl not found"), out)
}

// A port set to 0 in the config is not used; health listed it as DOWN.
func TestHealthSkipsUnusedPorts(t *testing.T) {
	var b strings.Builder
	writeHealth(&b, health{Shapes: []shapeHealth{{Shape: "all", Ports: map[string]bool{"ipfs:0": false, "main:4242": true}}}})
	if strings.Contains(b.String(), "ipfs:0") || !strings.Contains(b.String(), "main:4242") {
		t.Fatalf("got:\n%s", b.String())
	}
}

// B1: flags after the name were taken as more names.
func TestCertsDeleteFlagAfterName(t *testing.T) {
	root, fa := operatorRoot(t)
	out, err := runApp(t, "certs", "delete", "*.gen.example.com", "--root", root, "-s", "test")
	assert.NilError(t, err, out)
	assert.DeepEqual(t, fa.deleted, []string{"*.gen.example.com"})

	_, err = runApp(t, "certs", "delete", "--root", root, "a.example.com", "b.example.com")
	assert.ErrorContains(t, err, `got 2: ["a.example.com" "b.example.com"]`)
}

// B2: a name nobody has was reported deleted.
func TestCertsDeleteNotFound(t *testing.T) {
	root, fa := operatorRoot(t)
	_, err := runApp(t, "certs", "delete", "--root", root, "zz-nosuch.example.invalid")
	assert.ErrorContains(t, err, "not found")
	assert.Equal(t, len(fa.deleted), 0)

	// only a local copy: auth is not asked to delete, and the output says so
	out, err := runApp(t, "certs", "delete", "--root", root, "x.gen.example.com")
	assert.NilError(t, err)
	assert.Equal(t, len(fa.deleted), 0)
	assert.Assert(t, strings.Contains(out, "x.gen.example.com was not in auth"), out)
	assert.Assert(t, strings.Contains(out, "removed "), out)
}

// B3: health printed commit unknown when --version found vcs.revision.
func TestNodeHealthCommitFromBuildInfo(t *testing.T) {
	root, _ := operatorRoot(t)
	old := debugInfo
	t.Cleanup(func() { debugInfo = old })
	debugInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "5e4661e"}}}, true
	}
	out, err := runApp(t, "node", "health", "--root", root)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(out, "(commit 5e4661e)"), out)
}
