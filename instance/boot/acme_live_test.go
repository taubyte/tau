//go:build docker_integration

package boot

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/miekg/dns"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	"github.com/taubyte/tau/pkg/certificate"
	"github.com/taubyte/tau/pkg/containers/backends/docker"
	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/services/plm/ingress"
)

// Issuance with nothing in process but tau: pebble in its own container, resolving
// through a bind9 recursive resolver that finds the cloud's zone by real delegation (a
// bind9 parent for example.test with NS and glue for tau.example.test pointing at the
// fleet's two dns nodes). Port 53 needs root: make test-acme-live. Each dns node is
// fronted by a forwarder on <address>:53 recording questions, proving lookups reached
// tau rather than a cache.

const (
	pebbleImage = "ghcr.io/letsencrypt/pebble:2.10.0"
	bindImage   = "internetsystemsconsortium/bind9:9.20@sha256:071465f88068854d0ceadd9b985fb1acae4071e80754e545cd811fde57541ad0"

	liveParent = "example.test"
	liveCloud  = "tau." + liveParent
)

// fleet is three nodes: two run cm and dns and are the zone's NS, and a
// third runs only a gateway and the plm service, and orders what it serves
// itself.
type fleet struct {
	addrs   [3]string
	nodes   []*Running
	proxies [2]*forwarder
}

func TestARealCAIssuesThroughARealDelegation(t *testing.T) {
	sweepLive(t)
	var addrs [3]string
	var bridge string
	for i := range addrs {
		name, gw := liveNetwork(t, i)
		if i == 0 {
			bridge = name
		}
		addrs[i] = gw
	}
	parentNS := startBind(t, bridge, parentConf, map[string]string{"example.test.zone": parentZone(addrs[0], addrs[1])})
	compiled, hosts, name := fixtureWithHosts(t, liveCloud)
	proxies := [2]*forwarder{listenForwarder(t, addrs[0]), listenForwarder(t, addrs[1])}

	// EACH RUN HAS ITS OWN RESOLVER AND ITS OWN CA, so nothing either learned
	// in an earlier run answers for this one.
	run := func(t *testing.T, cfg cm.CertificateConfig, httpPort int) (*fleet, string, *x509.CertPool) {
		resolverAddr := startBind(t, bridge, resolverConf(parentNS), nil)
		ca := startLivePebble(t, httpPort, resolverAddr)
		f := &fleet{addrs: addrs, proxies: proxies}
		bundle := accountBundle(t)
		for i, addr := range addrs {
			file := liveNode(t, ca, cfg, addr, httpPort)
			if i < 2 {
				file.Node.Services = []string{instance.Register, instance.Discovery, instance.Gateway, instance.DNS, instance.Secrets, instance.CM}
				file.Node.DNSListen = "127.0.0.1:0"
				file = initialized(t, bundle, file)
			} else {
				file.Node.Services = []string{instance.Register, instance.Discovery, instance.Gateway, instance.PLM}
				file.Node.Sections = map[string]any{instance.PLM: ingress.Config{Listen: addr + ":8443"}}
			}
			if i > 0 {
				h := f.nodes[0].Host()
				for _, a := range h.Addrs() {
					file.Bootstrap = append(file.Bootstrap, a.String()+"/p2p/"+h.ID().String())
				}
			}
			running, err := Start(t.Context(), file)
			require.NoError(t, err)
			t.Cleanup(func() { running.Close() })
			f.nodes = append(f.nodes, running)
			if i < 2 {
				proxies[i].to(t, running)
			}
			_, err = running.Register().Local().Publish(t.Context(), register.Publication{
				Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled, Hosts: hosts,
			})
			require.NoError(t, err)
		}
		for i := range proxies {
			eventually(t, fmt.Sprintf("NS %d answers %s itself", i, name), time.Minute, func() bool {
				return len(resolve(addrs[i], name, dns.TypeA)) > 0
			})
		}
		eventually(t, "the resolver answers "+name+" through the delegation", time.Minute, func() bool {
			return len(resolve(resolverAddr, name, dns.TypeA)) > 0
		})
		require.True(t, f.reached(resolverAddr, name, dns.TypeA), "the resolver's answer came from tau's dns, not from a cache")
		t.Cleanup(func() {
			valid, invalid := ca.quiesced(t)
			require.NotZero(t, valid, "pebble's log names what it decided")
			require.Zero(t, invalid, "no authorization was ever failed")
		})
		return f, resolverAddr, ca.roots(t)
	}

	t.Run("dns-01", func(t *testing.T) {
		// NOTHING SERVES pebble's HTTP-01 port in this run, so only DNS-01 can
		// prove anything.
		f, resolverAddr, roots := run(t, cm.CertificateConfig{Wildcard: true}, freeLoopbackPort(t))
		gen := f.sameLeafEverywhere(t, name, roots)
		require.Equal(t, []string{name}, gen.DNSNames)
		require.True(t, f.reached(resolverAddr, "_acme-challenge."+name, dns.TypeTXT), "pebble's DNS-01 lookup reached tau's dns")

		frontendName := instance.PLM + "." + liveCloud
		res := issued(t, f.addrs[2]+":8443", frontendName, roots)
		wildcard := res.TLS.PeerCertificates[0]
		require.Equal(t, []string{"*." + liveCloud}, wildcard.DNSNames)
		require.True(t, f.reached(resolverAddr, "_acme-challenge."+liveCloud, dns.TypeTXT), "and the wildcard's")
		for i, n := range f.nodes[:2] {
			secure, err := n.ServiceTLS()
			require.NoError(t, err)
			cert, err := secure.GetCertificate(&tls.ClientHelloInfo{ServerName: frontendName})
			require.NoError(t, err, "node %d", i)
			require.Equal(t, wildcard.Raw, cert.Certificate[0], "node %d holds the one wildcard the cloud ordered", i)
		}
	})

	t.Run("http-01", func(t *testing.T) {
		f, _, roots := run(t, cm.CertificateConfig{Challenge: cm.ChallengeHTTP}, freeLoopbackPort(t))
		gen := f.sameLeafEverywhere(t, name, roots)
		require.Equal(t, []string{name}, gen.DNSNames)
	})
}

// reached is whether the client at from asked either dns node this question.
func (f *fleet) reached(from, name string, qtype uint16) bool {
	return f.proxies[0].asked(from, name, qtype) || f.proxies[1].asked(from, name, qtype)
}

// sameLeafEverywhere handshakes with every node's gateway for name, verified
// to pebble's root, and requires one leaf among them.
func (f *fleet) sameLeafEverywhere(t *testing.T, name string, roots *x509.CertPool) *x509.Certificate {
	t.Helper()
	var leaf *x509.Certificate
	for i, n := range f.nodes {
		res := issued(t, n.SecureAddress(), name, roots)
		require.NotEqual(t, http.StatusNotFound, res.StatusCode, "node %d found its route for the name over TLS", i)
		got := res.TLS.PeerCertificates[0]
		if leaf == nil {
			leaf = got
			continue
		}
		require.Equal(t, leaf.Raw, got.Raw, "node %d presents the one leaf the cloud ordered", i)
	}
	return leaf
}

// issued waits for a node to present a certificate for name that verifies to
// pebble's root: the first node asked orders it, and the others read it.
func issued(t *testing.T, address, name string, roots *x509.CertPool) *http.Response {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: name}}}
	var res *http.Response
	eventually(t, name+" is served at "+address, 3*time.Minute, func() bool {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+address+"/", nil)
		require.NoError(t, err)
		req.Host = name
		got, err := client.Do(req)
		if err != nil {
			return false
		}
		io.Copy(io.Discard, got.Body)
		got.Body.Close()
		res = got
		return true
	})
	return res
}

func liveNode(t *testing.T, ca *livePebble, cfg cm.CertificateConfig, addr string, httpPort int) File {
	t.Helper()
	minted, err := identity("")
	require.NoError(t, err)
	cfg.ACME = certificate.ACME{Enabled: true, Directory: ca.directory, Email: "ops@example.test", RootCA: ca.serverCA}
	return admit(t, File{
		Node: instance.Config{
			CloudFqdn: liveCloud,
			CM:        cm.Config{Certificate: cfg},
		},
		Root:       t.TempDir(),
		PrivateKey: base64.StdEncoding.EncodeToString(minted),
		Listen:     []string{"/ip4/" + addr + "/tcp/0"},
		HTTP:       fmt.Sprintf("%s:%d", addr, httpPort),
		HTTPS:      addr + ":0",
	})
}

// forwarder is a dns node's address as the parent's glue names it. It passes
// every query to the node's own listener and records the question.
type forwarder struct {
	mu       sync.Mutex
	upstream string
	seen     map[string]int
}

func listenForwarder(t *testing.T, addr string) *forwarder {
	t.Helper()
	f := &forwarder{seen: map[string]int{}}
	for _, network := range []string{"udp", "tcp"} {
		s := &dns.Server{Addr: addr + ":53", Net: network, Handler: f}
		started := make(chan error, 1)
		s.NotifyStartedFunc = func() { started <- nil }
		go func() { started <- s.ListenAndServe() }()
		require.NoError(t, <-started, "listening on %s/%s", addr, network)
		t.Cleanup(func() { s.Shutdown() })
	}
	return f
}

// to points the forwarder at a node's dns, over tcp.
func (f *forwarder) to(t *testing.T, running *Running) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upstream = dnsOf(t, running)
}

func (f *forwarder) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	from, _, err := net.SplitHostPort(w.RemoteAddr().String())
	if err != nil {
		from = w.RemoteAddr().String()
	}
	f.mu.Lock()
	upstream := f.upstream
	for _, q := range req.Question {
		f.seen[from+" "+strings.ToLower(q.Name)+"/"+dns.TypeToString[q.Qtype]]++
	}
	f.mu.Unlock()
	if upstream == "" {
		m := new(dns.Msg)
		m.SetRcode(req, dns.RcodeServerFailure)
		w.WriteMsg(m)
		return
	}
	c := &dns.Client{Net: "tcp", Timeout: 5 * time.Second}
	res, _, err := c.Exchange(req, upstream)
	if err != nil {
		m := new(dns.Msg)
		m.SetRcode(req, dns.RcodeServerFailure)
		w.WriteMsg(m)
		return
	}
	w.WriteMsg(res)
}

// asked is whether the client at from asked this question.
func (f *forwarder) asked(from, name string, qtype uint16) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seen[from+" "+strings.ToLower(dns.Fqdn(name))+"/"+dns.TypeToString[qtype]] > 0
}

const parentConf = `options {
	directory "/var/cache/bind";
	listen-on-v6 { none; };
	recursion no;
	allow-query { any; };
	dnssec-validation no;
};
controls { };
zone "` + liveParent + `" { type primary; file "/conf/example.test.zone"; };
`

func parentZone(ns1, ns2 string) string {
	return `$TTL 60
@	IN SOA	ns1.tau.` + liveParent + `. hostmaster.` + liveParent + `. 1 60 60 600 60
@	IN NS	ns1.tau.` + liveParent + `.
tau	IN NS	ns1.tau.` + liveParent + `.
tau	IN NS	ns2.tau.` + liveParent + `.
ns1.tau	IN A	` + ns1 + `
ns2.tau	IN A	` + ns2 + `
`
}

// The resolver knows the parent and nothing else: the cloud's zone is found
// only by following the parent's referral.
func resolverConf(parentNS string) string {
	return `options {
	directory "/var/cache/bind";
	listen-on-v6 { none; };
	recursion yes;
	allow-recursion { any; };
	allow-query { any; };
	dnssec-validation no;
};
controls { };
zone "` + liveParent + `" { type static-stub; server-addresses { ` + parentNS + `; }; };
`
}

// startBind runs named on the bridge and answers the address it holds there.
func startBind(t *testing.T, bridge, conf string, zones map[string]string) string {
	t.Helper()
	dir := readableDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "named.conf"), []byte(conf), 0o644))
	for file, body := range zones {
		require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644))
	}
	c := startLive(t, &core.ContainerConfig{
		Image:   bindImage,
		Command: []string{"-g", "-c", "/conf/named.conf"},
		Volumes: []core.VolumeMount{{Source: dir, Destination: "/conf", ReadOnly: true}},
		Network: &core.NetworkConfig{Mode: bridge},
	})
	addr := addressOn(t, c.id, bridge)
	eventually(t, "bind9 at "+addr+" answers", time.Minute, func() bool {
		return len(resolve(addr, liveParent, dns.TypeSOA)) > 0
	})
	return addr
}

func resolve(server, name string, qtype uint16) []dns.RR {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	m.RecursionDesired = true
	in, err := dns.Exchange(m, server+":53")
	if err != nil || in.Rcode != dns.RcodeSuccess {
		return nil
	}
	return in.Answer
}

type livePebble struct {
	container             *liveContainer
	directory, management string
	serverCA              string
	pool                  *x509.CertPool
}

func startLivePebble(t *testing.T, httpPort int, resolverAddr string) *livePebble {
	t.Helper()
	dir := readableDir(t)
	serverCA := selfSigned(t, dir)
	listen := fmt.Sprintf("127.0.0.1:%d", freeLoopbackPort(t))
	management := fmt.Sprintf("127.0.0.1:%d", freeLoopbackPort(t))
	conf, err := json.Marshal(map[string]any{"pebble": map[string]any{
		"listenAddress":                  listen,
		"managementListenAddress":        management,
		"certificate":                    "/conf/cert.pem",
		"privateKey":                     "/conf/key.pem",
		"httpPort":                       httpPort,
		"tlsPort":                        freeLoopbackPort(t),
		"ocspResponderURL":               "",
		"externalAccountBindingRequired": false,
		"keyAlgorithm":                   "ecdsa",
		"profiles":                       map[string]any{"default": map[string]any{"description": "default", "validityPeriod": 7776000}},
	}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pebble.json"), conf, 0o644))
	c := startLive(t, &core.ContainerConfig{
		Image:   pebbleImage,
		Command: []string{"-config", "/conf/pebble.json", "-dnsserver", resolverAddr + ":53"},
		Env:     []string{"PEBBLE_VA_NOSLEEP=1", "PEBBLE_WFE_NONCEREJECT=0", "PEBBLE_AUTHZREUSE=0"},
		Volumes: []core.VolumeMount{{Source: dir, Destination: "/conf", ReadOnly: true}},
		Network: &core.NetworkConfig{Mode: "host"},
	})
	p := &livePebble{container: c, directory: "https://" + listen + "/dir", management: "https://" + management, serverCA: serverCA}
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM([]byte(serverCA)))
	p.pool = pool
	eventually(t, "pebble serves its directory", time.Minute, func() bool {
		_, err := p.get(p.directory)
		return err == nil
	})
	return p
}

func (p *livePebble) get(url string) ([]byte, error) {
	c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: p.pool}}}
	res, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, res.Status)
	}
	return io.ReadAll(res.Body)
}

// authorizations counts what pebble decided, from its own log: every
// authorization it set valid and every one it set invalid.
func (p *livePebble) authorizations(t *testing.T) (valid, invalid int, lines []string) {
	t.Helper()
	log := p.container.logs()
	require.NoError(t, p.container.out.Err(), "reading pebble's log")
	for _, line := range strings.Split(log, "\n") {
		switch {
		case !strings.Contains(line, " authz "):
		case strings.Contains(line, " set VALID "):
			valid++
		case strings.Contains(line, " set INVALID "):
			invalid++
			lines = append(lines, line)
		}
	}
	return valid, invalid, lines
}

// quiesced is the counts once pebble's log has stopped changing for a few
// seconds, so an order still being decided is counted too.
func (p *livePebble) quiesced(t *testing.T) (valid, invalid int) {
	t.Helper()
	valid, invalid, lines := p.authorizations(t)
	stable := time.Now()
	for deadline := time.Now().Add(2 * time.Minute); time.Since(stable) < 5*time.Second; {
		require.True(t, time.Now().Before(deadline), "pebble's log never settled")
		time.Sleep(time.Second)
		v, i, l := p.authorizations(t)
		if v != valid || i != invalid {
			valid, invalid, lines, stable = v, i, l, time.Now()
		}
	}
	for _, line := range lines {
		t.Logf("pebble: %s", line)
	}
	return valid, invalid
}

// roots is pebble's issuing root, as its management endpoint hands it out.
func (p *livePebble) roots(t *testing.T) *x509.CertPool {
	t.Helper()
	raw, err := p.get(p.management + "/roots/0")
	require.NoError(t, err)
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(raw), "pebble's root is PEM")
	return pool
}

// selfSigned writes the certificate pebble serves its API with, and returns
// it as the PEM the issuer trusts.
func selfSigned(t *testing.T, dir string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "pebble"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cert.pem"), certPEM, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "key.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o644))
	return string(certPEM)
}

// liveOwner names the test process that started a container, as
// pkg/forge/live does: a run killed by -timeout runs no Cleanup, and the next
// run removes what it left only once its owner is gone.
const liveOwner = "taubyte.acme-live.owner"

// liveContainer is one container this test started, and what it has written.
type liveContainer struct {
	id  core.ContainerID
	out output
}

// output collects what a container writes, as it writes it.
type output struct {
	mu  sync.Mutex
	buf bytes.Buffer
	err error
}

func (o *output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.Write(p)
}

func (o *output) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.String()
}

func (o *output) fail(err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.err = err
}

// Err is why reading the container's output stopped before it ended, if it did.
func (o *output) Err() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.err
}

func startLive(t *testing.T, cfg *core.ContainerConfig) *liveContainer {
	t.Helper()
	backend, err := docker.New(core.DockerConfig{})
	require.NoError(t, err)
	require.NoError(t, backend.Image(cfg.Image).Pull(t.Context()), "pulling %s", cfg.Image)
	cfg.Labels = map[string]string{liveOwner: fmt.Sprint(os.Getpid())}
	id, err := backend.Create(t.Context(), cfg)
	require.NoError(t, err)
	c := &liveContainer{id: id}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("%s logs:\n%s", cfg.Image, tail(c.logs(), 60))
		}
		backend.Stop(context.Background(), id)
		backend.Remove(context.Background(), id)
	})
	require.NoError(t, backend.Start(t.Context(), id))

	// Logs follows until the container exits, so a server's log is read for as
	// long as it runs, and finished before the container is removed.
	follow, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	rc, err := backend.Logs(follow, id)
	require.NoError(t, err)
	read := make(chan struct{})
	go func() {
		defer close(read)
		defer rc.Close()
		if _, err := stdcopy.StdCopy(&c.out, &c.out, rc); err != nil {
			c.out.fail(err)
		}
	}()
	t.Cleanup(func() {
		stop()
		<-read
	})
	return c
}

// logs is everything the container has written so far.
func (c *liveContainer) logs() string { return c.out.String() }

// liveNetwork is a bridge of this run's own, and the gateway address the host
// holds on it.
func liveNetwork(t *testing.T, n int) (string, string) {
	t.Helper()
	cli, err := client.New(client.FromEnv)
	require.NoError(t, err)
	defer cli.Close()
	name := fmt.Sprintf("tau-acme-live-%d-%d", os.Getpid(), n)
	created, err := cli.NetworkCreate(t.Context(), name, client.NetworkCreateOptions{
		Driver: "bridge",
		Labels: map[string]string{liveOwner: fmt.Sprint(os.Getpid())},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		c, err := client.New(client.FromEnv)
		if err != nil {
			return
		}
		defer c.Close()
		c.NetworkRemove(context.Background(), created.ID, client.NetworkRemoveOptions{})
	})
	res, err := cli.NetworkInspect(t.Context(), created.ID, client.NetworkInspectOptions{})
	require.NoError(t, err)
	for _, c := range res.Network.IPAM.Config {
		if c.Gateway.Is4() {
			return name, c.Gateway.String()
		}
	}
	t.Fatalf("network %s has no IPv4 gateway", name)
	return "", ""
}

func addressOn(t *testing.T, id core.ContainerID, bridge string) string {
	t.Helper()
	cli, err := client.New(client.FromEnv)
	require.NoError(t, err)
	defer cli.Close()
	res, err := cli.ContainerInspect(t.Context(), string(id), client.ContainerInspectOptions{})
	require.NoError(t, err)
	ep := res.Container.NetworkSettings.Networks[bridge]
	require.NotNil(t, ep, "the container is on %s", bridge)
	return ep.IPAddress.String()
}

func sweepLive(t *testing.T) {
	t.Helper()
	cli, err := client.New(client.FromEnv)
	require.NoError(t, err)
	defer cli.Close()
	ctx := context.Background()
	res, err := cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: client.Filters{}.Add("label", liveOwner)})
	require.NoError(t, err)
	for _, c := range res.Items {
		var pid int
		fmt.Sscan(c.Labels[liveOwner], &pid)
		if pid > 0 && syscall.Kill(pid, 0) == nil {
			continue
		}
		t.Logf("removing %s (%s), left behind by test process %s", c.ID[:12], c.Image, c.Labels[liveOwner])
		_, err := cli.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
		require.NoError(t, err)
	}
	nets, err := cli.NetworkList(ctx, client.NetworkListOptions{Filters: client.Filters{}.Add("label", liveOwner)})
	require.NoError(t, err)
	for _, n := range nets.Items {
		var pid int
		fmt.Sscan(n.Labels[liveOwner], &pid)
		if pid > 0 && syscall.Kill(pid, 0) == nil {
			continue
		}
		_, err := cli.NetworkRemove(ctx, n.ID, client.NetworkRemoveOptions{})
		require.NoError(t, err)
	}
}

func TestTheLeftoverSweepTakesTheAnonymousVolumes(t *testing.T) {
	gone := exec.Command("true")
	require.NoError(t, gone.Run())

	backend, err := docker.New(core.DockerConfig{})
	require.NoError(t, err)
	require.NoError(t, backend.Image(bindImage).Pull(t.Context()), "pulling %s", bindImage)
	id, err := backend.Create(t.Context(), &core.ContainerConfig{
		Image:  bindImage,
		Labels: map[string]string{liveOwner: fmt.Sprint(gone.Process.Pid)},
	})
	require.NoError(t, err)
	t.Cleanup(func() { backend.Remove(context.Background(), id) })

	cli, err := client.New(client.FromEnv)
	require.NoError(t, err)
	defer cli.Close()
	res, err := cli.ContainerInspect(t.Context(), string(id), client.ContainerInspectOptions{})
	require.NoError(t, err)
	var anonymous []string
	for _, m := range res.Container.Mounts {
		if m.Type == mount.TypeVolume {
			anonymous = append(anonymous, m.Name)
		}
	}
	require.NotEmpty(t, anonymous, "%s declares VOLUMEs", bindImage)
	t.Cleanup(func() {
		for _, v := range anonymous {
			cli.VolumeRemove(context.Background(), v, client.VolumeRemoveOptions{Force: true})
		}
	})

	sweepLive(t)

	_, err = cli.ContainerInspect(t.Context(), string(id), client.ContainerInspectOptions{})
	require.True(t, errdefs.IsNotFound(err), "the sweep removes a container whose owner is gone: %v", err)
	for _, v := range anonymous {
		_, err := cli.VolumeInspect(t.Context(), v, client.VolumeInspectOptions{})
		require.True(t, errdefs.IsNotFound(err), "the anonymous volume %s outlived its container: %v", v, err)
	}
}

// readableDir is a temporary directory a container's unprivileged user can
// read: named drops to its own user before it loads zones.
func readableDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o755))
	return dir
}

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func tail(s string, n int) string {
	lines := 0
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\n' {
			lines++
			if lines > n {
				return s[i+1:]
			}
		}
	}
	return s
}

func eventually(t *testing.T, what string, limit time.Duration, ok func() bool) {
	t.Helper()
	began := time.Now()
	for time.Since(began) < limit {
		if ok() {
			t.Logf("%s after %s", what, time.Since(began).Round(time.Millisecond))
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("%s: not within %s", what, limit)
}
