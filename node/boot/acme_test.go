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
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/letsencrypt/pebble/v2/ca"
	"github.com/letsencrypt/pebble/v2/db"
	"github.com/letsencrypt/pebble/v2/va"
	"github.com/letsencrypt/pebble/v2/wfe"
	mdns "github.com/miekg/dns"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
	"github.com/taubyte/tau/pkg/certificate"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/gateway"
)

// acme_test is REAL ISSUANCE FROM A REAL ACME SERVER: pebble, Let's Encrypt's
// test CA, runs in this process; it fetches the HTTP-01 answer from the node's own clear listener,
// and signs a certificate the node then presents on its https listener to a
// client that trusts pebble's root and nothing else.

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

// resolver answers every A query with loopback, over tcp, as the public DNS
// does for a cloud whose zone is pointed at its gateways. The node's own dns
// answers routed services, not generated gateway names, so it is not asked.
func resolver(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &mdns.Server{Listener: l, Handler: mdns.HandlerFunc(func(w mdns.ResponseWriter, r *mdns.Msg) {
		m := new(mdns.Msg)
		m.SetReply(r)
		if q := r.Question[0]; q.Qtype == mdns.TypeA {
			m.Answer = []mdns.RR{&mdns.A{Hdr: mdns.RR_Header{Name: q.Name, Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: 1}, A: net.IPv4(127, 0, 0, 1)}}
		}
		w.WriteMsg(m)
	})}
	go srv.ActivateAndServe()
	t.Cleanup(func() { srv.Shutdown() })
	return l.Addr().String()
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

// TestAGeneratedNameIsIssuedByACMEAndServedOverTLS.
func TestAGeneratedNameIsIssuedByACMEAndServedOverTLS(t *testing.T) {
	const cloud = "tau.example.io"
	compiled, hosts, name := fixtureWithHosts(t, cloud)
	ca := startPebble(t)
	root := t.TempDir()

	f := File{
		Node: node.Config{
			Services: []string{node.Register, node.Discovery, node.Gateway},
			Gateway: gateway.Config{CloudFqdn: cloud, ACME: certificate.ACME{
				Enabled:   true,
				Directory: ca.server.URL + "/dir",
				Email:     "ops@example.io",
				RootCA:    ca.directoryCA(),
			}},
		},
		Root:     root,
		Listen:   []string{"/ip4/127.0.0.1/tcp/0"},
		SwarmKey: base64.StdEncoding.EncodeToString(swarmKey()),
		HTTP:     "127.0.0.1:0",
		HTTPS:    "127.0.0.1:0",
	}
	running, err := Start(t.Context(), f)
	require.NoError(t, err)
	ca.validate(t, running.Address(), resolver(t))

	_, err = running.Node.Register().Local().Publish(t.Context(), register.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled, Hosts: hosts,
	})
	require.NoError(t, err)

	res := handshake(t, running.SecureAddress(), name, ca.roots(t))
	require.Equal(t, []string{name}, res.TLS.PeerCertificates[0].DNSNames)
	require.NotEqual(t, http.StatusNotFound, res.StatusCode, "the gateway found its route for the name over TLS")
	serial := res.TLS.PeerCertificates[0].SerialNumber

	kept, err := os.ReadFile(filepath.Join(root, "acme", name+".pem"))
	require.NoError(t, err, "the certificate is kept under the node's root")
	require.Contains(t, string(kept), "PRIVATE KEY")
	require.FileExists(t, filepath.Join(root, "acme", "account.key"))

	// A NAME OUTSIDE THE ZONE is never issued, and fails its handshake.
	_, err = tls.Dial("tcp", running.SecureAddress(), &tls.Config{RootCAs: ca.roots(t), ServerName: "shop.example.com"})
	require.Error(t, err)

	// A RESTART ISSUES NOTHING: the CA is gone and the kept certificate answers.
	require.NoError(t, running.Close())
	ca.server.Close()
	again, err := Start(t.Context(), f)
	require.NoError(t, err)
	defer again.Close()
	res = handshake(t, again.SecureAddress(), name, ca.roots(t))
	require.Equal(t, serial, res.TLS.PeerCertificates[0].SerialNumber, "the same certificate, from the node's root")
}
