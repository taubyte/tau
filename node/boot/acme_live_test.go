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
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
	"github.com/taubyte/tau/pkg/certificate"
	"github.com/taubyte/tau/pkg/containers/backends/docker"
	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/build/frontend"
	"github.com/taubyte/tau/services/gateway"
	"github.com/taubyte/tau/services/globals"
)

// acme_live_test is issuance with nothing in process but tau: pebble is a CA
// in its own container, and it resolves through a bind9 recursive resolver
// that finds the cloud's zone by real delegation — a bind9 parent for
// example.test holding NS and glue for tau.example.test that point at the
// node's own dns listener. A delegation names port 53 and bind9 answers only
// on an address an interface holds, so the two bind9s sit on a bridge of
// their own, the node's dns listens on that bridge's gateway — which needs
// root: make test-acme-live — and pebble is host-networked, reaching the
// gateway's HTTP-01 port on loopback.

const (
	pebbleImage = "ghcr.io/letsencrypt/pebble:2.10.0"
	bindImage   = "internetsystemsconsortium/bind9:9.20@sha256:071465f88068854d0ceadd9b985fb1acae4071e80754e545cd811fde57541ad0"

	liveParent = "example.test"
	liveCloud  = "tau." + liveParent
)

func TestARealCAIssuesThroughARealDelegation(t *testing.T) {
	sweepLive(t)
	bridge, cloudNS := liveNetwork(t)
	gatewayHTTP := freeLoopbackPort(t)
	parentNS := startBind(t, bridge, parentConf, map[string]string{"example.test.zone": parentZone(cloudNS)})
	resolverAddr := startBind(t, bridge, resolverConf(parentNS), nil)
	ca := startLivePebble(t, gatewayHTTP, resolverAddr)
	roots := ca.roots(t)

	compiled, hosts, name := fixtureWithHosts(t, liveCloud)
	publish := func(running *Running) {
		_, err := running.Node.Register().Local().Publish(t.Context(), register.Publication{
			Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled, Hosts: hosts,
		})
		require.NoError(t, err)
		eventually(t, "the resolver answers "+name+" through the delegation", time.Minute, func() bool {
			return len(resolve(resolverAddr, name, dns.TypeA)) > 0
		})
	}

	t.Run("dns-01", func(t *testing.T) {
		// NOTHING SERVES pebble's HTTP-01 port in this run, so only DNS-01 can
		// prove anything.
		frontendAddr := fmt.Sprintf("127.0.0.1:%d", freeLoopbackPort(t))
		f := liveNode(t, ca, cloudNS, globals.CertificateConfig{Wildcard: true}, fmt.Sprintf("127.0.0.1:%d", freeLoopbackPort(t)))
		f.Node.Services = append(f.Node.Services, node.BuildFrontend)
		f.Node.BuildFrontend = frontend.Config{Listen: frontendAddr}
		running, err := Start(t.Context(), f)
		require.NoError(t, err)
		defer running.Close()
		publish(running)

		res := handshake(t, running.SecureAddress(), name, roots)
		require.Equal(t, []string{name}, res.TLS.PeerCertificates[0].DNSNames)
		require.NotEqual(t, http.StatusNotFound, res.StatusCode, "the gateway found its route for the name over TLS")

		res = handshake(t, frontendAddr, node.BuildFrontend+"."+liveCloud, roots)
		require.Equal(t, []string{"*." + liveCloud}, res.TLS.PeerCertificates[0].DNSNames)
	})

	t.Run("http-01", func(t *testing.T) {
		running, err := Start(t.Context(), liveNode(t, ca, cloudNS, globals.CertificateConfig{Challenge: globals.ChallengeHTTP}, fmt.Sprintf("127.0.0.1:%d", gatewayHTTP)))
		require.NoError(t, err)
		defer running.Close()
		publish(running)

		res := handshake(t, running.SecureAddress(), name, roots)
		require.Equal(t, []string{name}, res.TLS.PeerCertificates[0].DNSNames)
		require.NotEqual(t, http.StatusNotFound, res.StatusCode)
	})
}

func liveNode(t *testing.T, ca *livePebble, cloudNS string, cfg globals.CertificateConfig, httpAddr string) File {
	t.Helper()
	minted, err := identity("")
	require.NoError(t, err)
	cfg.ACME = certificate.ACME{Enabled: true, Directory: ca.directory, Email: "ops@example.test", RootCA: ca.serverCA}
	return File{
		Node: node.Config{
			Services:  []string{node.Register, node.Discovery, node.Gateway, node.DNS, node.Secrets, node.Globals},
			DNSListen: cloudNS + ":53",
			Gateway:   gateway.Config{CloudFqdn: liveCloud},
			Globals:   globals.Config{Certificate: cfg},
		},
		Root:       t.TempDir(),
		PrivateKey: base64.StdEncoding.EncodeToString(minted),
		Listen:     []string{"/ip4/127.0.0.1/tcp/0"},
		SwarmKey:   base64.StdEncoding.EncodeToString(swarmKey()),
		HTTP:       httpAddr,
		HTTPS:      "127.0.0.1:0",
	}
}

// ─────────────────────────────── dns ───────────────────────────────

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

func parentZone(cloudNS string) string {
	return `$TTL 60
@	IN SOA	ns.` + liveParent + `. hostmaster.` + liveParent + `. 1 60 60 600 60
@	IN NS	ns.` + liveParent + `.
ns	IN A	127.0.0.1
tau	IN NS	ns1.tau.` + liveParent + `.
ns1.tau	IN A	` + cloudNS + `
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
	id := startLive(t, &core.ContainerConfig{
		Image:   bindImage,
		Command: []string{"-g", "-c", "/conf/named.conf"},
		Volumes: []core.VolumeMount{{Source: dir, Destination: "/conf", ReadOnly: true}},
		Network: &core.NetworkConfig{Mode: bridge},
	})
	addr := addressOn(t, id, bridge)
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

// ─────────────────────────────── pebble ───────────────────────────────

type livePebble struct {
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
	startLive(t, &core.ContainerConfig{
		Image:   pebbleImage,
		Command: []string{"-config", "/conf/pebble.json", "-dnsserver", resolverAddr + ":53"},
		Env:     []string{"PEBBLE_VA_NOSLEEP=1", "PEBBLE_WFE_NONCEREJECT=0", "PEBBLE_AUTHZREUSE=0"},
		Volumes: []core.VolumeMount{{Source: dir, Destination: "/conf", ReadOnly: true}},
		Network: &core.NetworkConfig{Mode: "host"},
	})
	p := &livePebble{directory: "https://" + listen + "/dir", management: "https://" + management, serverCA: serverCA}
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

// ─────────────────────────────── containers ───────────────────────────────

// liveOwner names the test process that started a container, as
// pkg/forge/live does: a run killed by -timeout runs no Cleanup, and the next
// run removes what it left only once its owner is gone.
const liveOwner = "taubyte.acme-live.owner"

func startLive(t *testing.T, cfg *core.ContainerConfig) core.ContainerID {
	t.Helper()
	backend, err := docker.New(core.DockerConfig{})
	require.NoError(t, err)
	require.NoError(t, backend.Image(cfg.Image).Pull(t.Context()), "pulling %s", cfg.Image)
	cfg.Labels = map[string]string{liveOwner: fmt.Sprint(os.Getpid())}
	id, err := backend.Create(t.Context(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		if t.Failed() {
			if rc, err := backend.Logs(context.Background(), id); err == nil {
				var out bytes.Buffer
				stdcopy.StdCopy(&out, &out, rc)
				rc.Close()
				t.Logf("%s logs:\n%s", cfg.Image, tail(out.String(), 60))
			}
		}
		backend.Stop(context.Background(), id)
		backend.Remove(context.Background(), id)
	})
	require.NoError(t, backend.Start(t.Context(), id))
	return id
}

// liveNetwork is a bridge of this run's own, and the gateway address the host
// holds on it.
func liveNetwork(t *testing.T) (string, string) {
	t.Helper()
	cli, err := client.New(client.FromEnv)
	require.NoError(t, err)
	defer cli.Close()
	name := fmt.Sprintf("tau-acme-live-%d", os.Getpid())
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
		_, err := cli.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true})
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
