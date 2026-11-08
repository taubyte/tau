//go:build dreaming

package gateway_test

// End-to-end tests of the recommended layout: gateway and substrate run in the
// SAME node (one libp2p node, one lite client node, one shared http service),
// the way cli/node/start.go builds a node whose shape lists both services.
//
// Dream creates one node per service, so these tests build the colocated nodes
// themselves (see newCluster) inside an in-process Dream universe that only
// holds seer, hoarder and tns. Nothing here touches a running Dream or server.
//
// Known shortcut: the DHT ignores loopback addresses, so a node's lite client
// node cannot discover the other nodes by itself on 127.0.0.1. newCluster
// dials every client node to every full node instead.

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	peercore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/miekg/dns"
	seerClient "github.com/taubyte/tau/clients/p2p/seer"
	substrateClient "github.com/taubyte/tau/clients/p2p/substrate"
	commonIface "github.com/taubyte/tau/core/common"
	seerIface "github.com/taubyte/tau/core/services/seer"
	coreSubstrate "github.com/taubyte/tau/core/services/substrate"
	"github.com/taubyte/tau/dream"
	dreamCommon "github.com/taubyte/tau/dream/common"
	"github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/streams/client"
	tunnel "github.com/taubyte/tau/p2p/streams/tunnels/http"
	tauConfig "github.com/taubyte/tau/pkg/config"
	basicHttp "github.com/taubyte/tau/pkg/http/basic"
	httpOptions "github.com/taubyte/tau/pkg/http/options"
	"github.com/taubyte/tau/pkg/kvdb"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	gateway "github.com/taubyte/tau/services/gateway"
	"github.com/taubyte/tau/services/monkey/fixtures/compile"
	seerSvc "github.com/taubyte/tau/services/seer"
	"github.com/taubyte/tau/services/substrate"
	"github.com/taubyte/tau/utils/id"
	"github.com/taubyte/tau/utils/multihash"
	tcc "github.com/taubyte/tau/utils/tcc"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/hoarder/dream"
	_ "github.com/taubyte/tau/clients/p2p/seer/dream"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
	_ "github.com/taubyte/tau/pkg/tcc/taubyte/v1/fixtures"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/seer/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
)

const (
	coloFqdn    = "hal.computers.com"
	coloChannel = "chat"
)

// coloCfg makes a config look like a production shape: the services the node
// runs (substrate only attaches its own catch-all route when no gateway shares
// the node). DevMode stays on, since off it wants /tb/plugins and real DNS
// proofs, except for substrate.startHttp: that is the one place where off adds
// the http-to-https redirect listener (on the port after the http port), and
// the test wants it.
type coloCfg struct {
	tauConfig.Config
	services []string
}

func (c coloCfg) Services() []string { return c.services }

func (c coloCfg) DevMode() bool {
	pcs := make([]uintptr, 6)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])
	for f, more := frames.Next(); more; f, more = frames.Next() {
		if strings.HasSuffix(f.Function, ").startHttp") {
			return false
		}
	}
	return true
}

type coloNode struct {
	full, lite   peer.Node
	ctx          context.Context
	cancel       context.CancelFunc
	port         int // http
	redirectPort int // substrate's plain-http redirect listener
	sub          *substrate.Service
	gw           interface{ Close() error } // nil on a substrate-only node
	http         *basicHttp.Service
	once         sync.Once
}

func (n *coloNode) pid() string  { return n.full.ID().String() }
func (n *coloNode) base() string { return fmt.Sprintf("http://127.0.0.1:%d", n.port) }

// close stops the node the way a crash looks to the others: the network goes
// first, then the services.
func (n *coloNode) close() {
	n.once.Do(func() {
		n.full.Close()
		n.lite.Close()
		n.cancel()
		n.http.Stop()
		if n.gw != nil {
			n.gw.Close()
		}
		n.sub.Close()
	})
}

type cluster struct {
	t       *testing.T
	u       *dream.Universe
	name    string
	nodes   []*coloNode
	client  *http.Client
	project string
	dnsPort int
}

func freePort(t *testing.T) (port int) {
	// http needs port+1 free as well, for the redirect listener
	for range 20 {
		ports, err := dream.GetFreePorts(1)
		assert.NilError(t, err)
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", ports[0]+1))
		if err == nil {
			l.Close()
			return ports[0]
		}
	}
	t.Fatal("no free http port with a free next port")
	return 0
}

// newCluster starts a universe with seer, hoarder and tns, then n nodes. With
// colocated each node runs gateway and substrate; without, substrate only
// (that node serves http itself: the "direct" baseline).
func newCluster(t *testing.T, name string, n int, colocated bool, simples ...string) *cluster {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	t.Cleanup(func() { m.Close() })
	u, err := m.New(dream.UniverseConfig{Name: name})
	assert.NilError(t, err)

	sim := map[string]dream.SimpleConfig{
		"client": {Clients: dream.SimpleConfigClients{TNS: &commonIface.ClientConfig{}, Hoarder: &commonIface.ClientConfig{}, Seer: &commonIface.ClientConfig{}}.Compat()},
	}
	for _, s := range simples {
		sim[s] = dream.SimpleConfig{Clients: dream.SimpleConfigClients{Seer: &commonIface.ClientConfig{}}.Compat()}
	}
	assert.NilError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{"seer": {}, "hoarder": {}, "tns": {}},
		Simples:  sim,
	}))

	c := &cluster{t: t, u: u, name: name}
	c.client = &http.Client{Timeout: 90 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 128}}
	c.dnsPort, err = u.GetPort(u.Seer().Node(), "dns")
	assert.NilError(t, err)

	t.Cleanup(func() {
		for _, nd := range c.nodes {
			nd.close()
		}
	})
	for i := range n {
		c.addNode(i, colocated)
	}
	// every client node reaches every full node (see the shortcut above)
	for _, nd := range c.nodes {
		for _, other := range c.nodes {
			connect(nd.lite, other.full)
		}
	}
	return c
}

func connect(from, to peer.Node) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	from.Peer().Connect(ctx, peercore.AddrInfo{ID: to.ID(), Addrs: to.Peer().Addrs()})
}

func (c *cluster) addNode(i int, colocated bool) {
	t, u := c.t, c.u
	ctx, cancel := context.WithCancel(u.Context())
	nd := &coloNode{ctx: ctx, cancel: cancel}

	httpPort := freePort(t)
	ports, err := dream.GetFreePorts(3)
	assert.NilError(t, err)
	p2p, dnsPort, liteP2P := ports[0], ports[1], ports[2]
	nd.port, nd.redirectPort = httpPort, httpPort+1

	svcs := []string{"substrate"}
	if colocated {
		svcs = append(svcs, "gateway")
	}
	root := path.Join(u.Root(), fmt.Sprintf("colo-%d", i))
	assert.NilError(t, os.MkdirAll(root, 0750))
	base, err := dreamCommon.NewConfig(u, &commonIface.ServiceConfig{
		CommonConfig: commonIface.CommonConfig{Root: root, Port: p2p}, SwarmKey: u.SwarmKey(),
		Others: map[string]int{"http": httpPort, "p2p": p2p, "dns": dnsPort, "ipfs": 0},
	})
	assert.NilError(t, err)
	cfg := coloCfg{Config: base, services: svcs}

	nd.full, err = tauConfig.NewNode(ctx, cfg, path.Join(root, "node"))
	assert.NilError(t, err)
	nd.lite, err = peer.NewClientNode(ctx, nil, keypair.NewRaw(), u.SwarmKey(),
		[]string{fmt.Sprintf(dream.DefaultP2PListenFormat, liteP2P)}, nil, true,
		[]peercore.AddrInfo{{ID: nd.full.ID(), Addrs: nd.full.Peer().Addrs()}})
	assert.NilError(t, err)
	for _, other := range u.All() {
		connect(nd.lite, other)
	}
	cfg.SetNode(nd.full)
	cfg.SetClientNode(nd.lite)
	cfg.SetDatabases(kvdb.New(nd.full))

	nd.http, err = basicHttp.New(ctx, httpOptions.Listen(fmt.Sprintf("%s:%d", dream.DefaultHost, httpPort)))
	assert.NilError(t, err)
	cfg.SetHttp(nd.http)

	// the order of cli/node: substrate first, gateway last, so the catch-all
	// does not shadow substrate's websocket route
	nd.sub, err = substrate.New(ctx, cfg)
	assert.NilError(t, err)
	types := []seerIface.ServiceType{seerIface.ServiceTypeSubstrate}
	if colocated {
		nd.gw, err = gateway.New(ctx, cfg)
		assert.NilError(t, err)
		types = append(types, "gateway")
	}
	assert.NilError(t, seerClient.StartNodeBeacon(ctx, cfg, nd.lite, types...))
	nd.http.Start()

	u.Mesh(nd.full)
	c.nodes = append(c.nodes, nd)
	// the new full node must be reachable by the clients that already exist
	for _, other := range c.nodes {
		connect(other.lite, nd.full)
	}
}

func (c *cluster) deploy() {
	t := c.t
	c.project = id.Generate()
	pingId, upperId, siteId, pingPostId := id.Generate(), id.Generate(), id.Generate(), id.Generate()

	fs, _, err := tcc.GenerateProject(c.project,
		&structureSpec.Function{Id: pingPostId, Name: "pingPost", Type: "http", Call: "ping", Memory: 1 << 27, Timeout: 30_000_000_000, Method: "POST", Source: ".", Domains: []string{"d"}, Paths: []string{"/pingpost"}},
		&structureSpec.Function{Id: pingId, Name: "pingFn", Type: "http", Call: "ping", Memory: 1 << 27, Timeout: 30_000_000_000, Method: "GET", Source: ".", Domains: []string{"d"}, Paths: []string{"/ping"}},
		&structureSpec.Function{Id: upperId, Name: "upperFn", Type: "http", Call: "toUpper", Memory: 1 << 27, Timeout: 30_000_000_000, Method: "POST", Source: ".", Domains: []string{"d"}, Paths: []string{"/upper"}},
		&structureSpec.Website{Id: siteId, Name: "site", Domains: []string{"d"}, Paths: []string{"/"}, Provider: "github", RepoID: "123456", RepoName: "test/website"},
		&structureSpec.Messaging{Name: "wsMsg", Match: coloChannel, WebSocket: true},
		&structureSpec.Domain{Name: "d", Fqdn: coloFqdn},
	)
	assert.NilError(t, err)
	assert.NilError(t, c.u.RunFixture("injectProject", fs))

	wd, err := os.Getwd()
	assert.NilError(t, err)
	zipPath := coloSiteZip(t)
	for rid, files := range map[string][]string{
		pingPostId: {path.Join(wd, "fixtures", "ping.zwasm")},
		pingId:     {path.Join(wd, "fixtures", "ping.zwasm")},
		upperId:    {path.Join(wd, "fixtures", "toupper.zwasm")},
		siteId:     {zipPath},
	} {
		assert.NilError(t, c.u.RunFixture("compileFor", compile.BasicCompileFor{ProjectId: c.project, ResourceId: rid, Paths: files}))
	}
}

func coloSiteZip(t *testing.T) string {
	p := filepath.Join(t.TempDir(), "site.zip")
	f, err := os.Create(p)
	assert.NilError(t, err)
	zw := zip.NewWriter(f)
	for name, data := range map[string][]byte{
		"index.html": []byte("<html>HELLO-SITE</html>"),
		"big.bin":    bytes.Repeat([]byte("B"), 5<<20),
	} {
		w, err := zw.Create(name)
		assert.NilError(t, err)
		_, err = w.Write(data)
		assert.NilError(t, err)
	}
	assert.NilError(t, zw.Close())
	assert.NilError(t, f.Close())
	return p
}

// do sends a request to base with the project's domain as Host.
func (c *cluster) do(method, base, p string, body io.Reader) (*http.Response, []byte, time.Duration, error) {
	return c.doWith(c.client, method, base, p, coloFqdn, body)
}

func (c *cluster) doWith(cl *http.Client, method, base, p, host string, body io.Reader) (*http.Response, []byte, time.Duration, error) {
	req, err := http.NewRequest(method, base+p, body)
	if err != nil {
		return nil, nil, 0, err
	}
	req.Host = host
	start := time.Now()
	res, err := cl.Do(req)
	if err != nil {
		return nil, nil, time.Since(start), err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	return res, data, time.Since(start), err
}

func (c *cluster) get(base, p string) (int, string, error) {
	res, data, _, err := c.do("GET", base, p, nil)
	if err != nil {
		return 0, "", err
	}
	return res.StatusCode, string(data), nil
}

func coloEventually(d time.Duration, f func() bool) bool {
	for deadline := time.Now().Add(d); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		if f() {
			return true
		}
	}
	return f()
}

// warm waits until every node answers /ping through its own port.
func (c *cluster) warm() {
	assert.Assert(c.t, coloEventually(90*time.Second, func() bool {
		for _, n := range c.nodes {
			if code, body, err := c.get(n.base(), "/ping"); err != nil || code != 200 || body != "PONG" {
				return false
			}
		}
		return true
	}), "/ping never reached 200 on every node")
}

func coloDNS(port int, name string) ([]string, int, error) {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), dns.TypeA)
	r, _, err := (&dns.Client{Timeout: 3 * time.Second}).Exchange(m, fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, -1, err
	}
	var ips []string
	for _, a := range r.Answer {
		if rr, ok := a.(*dns.A); ok {
			ips = append(ips, rr.A.String())
		}
	}
	sort.Strings(ips)
	return ips, r.Rcode, nil
}

// fakeBeat announces a service with a made-up IP (Dream nodes are all 127.0.0.1).
func (c *cluster) fakeBeat(simple, svc, ip string) (stop func()) {
	s, err := c.u.Simple(simple)
	assert.NilError(c.t, err)
	sc, err := s.Seer()
	assert.NilError(c.t, err)
	services := seerIface.Services{{Type: seerIface.ServiceType(svc), Meta: map[string]string{"IP": ip, "cluster": "main"}}}
	done := make(chan struct{})
	beat := func() {
		sc.Usage().Announce(services, "", "", nil)
		sc.Usage().Heartbeat(&seerIface.UsageData{}, simple, "", "", nil)
	}
	beat()
	go func() {
		tk := time.NewTicker(3 * time.Second)
		defer tk.Stop()
		for {
			select {
			case <-done:
				return
			case <-tk.C:
				beat()
			}
		}
	}()
	return func() { close(done) }
}

type coloStats struct {
	durs   []time.Duration
	errs   int
	status map[int]int
}

func coloMeasure(n int, f func() (int, time.Duration, error)) coloStats {
	s := coloStats{status: map[int]int{}}
	for range n {
		code, d, err := f()
		if err != nil {
			s.errs++
			continue
		}
		s.status[code]++
		s.durs = append(s.durs, d)
	}
	return s
}

func (s coloStats) pct(p float64) time.Duration {
	if len(s.durs) == 0 {
		return 0
	}
	d := slices.Clone(s.durs)
	slices.Sort(d)
	return d[int(float64(len(d)-1)*p)]
}

func (s coloStats) String() string {
	return fmt.Sprintf("n=%d p50=%v p95=%v max=%v status=%v errs=%d", len(s.durs), s.pct(.5).Round(10*time.Microsecond), s.pct(.95).Round(10*time.Microsecond), s.pct(1).Round(10*time.Microsecond), s.status, s.errs)
}

func (c *cluster) ping(base string) (int, time.Duration, error) {
	res, data, d, err := c.do("GET", base, "/ping", nil)
	if err != nil {
		return 0, d, err
	}
	if res.StatusCode == 200 && string(data) != "PONG" {
		return res.StatusCode, d, fmt.Errorf("body %q", data)
	}
	return res.StatusCode, d, nil
}

// TestColocated runs the whole matrix against 1 and then 3 nodes that each run
// gateway and substrate. A separate substrate-only universe is the "direct"
// baseline for the latency comparison.
func TestColocated(t *testing.T) {
	seerSvc.ServerIpCacheTTL = 500 * time.Millisecond

	direct := newCluster(t, "gwdirect", 1, false)
	direct.deploy()
	direct.warm()

	for _, n := range []int{1, 3} {
		t.Run(fmt.Sprintf("%dnode", n), func(t *testing.T) { coloMatrix(t, n, direct) })
	}
}

func coloMatrix(t *testing.T, n int, direct *cluster) {
	c := newCluster(t, fmt.Sprintf("gwcolo%d", n), n, true, "fakegw", "fakesub")
	c.deploy()
	c.warm()
	ports := func() []string {
		var b []string
		for _, nd := range c.nodes {
			b = append(b, nd.base())
		}
		return b
	}()
	pids := map[string]bool{}
	for _, nd := range c.nodes {
		pids[nd.pid()] = true
	}

	t.Run("function GET on every node", func(t *testing.T) {
		// proves: each node's gateway reaches a substrate (its own or another) and the function answers
		for _, base := range ports {
			for range 30 {
				res, data, _, err := c.do("GET", base, "/ping", nil)
				assert.NilError(t, err)
				assert.Equal(t, res.StatusCode, 200)
				assert.Equal(t, string(data), "PONG")
				assert.Assert(t, pids[res.Header.Get(gateway.ProxyHeader)], "X-Substrate-Peer %q is not one of the nodes", res.Header.Get(gateway.ProxyHeader))
			}
		}
	})

	t.Run("distribution of idle GETs (informational)", func(t *testing.T) {
		// proves nothing: logs which nodes one gateway picks when all are idle (balancing is by free capacity, so ties may all go to one node)
		seen := map[string]int{}
		for range 90 {
			if res, _, _, err := c.do("GET", ports[0], "/ping", nil); err == nil {
				seen[res.Header.Get(gateway.ProxyHeader)]++
			}
		}
		t.Logf("distribution over %d nodes: %v", n, seen)
	})

	t.Run("POST echo 1 and 8 MiB", func(t *testing.T) {
		// proves: request bodies cross the tunnel intact in both directions, from every node's gateway
		for _, base := range ports {
			for _, sz := range []int{1 << 20, 8 << 20} {
				big := bytes.Repeat([]byte("a"), sz)
				res, data, took, err := c.do("POST", base, "/upper", bytes.NewReader(big))
				assert.NilError(t, err, "%d MiB", sz>>20)
				assert.Equal(t, res.StatusCode, 200, "%d MiB: %.80s", sz>>20, data)
				assert.Equal(t, len(data), sz, "%d MiB response length", sz>>20)
				assert.Assert(t, bytes.Equal(data, bytes.ToUpper(big)), "%d MiB body differs", sz>>20)
				t.Logf("%s %d MiB took %v", base, sz>>20, took.Round(time.Millisecond))
			}
		}
	})

	t.Run("website page and asset", func(t *testing.T) {
		// proves: the website route works from every gateway, including a 5 MiB asset
		for _, base := range ports {
			assert.Assert(t, coloEventually(30*time.Second, func() bool {
				code, body, err := c.get(base, "/")
				return err == nil && code == 200 && strings.Contains(body, "HELLO-SITE")
			}), "page")
			res, data, _, err := c.do("GET", base, "/big.bin", nil)
			assert.NilError(t, err)
			assert.Equal(t, res.StatusCode, 200)
			assert.Equal(t, len(data), 5<<20)
		}
	})

	t.Run("404 for an unknown path and an unknown host", func(t *testing.T) {
		// proves: a route that exists nowhere is a definite 404 (not a 503 or a hang) for a known host and for an unknown one
		for _, base := range ports {
			res, data, _, err := c.do("POST", base, "/nothing-here", strings.NewReader("x"))
			assert.NilError(t, err)
			assert.Equal(t, res.StatusCode, 404, "known host, POST /nothing-here: %.100s", data)

			res, data, _, err = c.doWith(c.client, "GET", base, "/ping", "unknown.example.org", nil)
			assert.NilError(t, err)
			assert.Equal(t, res.StatusCode, 404, "unknown host: %.100s", data)
		}
	})

	t.Run("pubsub websocket handshake 101", func(t *testing.T) {
		// proves: /ws-* is answered by the substrate route on the shared http service, ahead of the gateway catch-all
		wsPath := fmt.Sprintf("/ws-%s/%s", multihash.Hash(c.project), coloChannel)
		d := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
		for _, nd := range c.nodes {
			conn, resp, err := d.Dial(fmt.Sprintf("ws://127.0.0.1:%d%s", nd.port, wsPath), http.Header{"Host": []string{coloFqdn}})
			if conn != nil {
				conn.Close()
			}
			assert.Assert(t, err == nil && resp != nil && resp.StatusCode == 101, "node %s: resp=%v err=%v", nd.pid(), resp, err)
		}
	})

	t.Run("http port redirect still there", func(t *testing.T) {
		// proves: with a gateway on the node, substrate still serves its plain-http redirect to https (the port after the https one)
		noFollow := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		for _, nd := range c.nodes {
			res, _, _, err := c.doWith(noFollow, "GET", fmt.Sprintf("http://127.0.0.1:%d", nd.redirectPort), "/ping?x=1", coloFqdn, nil)
			assert.NilError(t, err)
			assert.Equal(t, res.StatusCode, http.StatusTemporaryRedirect)
			assert.Equal(t, res.Header.Get("Location"), "https://"+coloFqdn+"/ping?x=1")
		}
	})

	t.Run("DNS returns the gateway nodes", func(t *testing.T) {
		// proves: seer answers generated-domain lookups with the IPs of nodes that announce a gateway (the colocated nodes and a fake
		// gateway-only IP), never a fake substrate-only IP
		domain := "abc.g." + c.u.Name() + ".localtau"
		stopSub := c.fakeBeat("fakesub", "substrate", "10.8.8.8")
		stopGw := c.fakeBeat("fakegw", "gateway", "10.9.9.9")
		defer stopSub()
		defer stopGw()
		var ips []string
		coloEventually(30*time.Second, func() bool {
			ips, _, _ = coloDNS(c.dnsPort, domain)
			return slices.Contains(ips, "10.9.9.9") && slices.Contains(ips, "127.0.0.1")
		})
		t.Logf("answers=%v", ips)
		assert.Assert(t, slices.Contains(ips, "10.9.9.9"), "fake gateway IP missing: %v", ips)
		assert.Assert(t, slices.Contains(ips, "127.0.0.1"), "colocated nodes (127.0.0.1) missing: %v", ips)
		assert.Assert(t, !slices.Contains(ips, "10.8.8.8"), "substrate-only IP must not be answered: %v", ips)
		_, code, _ := coloDNS(c.dnsPort, "nothing.example.org")
		assert.Equal(t, code, dns.RcodeNameError, "unknown domain")
	})

	t.Run("large POST to a function that ignores its body returns promptly", func(t *testing.T) {
		// proves: a function that never reads its body does not leave the gateway hanging on the unread upload.
		// KNOWN BUG: expected to fail until the tunnel fix lands (the request hangs).
		short := &http.Client{Timeout: 15 * time.Second}
		assert.Assert(t, coloEventually(45*time.Second, func() bool {
			res, _, _, err := c.doWith(short, "POST", ports[0], "/pingpost", coloFqdn, strings.NewReader("x"))
			return err == nil && res.StatusCode == 200
		}), "small POST /pingpost never worked")
		for _, base := range ports {
			for _, sz := range []int{128 << 10, 8 << 20} {
				res, data, took, err := c.doWith(short, "POST", base, "/pingpost", coloFqdn, bytes.NewReader(make([]byte, sz)))
				assert.NilError(t, err, "%d KiB hung (took %v)", sz>>10, took.Round(time.Millisecond))
				assert.Equal(t, res.StatusCode, 200, "%d KiB: %.80s", sz>>10, data)
				assert.Assert(t, took < 5*time.Second, "%d KiB took %v", sz>>10, took)
			}
		}
	})

	t.Run("latency over 300 sequential GETs vs direct", func(t *testing.T) {
		// proves: the gateway hop (lookup + tunnel, same node or another) adds only a small, bounded cost over serving the same
		// function straight from a substrate-only node.
		for range 30 {
			c.ping(ports[0])
			direct.ping(direct.nodes[0].base())
		}
		g := coloMeasure(300, func() (int, time.Duration, error) { return c.ping(ports[0]) })
		d := coloMeasure(300, func() (int, time.Duration, error) { return direct.ping(direct.nodes[0].base()) })
		t.Logf("PERF direct  (substrate only): %s", d)
		t.Logf("PERF gateway (%d nodes):        %s", n, g)
		assert.Equal(t, g.errs, 0, "gateway errors")
		assert.Equal(t, g.status[200], 300, "gateway statuses %v", g.status)
		assert.Assert(t, g.pct(.5) <= d.pct(.5)+10*time.Millisecond, "gateway p50 %v vs direct p50 %v", g.pct(.5), d.pct(.5))
		assert.Assert(t, g.pct(.95) <= d.pct(.95)+30*time.Millisecond, "gateway p95 %v vs direct p95 %v", g.pct(.95), d.pct(.95))
	})

	if n < 3 {
		return
	}

	// destructive: keep last
	t.Run("killing one of three nodes leaves 0 errors after failover", func(t *testing.T) {
		// proves: clients sent to the surviving gateways see no error once failover is done; the dead node's
		// substrate leaves the rotation. Requests before the settle time are logged, not counted.
		const settle = 8 * time.Second
		survivors := c.nodes[1:]
		type sample struct {
			at     time.Duration
			status int
			err    string
			method string
		}
		var (
			mu      sync.Mutex
			samples []sample
			stop    atomic.Bool
			wg      sync.WaitGroup
		)
		begin := time.Now()
		for w := range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; !stop.Load(); i++ {
					base := survivors[(w+i)%len(survivors)].base()
					method, p, body := "GET", "/ping", io.Reader(nil)
					if w%4 == 3 {
						method, p, body = "POST", "/upper", strings.NewReader("x")
					}
					at := time.Since(begin)
					res, _, _, err := c.do(method, base, p, body)
					s := sample{at: at, method: method}
					if err != nil {
						s.err = err.Error()
					} else {
						s.status = res.StatusCode
					}
					mu.Lock()
					samples = append(samples, s)
					mu.Unlock()
					time.Sleep(10 * time.Millisecond)
				}
			}()
		}
		time.Sleep(4 * time.Second)
		killAt := time.Since(begin)
		c.nodes[0].close()
		time.Sleep(settle + 6*time.Second)
		stop.Store(true)
		wg.Wait()

		var early, late, lateBad int
		var examples []string
		for _, s := range samples {
			bad := s.err != "" || s.status != 200
			switch {
			case s.at < killAt:
				assert.Assert(t, !bad, "error before the kill: %v", s)
			case s.at < killAt+settle:
				if bad {
					early++
				}
			default:
				late++
				if bad {
					lateBad++
					if len(examples) < 5 {
						examples = append(examples, fmt.Sprintf("%s status=%d err=%s", s.method, s.status, s.err))
					}
				}
			}
		}
		t.Logf("%d requests; failed during the %v after the kill: %d; after: %d of %d; examples %v", len(samples), settle, early, lateBad, late, examples)
		assert.Equal(t, lateBad, 0, "errors after failover: %v", examples)
	})
}

// held is a request the test keeps inside one node's function, so that node
// (admission limit 1) has no free slot. It goes through the substrate tunnel
// to that exact node, since a gateway would choose the node itself.
type held struct {
	pw   *io.PipeWriter
	done chan struct{}
}

func (h *held) release() { h.pw.Close(); <-h.done }

// coloOnly sends one request to node nd alone and returns Frontend's error and the recorder.
func coloOnly(sc coreSubstrate.ProxyClient, nd *coloNode, method, p string, body io.Reader) (*httptest.ResponseRecorder, error) {
	ch, err := sc.ProxyHTTP(coloFqdn, p, method)
	if err != nil {
		return nil, err
	}
	var mine *client.Response
	for res := range ch {
		if res.Error() == nil && res.PID() == nd.full.ID() && mine == nil {
			mine = res
		} else {
			res.Close()
		}
	}
	if mine == nil {
		return nil, fmt.Errorf("node %s did not answer the lookup", nd.pid())
	}
	req := httptest.NewRequest(method, "http://"+coloFqdn+p, body)
	req.Host = coloFqdn
	rec := httptest.NewRecorder()
	return rec, tunnel.Frontend(rec, req, mine)
}

// TestColocatedBusy runs two colocated nodes with an admission limit of 1.
func TestColocatedBusy(t *testing.T) {
	t.Setenv("TAU_MAX_INFLIGHT", "1")
	c := newCluster(t, "gwcolobusy", 2, true)
	c.deploy()
	c.warm()
	a, b := c.nodes[0], c.nodes[1]
	assert.Assert(t, coloEventually(45*time.Second, func() bool {
		code, body, err := c.get(a.base(), "/ping")
		return err == nil && code == 200 && body == "PONG"
	}))
	sc, err := substrateClient.New(t.Context(), a.lite)
	assert.NilError(t, err)
	t.Cleanup(func() { sc.Close() })

	hold := func(nd *coloNode) *held {
		pr, pw := io.Pipe()
		h := &held{pw: pw, done: make(chan struct{})}
		go func() {
			defer close(h.done)
			coloOnly(sc, nd, "POST", "/upper", pr)
		}()
		pw.Write([]byte("x")) // toUpper is now inside its body read, holding the only slot
		return h
	}
	// isFull is true when the node refuses a request before running it
	isFull := func(nd *coloNode) bool {
		_, err := coloOnlyErr(sc, nd)
		return errors.Is(err, tunnel.ErrRefused)
	}

	t.Run("a busy node's GETs go to the free one", func(t *testing.T) {
		// proves: with node A out of slots, GETs entering at A's gateway or B's are served (200) by B, never refused
		h := hold(a)
		defer h.release()
		assert.Assert(t, coloEventually(5*time.Second, func() bool { return isFull(a) }), "A never reported full")
		seen := map[string]int{}
		for _, nd := range c.nodes {
			for range 30 {
				res, _, _, err := c.do("GET", nd.base(), "/ping", nil)
				assert.NilError(t, err)
				assert.Equal(t, res.StatusCode, 200, "entering at %s", nd.pid())
				seen[res.Header.Get(gateway.ProxyHeader)]++
			}
		}
		t.Logf("served by %v (A=%s B=%s)", seen, a.pid(), b.pid())
		assert.Equal(t, seen[a.pid()], 0, "a node with no free slot was picked: %v", seen)
	})

	t.Run("POST is sent exactly once", func(t *testing.T) {
		// proves: a write the busy node would refuse is never replayed. Both nodes full: the POST costs one admission wait
		// (75 ms, one node tried), the GET two (both tried), and the POST ends 503. Inferred from timing: nothing counts the
		// executions inside the function.
		h1, h2 := hold(a), hold(b)
		defer h1.release()
		defer h2.release()
		assert.Assert(t, coloEventually(5*time.Second, func() bool { return isFull(a) && isFull(b) }), "nodes never both full")
		gres, _, gd, gerr := c.do("GET", a.base(), "/ping", nil)
		pres, pdata, pd, perr := c.do("POST", a.base(), "/upper", strings.NewReader("y"))
		assert.NilError(t, gerr)
		assert.NilError(t, perr)
		t.Logf("GET status=%d took=%v | POST status=%d took=%v body=%.80q", gres.StatusCode, gd, pres.StatusCode, pd, pdata)
		assert.Equal(t, gres.StatusCode, 503)
		assert.Equal(t, gres.Header.Get("Retry-After"), "1")
		assert.Equal(t, pres.StatusCode, 503)
		assert.Assert(t, gd > pd+40*time.Millisecond, "GET %v should have tried a second node, POST %v only one", gd, pd)
	})

	t.Run("a refused GET is moved to another node", func(t *testing.T) {
		// proves: when a node refuses a GET, the gateway sends it to the next node instead of failing. Both nodes are full when
		// the lookup answers and one frees up in the 75 ms the first attempt spends waiting, so the retry lands; the timing is
		// not exact, so the test gives it several tries.
		wins := 0
		var last string
		for try := 0; try < 8 && wins < 2; try++ {
			h1, h2 := hold(a), hold(b)
			full := coloEventually(3*time.Second, func() bool { return isFull(a) && isFull(b) })
			ch := make(chan string, 1)
			go func() {
				res, _, d, err := c.do("GET", a.base(), "/ping", nil)
				if err != nil {
					ch <- err.Error()
					return
				}
				ch <- fmt.Sprintf("status=%d took=%v", res.StatusCode, d.Round(time.Millisecond))
			}()
			time.Sleep(60 * time.Millisecond)
			go h1.release()
			go h2.release()
			last = <-ch
			if full && strings.HasPrefix(last, "status=200") {
				wins++
			}
			t.Logf("try %d full=%v -> %s", try, full, last)
			time.Sleep(300 * time.Millisecond)
		}
		assert.Assert(t, wins >= 1, "no GET was moved to another node; last: %s", last)
	})
}

// coloOnlyErr is a GET /ping to one node, discarding the body.
func coloOnlyErr(sc coreSubstrate.ProxyClient, nd *coloNode) (int, error) {
	rec, err := coloOnly(sc, nd, "GET", "/ping", nil)
	if rec == nil {
		return 0, err
	}
	return rec.Code, err
}
