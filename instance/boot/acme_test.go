package boot

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/letsencrypt/pebble/v2/ca"
	"github.com/letsencrypt/pebble/v2/db"
	"github.com/letsencrypt/pebble/v2/va"
	"github.com/letsencrypt/pebble/v2/wfe"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	"github.com/taubyte/tau/pkg/certificate"
	"github.com/taubyte/tau/pkg/certificate/acme"
	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/cm"
	secretsService "github.com/taubyte/tau/services/secrets"
)

// Real issuance from pebble, Let's Encrypt's test CA, in process: it resolves through
// the node's dns (which answers DNS-01), fetches HTTP-01 from the node's clear
// listener, and the node presents the certificates to a client trusting only pebble.

// pebble serves an ACME directory over TLS. Its validation authority is built
// once the node is up, because it must be told the node's http port.
type pebble struct {
	server *httptest.Server
	ca     *ca.CAImpl
	db     *db.MemoryStore
	logger *log.Logger
	wfe    atomic.Pointer[http.Handler]
}

func startPebble(t *testing.T) *pebble {
	t.Helper()
	t.Setenv("PEBBLE_VA_NOSLEEP", "1")
	// NO NONCE IS REJECTED: pebble refuses 5% of good nonces by default, and each
	// refusal costs the client an exponential backoff of up to ten seconds, so a
	// run that drew a few spent its whole wait on them. Retrying a bad nonce is the
	// ACME client's job, not what these tests are about.
	t.Setenv("PEBBLE_WFE_NONCEREJECT", "0")
	// EVERY ORDER IS PROVED: pebble otherwise reuses a valid authorization at
	// random, so whether a renewal reaches the challenge would be chance.
	t.Setenv("PEBBLE_AUTHZREUSE", "0")
	p := &pebble{db: db.NewMemoryStore(), logger: log.New(io.Discard, "", 0)}
	p.ca = ca.New(p.logger, p.db, "", "ecdsa", 0, 1, map[string]ca.Profile{"default": {Description: "default"}})
	p.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := p.wfe.Load()
		if h == nil {
			http.Error(w, "not yet", http.StatusServiceUnavailable)
			return
		}
		(*h).ServeHTTP(w, r)
	}))
	t.Cleanup(p.server.Close)
	return p
}

// validate points the validation authority at the node.
func (p *pebble) validate(t *testing.T, httpAddress, resolver string) {
	t.Helper()
	_, port, err := net.SplitHostPort(httpAddress)
	require.NoError(t, err)
	httpPort, err := strconv.Atoi(port)
	require.NoError(t, err)
	v := va.New(p.logger, httpPort, 0, false, resolver, p.db)
	front := wfe.New(p.logger, p.db, v, p.ca, nil, false, false, 0, 0)
	h := front.Handler()
	p.wfe.Store(&h)
}

func (p *pebble) directoryCA() string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: p.server.Certificate().Raw}))
}

func (p *pebble) roots(t *testing.T) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(p.ca.GetRootCert(0).PEM()))
	return pool
}

// dnsOf is the node's own dns listener over tcp, which is how pebble asks:
// the node answers its generated names itself, as it does for a cloud whose
// zone is delegated to its dns nodes.
func dnsOf(t *testing.T, running *Running) string {
	t.Helper()
	for _, a := range running.DNSAddrs() {
		if tcp, ok := a.(*net.TCPAddr); ok {
			return tcp.String()
		}
	}
	t.Fatal("the node serves no dns over tcp")
	return ""
}

func handshake(t *testing.T, address, name string, roots *x509.CertPool) *http.Response {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: name}}}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+address+"/", nil)
	require.NoError(t, err)
	req.Host = name
	res, err := client.Do(req)
	require.NoError(t, err, "the handshake verified a certificate pebble issued for the name")
	_, err = io.ReadAll(res.Body)
	require.NoError(t, err)
	res.Body.Close()
	return res
}

// issuing is a single node that runs everything issuance needs: the gateway,
// the dns server, the secrets service the keys go to and the certificate
// service. Its identity is fixed, as an operator's is: the secrets it kept
// are opened by the shares of the peer it is.
func issuing(t *testing.T, ca *pebble, cloud, root string, cfg cm.CertificateConfig) File {
	t.Helper()
	minted, err := identity("")
	require.NoError(t, err)
	cfg.ACME = certificate.ACME{
		Enabled:   true,
		Directory: ca.server.URL + "/dir",
		Email:     "ops@example.io",
		RootCA:    ca.directoryCA(),
	}
	return initialized(t, accountBundle(t), admit(t, File{
		Node: instance.Config{
			Services:  []string{instance.Register, instance.Discovery, instance.Gateway, instance.DNS, instance.Secrets, instance.CM},
			DNSListen: "127.0.0.1:0",
			CloudFqdn: cloud,
			CM:        cm.Config{Certificate: cfg},
		},
		Root:       root,
		PrivateKey: base64.StdEncoding.EncodeToString(minted),
		Listen:     []string{"/ip4/127.0.0.1/tcp/0"},
		HTTP:       "127.0.0.1:0",
		HTTPS:      "127.0.0.1:0",
	}))
}

// accountBundle seals the cloud's ACME account key once, for every node that
// runs the secrets service to be initialized with.
func accountBundle(t *testing.T) *secretsv2.InitBundle {
	t.Helper()
	return bundleOf(t, secretsService.Ref{Scope: secretsService.Cloud(cm.VaultService), Name: cm.AccountSecret}, cloudAccount(t))
}

// initialized writes bundle into f's vault before f starts, as `taud init`
// does.
func initialized(t *testing.T, bundle *secretsv2.InitBundle, f File) File {
	t.Helper()
	require.NoError(t, Init(t.Context(), f, bundle))
	return f
}

// cloudAccount is the one ACME account key every node of the test cloud is
// configured with.
var mintAccount = sync.OnceValues(func() ([]byte, error) {
	_, blob, err := acme.MintKey()
	return blob, err
})

func cloudAccount(t *testing.T) string {
	t.Helper()
	blob, err := mintAccount()
	require.NoError(t, err)
	return string(blob)
}

func TestAGeneratedNameIsIssuedByACMEAndServedOverTLS(t *testing.T) {
	const cloud = "tau.example.io"
	compiled, hosts, name := fixtureWithHosts(t, cloud)
	ca := startPebble(t)
	f := issuing(t, ca, cloud, t.TempDir(), cm.CertificateConfig{Challenge: cm.ChallengeHTTP})
	running, err := Start(t.Context(), f)
	require.NoError(t, err)
	ca.validate(t, running.Address(), dnsOf(t, running))

	_, err = running.Register().Local().Publish(t.Context(), register.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled, Hosts: hosts,
	})
	require.NoError(t, err)

	res := handshake(t, running.SecureAddress(), name, ca.roots(t))
	require.Equal(t, []string{name}, res.TLS.PeerCertificates[0].DNSNames)
	require.NotEqual(t, http.StatusNotFound, res.StatusCode, "the gateway found its route for the name over TLS")
	serial := res.TLS.PeerCertificates[0].SerialNumber

	// A NAME OUTSIDE THE ZONE is never issued, and fails its handshake.
	_, err = tls.Dial("tcp", running.SecureAddress(), &tls.Config{RootCAs: ca.roots(t), ServerName: "shop.example.com"})
	require.Error(t, err)

	// A RESTART ISSUES NOTHING: the CA is gone and the certificate the
	// service kept, with its key in the secrets service, answers.
	require.NoError(t, running.Close())
	ca.server.Close()
	again, err := Start(t.Context(), f)
	require.NoError(t, err)
	defer again.Close()
	res = handshake(t, again.SecureAddress(), name, ca.roots(t))
	require.Equal(t, serial, res.TLS.PeerCertificates[0].SerialNumber, "the same certificate, from the node's root")
}

func TestTheZoneIsProvedByDNS01AndItsWildcardAnswersEveryService(t *testing.T) {
	const cloud = "tau.example.io"
	compiled, hosts, name := fixtureWithHosts(t, cloud)
	ca := startPebble(t)
	running, err := Start(t.Context(), issuing(t, ca, cloud, t.TempDir(), cm.CertificateConfig{Wildcard: true}))
	require.NoError(t, err)
	defer running.Close()
	// NO HTTP-01 PORT: pebble is pointed at a port nothing serves, so only
	// DNS-01 can prove anything here.
	ca.validate(t, "127.0.0.1:1", dnsOf(t, running))

	_, err = running.Register().Local().Publish(t.Context(), register.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled, Hosts: hosts,
	})
	require.NoError(t, err)

	// A GENERATED NAME, two labels under the zone, gets its own.
	res := handshake(t, running.SecureAddress(), name, ca.roots(t))
	require.Equal(t, []string{name}, res.TLS.PeerCertificates[0].DNSNames)

	// A SERVICE'S NAME is answered by the wildcard, on a listener taking the
	// node's ServiceTLS — which is what the build frontend's does.
	secure, err := running.ServiceTLS()
	require.NoError(t, err)
	require.NotNil(t, secure)
	l, err := tls.Listen("tcp", "127.0.0.1:0", secure)
	require.NoError(t, err)
	defer l.Close()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), ReadHeaderTimeout: time.Minute}
	go server.Serve(l)
	defer server.Close()
	for _, service := range []string{"build-frontend." + cloud, "dns." + cloud} {
		res := handshake(t, l.Addr().String(), service, ca.roots(t))
		require.Equal(t, []string{"*." + cloud}, res.TLS.PeerCertificates[0].DNSNames, service)
	}
}
