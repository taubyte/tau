//go:build raft_integration

package raft

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ipfs/go-datastore/query"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	peercore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/keypair"
	taupeer "github.com/taubyte/tau/p2p/peer"
)

// Groups across processes: every node here is a real libp2p node in an OS process of
// its own, on the machine's own address rather than loopback. libp2p limits identify
// pushes per /24 and exempts loopback, so only here do the nodes learn of a protocol
// registered after they met at the rate a datacenter subnet gets.

// A child is this test binary started with processDir set: one node, driven a line
// at a time over stdin, answering a line per command on stdout.
const (
	processDir    = "TAU_RAFT_PROCESS_DIR"
	processKey    = "TAU_RAFT_PROCESS_KEY"
	processListen = "TAU_RAFT_PROCESS_LISTEN"
	// processLogs, when set, is a directory each child's whole log is kept in.
	processLogs = "TAU_RAFT_PROCESS_LOGS"
)

func TestMain(m *testing.M) {
	if dir := os.Getenv(processDir); dir != "" {
		os.Exit(runProcess(dir))
	}
	os.Exit(m.Run())
}

// runProcess is a child: it reports its address, then answers commands until quit.
func runProcess(dir string) int {
	key, err := hex.DecodeString(os.Getenv(processKey))
	if err != nil {
		fmt.Println("err", err)
		return 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	node, err := taupeer.New(ctx, dir, key, nil, []string{os.Getenv(processListen)}, nil, true, false)
	if err != nil {
		fmt.Println("err", err)
		return 1
	}
	defer node.Close()
	fmt.Printf("ready %s/p2p/%s\n", node.Host().Addrs()[0], node.ID())

	groups := map[string]Cluster{}
	defer func() {
		for _, g := range groups {
			g.Close()
		}
	}()
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		f := strings.Fields(in.Text())
		if len(f) == 0 {
			continue
		}
		// ONE LINE A REPLY: an error libp2p writes over several lines would
		// otherwise answer the commands after it.
		fmt.Println(strings.ReplaceAll(answer(ctx, node, groups, f), "\n", " | "))
		if f[0] == "quit" {
			return 0
		}
	}
	return 0
}

// meet dials at until the two are connected. A dial that crosses one from the
// other side at the same instant, each from its listening port, is one TCP connection
// both ends took for their own, and its TLS handshake fails on both; a peer keeps
// dialing, as the DHT and a stream that wants one do, and the next dial lands.
func meet(ctx context.Context, node taupeer.Node, at peercore.AddrInfo) string {
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := node.Host().Connect(network.WithForceDirectDial(ctx, "a dial that crossed"), at)
		if err == nil || node.Host().Network().Connectedness(at.ID) == network.Connected {
			return "ok"
		}
		if time.Now().After(deadline) {
			return "err " + err.Error()
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// answer is one command's one-line reply.
func answer(ctx context.Context, node taupeer.Node, groups map[string]Cluster, f []string) string {
	switch {
	case f[0] == "open" && len(f) == 3:
		window, err := time.ParseDuration(f[2])
		if err != nil {
			return "err " + err.Error()
		}
		g, err := New(node, f[1], WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(window))
		if err != nil {
			return "err " + err.Error()
		}
		groups[f[1]] = g
		return "ok"
	case f[0] == "connect" && len(f) == 2:
		at, err := peercore.AddrInfoFromString(f[1])
		if err != nil {
			return "err " + err.Error()
		}
		return meet(ctx, node, *at)
	case f[0] == "dial" && len(f) == 2:
		// One dial, as a stream that wants a connection makes it.
		at, err := peercore.AddrInfoFromString(f[1])
		if err != nil {
			return "err " + err.Error()
		}
		if err := node.Host().Connect(network.WithForceDirectDial(ctx, "one dial"), *at); err != nil {
			return "err " + err.Error()
		}
		return "ok"
	case f[0] == "drop" && len(f) == 2:
		id, err := peercore.Decode(f[1])
		if err != nil {
			return "err " + err.Error()
		}
		if err := node.Host().Network().ClosePeer(id); err != nil {
			return "err " + err.Error()
		}
		return "ok"
	case f[0] == "ports" && len(f) == 2:
		// The port this node listens on, and the local port of each connection it
		// dialed to the peer.
		id, err := peercore.Decode(f[1])
		if err != nil {
			return "err " + err.Error()
		}
		listen, err := node.Host().Addrs()[0].ValueForProtocol(multiaddr.P_TCP)
		if err != nil {
			return "err " + err.Error()
		}
		out := "listen " + listen
		for _, c := range node.Host().Network().ConnsToPeer(id) {
			if c.Stat().Direction != network.DirOutbound {
				continue
			}
			local, err := c.LocalMultiaddr().ValueForProtocol(multiaddr.P_TCP)
			if err != nil {
				return "err " + err.Error()
			}
			out += " dialed " + local
		}
		return out
	case f[0] == "set" && len(f) == 4 && groups[f[1]] != nil:
		if err := groups[f[1]].Set(f[2], []byte(f[3]), 2*time.Second); err != nil {
			return "err " + err.Error()
		}
		return "ok"
	case f[0] == "get" && len(f) == 3 && groups[f[1]] != nil:
		v, ok := groups[f[1]].Get(f[2])
		if !ok {
			return "none"
		}
		return "val " + string(v)
	case f[0] == "leads" && len(f) == 2 && groups[f[1]] != nil:
		return "leads " + strconv.FormatBool(groups[f[1]].IsLeader())
	case f[0] == "members" && len(f) == 2 && groups[f[1]] != nil:
		held, err := groups[f[1]].Members()
		if err != nil {
			return "members 0"
		}
		return "members " + strconv.Itoa(len(held))
	case f[0] == "keys" && len(f) == 2:
		results, err := node.Store().Query(ctx, query.Query{Prefix: f[1], KeysOnly: true})
		if err != nil {
			return "err " + err.Error()
		}
		entries, err := results.Rest()
		if err != nil {
			return "err " + err.Error()
		}
		return "keys " + strconv.Itoa(len(entries))
	case f[0] == "quit":
		return "bye"
	}
	return "err unknown command " + strings.Join(f, " ")
}

// process is a child as its parent drives it.
type process struct {
	t    *testing.T
	dir  string
	logs string
	key  []byte
	ip   string
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Scanner
	addr string
	mu   sync.Mutex
}

// lanIP is this machine's own IPv4 address, not loopback: a test here means to run
// under the limits a subnet gets.
func lanIP(t *testing.T) string {
	t.Helper()
	ifaces, err := net.Interfaces()
	require.NoError(t, err)
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				if ip := ipn.IP.To4(); ip != nil && !ip.IsLinkLocalUnicast() {
					return ip.String()
				}
			}
		}
	}
	t.Fatal("these tests need a non-loopback IPv4 address: loopback is exempt from the identify push limits they run under")
	return ""
}

// spawn starts a child for a new node.
func spawn(t *testing.T, ip string) *process {
	t.Helper()
	logs := t.TempDir()
	if kept := os.Getenv(processLogs); kept != "" {
		logs = filepath.Join(kept, strings.ReplaceAll(t.Name(), "/", "-"))
		require.NoError(t, os.MkdirAll(logs, 0o755))
	}
	key := keypair.NewRaw()
	p := &process{t: t, dir: t.TempDir(), key: key, ip: ip}
	private, err := crypto.UnmarshalPrivateKey(key)
	require.NoError(t, err)
	id, err := peercore.IDFromPrivateKey(private)
	require.NoError(t, err)
	p.logs = filepath.Join(logs, id.String()+".log")
	p.start()
	return p
}

// start runs the child, the same node each time: its key and its directory.
func (p *process) start() {
	p.t.Helper()
	self, err := os.Executable()
	require.NoError(p.t, err)
	cmd := exec.Command(self, "-test.run=^$")
	cmd.Env = append(os.Environ(),
		processDir+"="+p.dir,
		processKey+"="+hex.EncodeToString(p.key),
		processListen+"=/ip4/"+p.ip+"/tcp/0",
	)
	logs, err := os.OpenFile(p.logs, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	require.NoError(p.t, err)
	cmd.Stderr = logs
	in, err := cmd.StdinPipe()
	require.NoError(p.t, err)
	out, err := cmd.StdoutPipe()
	require.NoError(p.t, err)
	require.NoError(p.t, cmd.Start())
	p.cmd, p.in, p.out = cmd, in, bufio.NewScanner(out)
	p.t.Cleanup(func() {
		p.stop()
		logs.Close()
		if p.t.Failed() {
			p.tail()
		}
	})

	ready := p.line()
	addr, ok := strings.CutPrefix(ready, "ready ")
	require.True(p.t, ok, "the child started as %q", ready)
	p.addr = addr
}

// tail is the end of what the child logged, for a test that failed.
func (p *process) tail() {
	raw, err := os.ReadFile(p.logs)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	p.t.Logf("%s logged, last lines:\n%s", p.addr, strings.Join(lines[max(0, len(lines)-60):], "\n"))
}

// stop ends the child, killing it when it does not quit.
func (p *process) stop() {
	if p.cmd == nil || p.cmd.ProcessState != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		p.cmd.Wait()
		close(done)
	}()
	fmt.Fprintln(p.in, "quit")
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		p.cmd.Process.Kill()
		<-done
	}
}

// kill ends the child at once, as a machine losing power does.
func (p *process) kill() {
	p.t.Helper()
	require.NoError(p.t, p.cmd.Process.Kill())
	p.cmd.Wait()
}

func (p *process) line() string {
	p.t.Helper()
	if !p.out.Scan() {
		p.t.Fatalf("the child ended: %v", p.out.Err())
	}
	return p.out.Text()
}

// do sends one command and returns its reply.
func (p *process) do(format string, args ...any) string {
	p.t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	_, err := fmt.Fprintf(p.in, format+"\n", args...)
	require.NoError(p.t, err)
	return p.line()
}

// id is the child's peer id, the last part of its address.
func (p *process) id() string { return p.addr[strings.LastIndex(p.addr, "/")+1:] }

func (p *process) open(namespace string, window time.Duration) {
	p.t.Helper()
	require.Equal(p.t, "ok", p.do("open %s %s", namespace, window))
}

func (p *process) connect(other *process) {
	p.t.Helper()
	require.Equal(p.t, "ok", p.do("connect %s", other.addr))
}

func (p *process) holds(namespace, key, value string) bool {
	return p.do("get %s %s", namespace, key) == "val "+value
}

func (p *process) leads(namespace string) bool {
	return p.do("leads %s", namespace) == "leads true"
}

func (p *process) members(namespace string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(p.do("members %s", namespace), "members "))
	return n
}

// written is a write made through whichever process leads namespace's group.
func written(namespace, key, value string, all ...*process) func() bool {
	return func() bool {
		for _, p := range all {
			if p.do("set %s %s %s", namespace, key, value) == "ok" {
				return true
			}
		}
		return false
	}
}

// counts is what each process last counted, said when an assertion gives up.
type counts struct{ last []int }

func (c *counts) String() string { return fmt.Sprintf("members per node: %v", c.last) }

// oneGroup is every process counting want members in namespace, each count kept in
// seen.
func oneGroup(seen *counts, namespace string, want int, all ...*process) func() bool {
	return func() bool {
		seen.last = seen.last[:0]
		ok := true
		for _, p := range all {
			n := p.members(namespace)
			seen.last = append(seen.last, n)
			ok = ok && n == want
		}
		return ok
	}
}

// GROUPS FORMED APART BECOME ONE WITHIN SECONDS OF BEING CONNECTED, not on a settled
// discovery pass: each node opened its group before it was connected to anything.
func TestAcrossProcesses_GroupsFormedApartBecomeOneOnceConnected(t *testing.T) {
	ip := lanIP(t)
	seen := &counts{}
	a, b := spawn(t, ip), spawn(t, ip)
	a.open("apart", time.Second)
	b.open("apart", time.Second)
	require.Eventually(t, oneGroup(seen, "apart", 1, a, b), 10*time.Second, 100*time.Millisecond, "%s", seen)
	require.Eventually(t, written("apart", "from-a", "a", a), 10*time.Second, 100*time.Millisecond)
	require.Eventually(t, written("apart", "from-b", "b", b), 10*time.Second, 100*time.Millisecond)

	b.connect(a)
	require.Eventually(t, oneGroup(seen, "apart", 2, a, b), 10*time.Second, 100*time.Millisecond,
		"two connected groups stayed apart (%s)", seen)
	require.Eventually(t, func() bool {
		return a.holds("apart", "from-a", "a") && a.holds("apart", "from-b", "b") &&
			b.holds("apart", "from-a", "a") && b.holds("apart", "from-b", "b")
	}, 10*time.Second, 100*time.Millisecond, "the group they became lost a side's write")
}

// TWO NODES DIALING EACH OTHER AT THE SAME INSTANT BOTH CONNECT: a node of the cloud
// dials from a port of its own, so the two dials are two connections. From the
// listening ports they would be one, which both took for their own dial, and its TLS
// handshake would fail on both ends. Five rounds stay within the connections a node
// takes from one address before its rate limit refuses them.
func TestAcrossProcesses_TwoNodesDialingEachOtherAtOnceBothConnect(t *testing.T) {
	ip := lanIP(t)
	a, b := spawn(t, ip), spawn(t, ip)
	dialed := 0
	for round := range 5 {
		replies := make([]string, 2)
		var wg sync.WaitGroup
		for i, pair := range [][2]*process{{a, b}, {b, a}} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				replies[i] = pair[0].do("dial %s", pair[1].addr)
			}()
		}
		wg.Wait()
		require.Equal(t, []string{"ok", "ok"}, replies, "round %d", round)

		ports := strings.Fields(a.do("ports %s", b.id()))
		require.Equal(t, "listen", ports[0], "%v", ports)
		for i := 2; i+1 < len(ports); i += 2 {
			require.NotEqual(t, ports[1], ports[i+1], "a dialed from its listening port")
			dialed++
		}
		require.Equal(t, "ok", a.do("drop %s", b.id()))
		require.Equal(t, "ok", b.do("drop %s", a.id()))
	}
	require.NotZero(t, dialed, "no round left a connection a dialed, so no port was checked")
}

// A NAMESPACE OPENED AFTER THE NODES MET reaches the others only by identify push,
// which a subnet gets one of every five seconds after a burst of ten: five nodes
// opening it at once still form one group.
func TestAcrossProcesses_ANamespaceOpenedAfterTheNodesMetIsOneGroup(t *testing.T) {
	ip := lanIP(t)
	seen := &counts{}
	var all []*process
	for range 5 {
		all = append(all, spawn(t, ip))
	}
	for i, p := range all {
		for _, other := range all[i+1:] {
			p.connect(other)
		}
	}

	var wg sync.WaitGroup
	for _, p := range all {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.open("met-first", 2*time.Second)
		}()
	}
	wg.Wait()
	require.Eventually(t, oneGroup(seen, "met-first", 5, all...), 20*time.Second, 200*time.Millisecond,
		"nodes that met before opening the namespace did not form one group (%s)", seen)
}

// A NODE REACHED ONLY THROUGH ANOTHER joins the group too: a line of three, each
// formed apart, connected end to middle to end.
func TestAcrossProcesses_ALineOfNodesFormedApartBecomesOneGroup(t *testing.T) {
	ip := lanIP(t)
	seen := &counts{}
	a, b, c := spawn(t, ip), spawn(t, ip), spawn(t, ip)
	for _, p := range []*process{a, b, c} {
		p.open("line", time.Second)
	}
	require.Eventually(t, oneGroup(seen, "line", 1, a, b, c), 10*time.Second, 100*time.Millisecond, "%s", seen)

	a.connect(b)
	c.connect(b)
	require.Eventually(t, oneGroup(seen, "line", 3, a, b, c), 20*time.Second, 200*time.Millisecond,
		"the line did not become one group (%s)", seen)
}

// A NODE THAT LOST POWER COMES BACK INTO ITS GROUP: restarted with what it held, it
// opens its group before its connections are up, as a booting node does.
func TestAcrossProcesses_ARestartedNodeRejoinsItsGroup(t *testing.T) {
	ip := lanIP(t)
	seen := &counts{}
	a, b, c := spawn(t, ip), spawn(t, ip), spawn(t, ip)
	a.connect(b)
	a.connect(c)
	b.connect(c)
	for _, p := range []*process{a, b, c} {
		p.open("reboot", 2*time.Second)
	}
	require.Eventually(t, oneGroup(seen, "reboot", 3, a, b, c), 20*time.Second, 200*time.Millisecond, "%s", seen)
	require.Eventually(t, written("reboot", "before", "1", a, b, c), 10*time.Second, 200*time.Millisecond)
	require.Eventually(t, func() bool { return c.holds("reboot", "before", "1") }, 10*time.Second, 100*time.Millisecond)

	c.kill()
	c.start()
	c.open("reboot", 2*time.Second)
	c.connect(a)
	c.connect(b)

	require.Eventually(t, oneGroup(seen, "reboot", 3, a, b, c), 20*time.Second, 200*time.Millisecond,
		"the restarted node is not back in its group (%s)", seen)
	require.Eventually(t, written("reboot", "after", "2", a, b, c), 10*time.Second, 200*time.Millisecond,
		"the group took no write once the node was back")
	require.Eventually(t, func() bool {
		return c.holds("reboot", "before", "1") && c.holds("reboot", "after", "2")
	}, 10*time.Second, 100*time.Millisecond, "the restarted node does not hold what the group committed")
}

// A NODE BACK WITH ITS DISK LOST, UNDER ITS OWN KEY, IS CAUGHT UP by the group that
// still lists it. The rest lead on, their leader holding the progress of the log the
// node lost: raft would send it a commit past the end of the log it holds now, which
// etcd's raft panics on.
func TestAcrossProcesses_ANodeBackWithItsDiskLostIsCaughtUp(t *testing.T) {
	ip := lanIP(t)
	seen := &counts{}
	all := []*process{spawn(t, ip), spawn(t, ip), spawn(t, ip)}
	all[0].connect(all[1])
	all[0].connect(all[2])
	all[1].connect(all[2])
	for _, p := range all {
		p.open("disk", 2*time.Second)
	}
	require.Eventually(t, oneGroup(seen, "disk", 3, all...), 20*time.Second, 200*time.Millisecond, "%s", seen)
	require.Eventually(t, written("disk", "before", "1", all...), 10*time.Second, 200*time.Millisecond)
	for _, p := range all {
		require.Eventually(t, func() bool { return p.holds("disk", "before", "1") }, 10*time.Second, 100*time.Millisecond)
	}

	lost := all[0]
	if lost.leads("disk") {
		lost = all[1]
	}
	lost.kill()
	require.NoError(t, os.RemoveAll(lost.dir))
	require.NoError(t, os.MkdirAll(lost.dir, 0o755))
	lost.start()
	lost.open("disk", 2*time.Second)
	for _, p := range all {
		if p != lost {
			lost.connect(p)
		}
	}

	require.Eventually(t, func() bool { return lost.holds("disk", "before", "1") }, 30*time.Second, 200*time.Millisecond,
		"the node back with its disk lost was never caught up")
	require.Eventually(t, oneGroup(seen, "disk", 3, all...), 20*time.Second, 200*time.Millisecond, "%s", seen)
	require.Eventually(t, written("disk", "after", "2", all...), 10*time.Second, 200*time.Millisecond)
	require.Eventually(t, func() bool { return lost.holds("disk", "after", "2") }, 10*time.Second, 100*time.Millisecond)
}

// WHAT A GROUP COMMITTED OUTLIVES THE PROCESS: a node that formed alone and took a
// write is killed, and started again its group is the one it formed and holds the
// write. A store keeping recent writes in its own buffers until a sync lost all of it.
func TestAcrossProcesses_AKilledNodeKeepsWhatItsGroupCommitted(t *testing.T) {
	ip := lanIP(t)
	seen := &counts{}
	a := spawn(t, ip)
	a.open("kept", time.Second)
	require.Eventually(t, oneGroup(seen, "kept", 1, a), 10*time.Second, 100*time.Millisecond, "%s", seen)
	require.Eventually(t, written("kept", "k", "v", a), 10*time.Second, 100*time.Millisecond)

	a.kill()
	a.start()
	require.NotEqual(t, "keys 0", a.do("keys /raft/kept"), "the group's log did not outlive the process")
	a.open("kept", time.Second)
	require.Eventually(t, func() bool { return a.holds("kept", "k", "v") }, 10*time.Second, 100*time.Millisecond,
		"the group does not hold what it committed before the process died")
	require.Eventually(t, written("kept", "after", "v", a), 10*time.Second, 100*time.Millisecond,
		"the group it came back to takes no write")
}
